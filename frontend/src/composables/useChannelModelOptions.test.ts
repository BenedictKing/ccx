import { computed, effectScope, nextTick, ref, type EffectScope } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api, type Channel, type ModelsResponse, type TextChannelKind } from '../services/api'
import { useChannelModelOptions } from './useChannelModelOptions'

vi.mock('../services/api', () => ({ api: { getProtocolChannelModels: vi.fn() } }))
const scopes: EffectScope[] = []
const response = (...models: string[]): ModelsResponse => ({
  object: 'list', data: models.map(id => ({ id, object: 'model', created: 0, owned_by: 'test' }))
})
const channel = (values: Partial<Channel> = {}): Channel => ({
  channelUid: 'a', index: 0, name: 'A', serviceType: 'responses', baseUrl: 'https://example.invalid', apiKeys: [], status: 'active', ...values
})
function setup(initial: Channel[]) {
  const scope = effectScope()
  scopes.push(scope)
  const type = ref<TextChannelKind>('responses')
  const channels = ref(initial)
  const models = scope.run(() => useChannelModelOptions(computed(() => type.value), computed(() => channels.value)))!
  return { type, channels, models }
}

describe('useChannelModelOptions', () => {
  beforeEach(() => vi.mocked(api.getProtocolChannelModels).mockReset())
  afterEach(() => scopes.splice(0).forEach(scope => scope.stop()))

  it('使用协议渠道的映射、能力和 Key 模型，排除备用渠道、规则和停用 Key', () => {
    const { models } = setup([
      channel({
        apiKeys: ['active', 'paused'],
        apiKeyConfigs: [{ key: 'active', models: ['key-model'] }, { key: 'paused', enabled: false, models: ['paused-model'] }],
        modelMapping: { sol: 'gpt-5.4', '*': 'gpt-blocked' },
        modelCapabilities: { 'claude-haiku-4-5': {}, '^regex.*': {} },
        supportedModels: ['*', '!gpt-blocked']
      }),
      channel({ channelUid: 'backup', index: 1, status: 'disabled', modelMapping: { backup: 'backup-model' } })
    ])
    expect([...models.modelOptions.value].sort()).toEqual(['claude-haiku-4-5', 'gpt-5.4', 'key-model', 'sol'])
  })

  it('通过当前协议查询多个有效 Key 的模型并去重，成功结果复用缓存', async () => {
    vi.mocked(api.getProtocolChannelModels).mockResolvedValueOnce(response('gpt-5.4', 'shared')).mockResolvedValueOnce(response('claude-haiku-4-5', 'shared'))
    const { models } = setup([channel({ apiKeys: ['one', 'two', 'paused'], apiKeyConfigs: [{ key: 'paused', enabled: false }] })])
    await models.fetchModelOptions()
    expect(api.getProtocolChannelModels).toHaveBeenCalledWith('responses', 0, { key: 'one' })
    expect(api.getProtocolChannelModels).toHaveBeenCalledWith('responses', 0, { key: 'two' })
    expect([...models.modelOptions.value].sort()).toEqual(['claude-haiku-4-5', 'gpt-5.4', 'shared'])
    await models.fetchModelOptions()
    expect(api.getProtocolChannelModels).toHaveBeenCalledTimes(2)
  })

  it('渠道配置变化会清理旧模型并按新的索引重新查询', async () => {
    vi.mocked(api.getProtocolChannelModels).mockResolvedValueOnce(response('old-model')).mockResolvedValueOnce(response('new-model'))
    const { channels, models } = setup([channel({ apiKeys: ['one'] })])
    await models.fetchModelOptions()
    channels.value = [channel({ index: 2, baseUrl: 'https://new.example.invalid', apiKeys: ['one'] })]
    await nextTick()
    expect(models.modelOptions.value).toEqual([])
    await models.fetchModelOptions()
    expect(api.getProtocolChannelModels).toHaveBeenLastCalledWith('responses', 2, { key: 'one' })
    expect(models.modelOptions.value).toEqual(['new-model'])
  })

  it('切换协议后忽略旧协议的延迟模型响应', async () => {
    let resolve!: (_response: ModelsResponse) => void
    vi.mocked(api.getProtocolChannelModels).mockImplementationOnce(() => new Promise(done => { resolve = done }))
    const { type, models } = setup([channel({ apiKeys: ['one'] })])
    const pending = models.fetchModelOptions()
    type.value = 'gemini'
    await nextTick()
    resolve(response('old-response-model'))
    await pending
    expect(models.modelOptions.value).toEqual([])
    vi.mocked(api.getProtocolChannelModels).mockResolvedValue(response('gemini-3.5-flash'))
    await models.fetchModelOptions()
    expect(api.getProtocolChannelModels).toHaveBeenLastCalledWith('gemini', 0, { key: 'one' })
    expect(models.modelOptions.value).toEqual(['gemini-3.5-flash'])
  })

  it('获取失败后可再次打开重试，保留其他渠道已经取得的模型', async () => {
    vi.mocked(api.getProtocolChannelModels).mockResolvedValueOnce(response('available')).mockRejectedValueOnce(new Error('offline'))
    const { models } = setup([channel({ apiKeys: ['one', 'two'] })])
    await models.fetchModelOptions()
    expect(models.modelOptions.value).toEqual(['available'])
    expect(models.modelsUnavailable.value).toBe(false)
    vi.mocked(api.getProtocolChannelModels).mockResolvedValue(response('recovered'))
    await models.fetchModelOptions()
    expect(api.getProtocolChannelModels).toHaveBeenCalledTimes(3)
    expect([...models.modelOptions.value].sort()).toEqual(['available', 'recovered'])
  })

  it('无法获取且没有已配置模型时报告失败，重试成功后清除状态', async () => {
    vi.mocked(api.getProtocolChannelModels).mockRejectedValueOnce(new Error('offline'))
    const { models } = setup([channel({ apiKeys: ['one'] })])
    await models.fetchModelOptions()
    expect(models.modelsUnavailable.value).toBe(true)
    vi.mocked(api.getProtocolChannelModels).mockResolvedValue(response('available'))
    await models.fetchModelOptions()
    expect(models.modelsUnavailable.value).toBe(false)
  })
})
