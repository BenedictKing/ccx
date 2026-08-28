// @vitest-environment jsdom
import { mount } from '@vue/test-utils'
import { defineComponent, nextTick } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { ChannelProtocolRoute } from '../../services/api'
import ProtocolModelAvailability from './ProtocolModelAvailability.vue'

const autopilotMocks = vi.hoisted(() => ({
  autoDiscoverChannel: vi.fn(),
  getChannelAutoStatus: vi.fn(),
}))

vi.mock('../../services/autopilot-api', () => autopilotMocks)

vi.mock('../../i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: Record<string, number>) => {
      if (params?.count !== undefined) return `${key}:${params.count}`
      if (params?.available !== undefined) return `${key}:${params.available}/${params.total}`
      return key
    },
  }),
}))

const passthroughStub = defineComponent({
  template: '<span><slot /></span>',
})

// v-tooltip 需要渲染 activator 插槽里的内容（来源标签 chip 在 tooltip 内部）。
const tooltipStub = defineComponent({
  template: '<span><slot name="activator" :props="{}" /><slot /></span>',
})

const baseStubs = {
  VChip: passthroughStub,
  VIcon: passthroughStub,
  VTooltip: tooltipStub,
}

const buttonStub = defineComponent({
  emits: ['click'],
  template: '<button @click="$emit(\'click\')"><slot /></button>',
})

describe('ProtocolModelAvailability', () => {
  beforeEach(() => {
    autopilotMocks.autoDiscoverChannel.mockReset()
    autopilotMocks.getChannelAutoStatus.mockReset()
  })
  it('按协议分组展示各自的可用模型', () => {
    const wrapper = mount(ProtocolModelAvailability, {
      props: {
        routes: [
          {
            kind: 'messages', index: 0, name: 'fastaitoken-claude', serviceType: 'claude',
            supportedModels: ['gpt-5.6-terra', 'gpt-5.6-sol', 'gpt-5.6-sol'],
          },
          {
            kind: 'chat', index: 0, name: 'fastaitoken-chat', serviceType: 'openai',
            supportedModels: ['gpt-5.6-sol'],
          },
          {
            kind: 'responses', index: 0, name: 'fastaitoken-codex', serviceType: 'responses',
            supportedModels: ['codex-auto-review'],
          },
        ],
      },
      global: {
        stubs: baseStubs,
      },
    })

    const messages = wrapper.get('[data-kind="messages"]')
    const chat = wrapper.get('[data-kind="chat"]')
    const responses = wrapper.get('[data-kind="responses"]')

    expect(messages.text()).toContain('/v1/messages')
    expect(messages.text()).toContain('gpt-5.6-sol')
    expect(messages.text()).toContain('gpt-5.6-terra')
    expect(messages.text().match(/gpt-5\.6-sol/g)).toHaveLength(1)
    expect(chat.text()).toContain('/v1/chat/completions')
    expect(chat.text()).not.toContain('gpt-5.6-terra')
    expect(responses.text()).toContain('/v1/responses')
    expect(responses.text()).toContain('codex-auto-review')
  })

  it('模型清单一致时只展示一次跨协议共有模型', () => {
    const routes: ChannelProtocolRoute[] = (['messages', 'chat', 'responses'] as const).map((kind, index) => ({
      kind,
      index,
      name: `shared-${kind}`,
      serviceType: kind === 'messages' ? 'claude' : kind === 'chat' ? 'openai' : 'responses',
      modelInventoryKnown: true,
      discoveredModels: ['model-a', 'model-b'],
    }))
    const wrapper = mount(ProtocolModelAvailability, {
      props: { routes },
      global: {
        stubs: baseStubs,
      },
    })

    const shared = wrapper.get('.protocol-model-shared')
    expect(shared.text()).toContain('channelEditor.protocolModels.sharedTitle')
    expect(shared.text()).toContain('channelEditor.protocolModels.sharedHint:3')
    expect(shared.text()).toContain('model-a')
    expect(shared.text()).toContain('model-b')
    for (const kind of ['messages', 'chat', 'responses']) {
      const route = wrapper.get(`[data-kind="${kind}"]`)
      expect(route.text()).not.toContain('model-a')
      expect(route.text()).not.toContain('model-b')
      expect(route.text()).toContain('channelEditor.protocolModels.specificEmpty')
    }
  })

  it('先展示跨协议交集，再按协议展示独有模型', () => {
    const wrapper = mount(ProtocolModelAvailability, {
      props: {
        routes: [
          {
            kind: 'messages', index: 0, name: 'messages', serviceType: 'claude',
            discoveredModels: ['shared-model', 'claude-only'],
          },
          {
            kind: 'chat', index: 0, name: 'chat', serviceType: 'openai',
            discoveredModels: ['shared-model', 'chat-only'],
          },
          {
            kind: 'responses', index: 0, name: 'responses', serviceType: 'responses',
            discoveredModels: ['shared-model'],
          },
        ],
      },
      global: {
        stubs: baseStubs,
      },
    })

    const shared = wrapper.get('.protocol-model-shared')
    expect(shared.text()).toContain('shared-model')
    expect(shared.text()).not.toContain('claude-only')
    expect(shared.text()).not.toContain('chat-only')
    expect(wrapper.get('[data-kind="messages"]').text()).toContain('claude-only')
    expect(wrapper.get('[data-kind="messages"]').text()).not.toContain('shared-model')
    expect(wrapper.get('[data-kind="chat"]').text()).toContain('chat-only')
    expect(wrapper.get('[data-kind="responses"]').text()).toContain('channelEditor.protocolModels.specificEmpty')
  })

  it('已发现的空模型协议会使跨协议交集为空', () => {
    const wrapper = mount(ProtocolModelAvailability, {
      props: {
        routes: [
          {
            kind: 'messages', index: 0, name: 'messages', serviceType: 'claude',
            modelInventoryKnown: true, discoveredModels: ['model-a'],
          },
          {
            kind: 'chat', index: 0, name: 'chat', serviceType: 'openai',
            modelInventoryKnown: true, discoveredModels: [],
          },
        ],
      },
      global: {
        stubs: baseStubs,
      },
    })

    expect(wrapper.find('.protocol-model-shared').exists()).toBe(false)
    expect(wrapper.get('[data-kind="messages"]').text()).toContain('model-a')
  })

  it('区分未记录模型范围与协议不可用', () => {
    const wrapper = mount(ProtocolModelAvailability, {
      props: {
        routes: [{ kind: 'gemini', index: 0, name: 'gemini', serviceType: 'gemini' }],
      },
      global: {
        stubs: baseStubs,
      },
    })

    const gemini = wrapper.get('[data-kind="gemini"]')
    expect(gemini.text()).toContain('channelEditor.protocolModels.empty')
    expect(gemini.text()).not.toContain('channelEditor.protocolModels.count:0')
  })

  it('已发现到空模型清单时不回退配置白名单', () => {
    const wrapper = mount(ProtocolModelAvailability, {
      props: {
        routes: [{
          kind: 'responses', upstreamKind: 'chat', index: 0, name: 'chat-through-responses', serviceType: 'openai',
          supportedModels: ['configured-model'], modelInventoryKnown: true, discoveredModels: [],
          modelBindings: [{ credentialUid: 'cred-empty', keyMask: 'sk-e***001', models: [] }],
        }],
      },
      global: {
        stubs: baseStubs,
      },
    })

    const chat = wrapper.get('[data-kind="chat"]')
    expect(chat.text()).toContain('/v1/chat/completions')
    expect(chat.text()).toContain('channelEditor.protocolModels.count:0')
    expect(chat.text()).not.toContain('configured-model')
  })

  it('优先展示 endpoint profile 模型并标记 Key 差异', () => {
    const wrapper = mount(ProtocolModelAvailability, {
      props: {
        routes: [{
          kind: 'messages', index: 0, name: 'volcengine-claude', serviceType: 'claude',
          supportedModels: ['configured-model'],
          discoveredModels: ['actual-model'],
          modelBindings: [
            { credentialUid: 'cred-a', keyMask: 'ark-a***001', models: ['actual-model'] },
            { credentialUid: 'cred-b', keyMask: 'ark-b***002', models: ['other-model'] },
          ],
        }],
      },
      global: {
        stubs: baseStubs,
      },
    })

    const messages = wrapper.get('[data-kind="messages"]')
    expect(messages.text()).toContain('actual-model')
    expect(messages.text()).not.toContain('configured-model')
    expect(messages.text()).toContain('channelEditor.protocolModels.diffCount:2')
    expect(messages.text()).toContain('ark-a***001')
    expect(messages.text()).toContain('ark-b***002')
    expect(messages.text()).toContain('channelEditor.protocolModels.coverage:1/2')
    // 模型列表交由 ModelChipList 渲染，未溢出两行时不出现展开按钮。
    expect(messages.findAll('.model-chip-list__toggle')).toHaveLength(0)
    expect(messages.text()).toContain('actual-model')
    expect(messages.text()).toContain('other-model')
  })

  it('按相同可用 Key 集合归并共同与专有模型', () => {
    const wrapper = mount(ProtocolModelAvailability, {
      props: {
        routes: [{
          kind: 'messages', index: 0, name: 'volcengine-agent-plan', serviceType: 'claude',
          discoveredModels: ['shared-model', 'coding-exclusive', 'agent-exclusive'],
          modelBindings: [
            { credentialUid: 'cred-f5', keyMask: 'ark-f5***2fd', models: ['shared-model', 'coding-exclusive'] },
            { credentialUid: 'cred-de', keyMask: 'de5371***84e', models: ['shared-model', 'coding-exclusive'] },
            { credentialUid: 'cred-9b', keyMask: 'ark-9b***8db', models: ['shared-model', 'agent-exclusive'] },
            { credentialUid: 'cred-ec', keyMask: 'ark-ec***570', models: ['shared-model', 'agent-exclusive'] },
          ],
        }],
      },
      global: {
        stubs: baseStubs,
      },
    })

    const messages = wrapper.get('[data-kind="messages"]')
    const groups = messages.findAll('.protocol-model-coverage-group')
    const shared = groups.find(group => group.text().includes('shared-model'))
    const codingOnly = groups.find(group => group.text().includes('coding-exclusive'))
    const agentOnly = groups.find(group => group.text().includes('agent-exclusive'))

    expect(messages.text()).toContain('channelEditor.protocolModels.diffCount:2')
    expect(groups).toHaveLength(3)
    expect(shared?.text()).toContain('channelEditor.protocolModels.coverageGroupShared:4')
    expect(shared?.text()).toContain('ark-f5***2fd')
    expect(shared?.text()).toContain('ark-ec***570')
    expect(codingOnly?.text()).toContain('channelEditor.protocolModels.coverageGroupExclusive:2')
    expect(codingOnly?.text()).toContain('ark-f5***2fd')
    expect(codingOnly?.text()).toContain('de5371***84e')
    expect(codingOnly?.text()).not.toContain('ark-9b***8db')
    expect(agentOnly?.text()).toContain('ark-9b***8db')
    expect(agentOnly?.text()).toContain('ark-ec***570')
    expect(agentOnly?.text()).not.toContain('ark-f5***2fd')
  })

  it('多 Key 模型一致时直接展示 Key 与模型', () => {
    const wrapper = mount(ProtocolModelAvailability, {
      props: {
        routes: [{
          kind: 'messages', index: 0, name: 'multi-key', serviceType: 'claude',
          discoveredModels: ['model-a', 'model-b'],
          modelBindings: [
            { credentialUid: 'cred-a', keyMask: 'sk-a***001', models: ['model-a', 'model-b'] },
            { credentialUid: 'cred-b', keyMask: 'sk-b***002', models: ['model-a', 'model-b'] },
          ],
        }],
      },
      global: {
        stubs: baseStubs,
      },
    })

    const messages = wrapper.get('[data-kind="messages"]')
    expect(messages.text()).toContain('channelEditor.protocolModels.consistent:2')
    expect(messages.text()).not.toContain('channelEditor.protocolModels.diffCount')
    expect(messages.find('.protocol-model-route__coverage-groups').exists()).toBe(true)
    // 一致时不再重复展示“共同可用”分组元信息，Key 与模型各只出现一次。
    expect(messages.findAll('.protocol-model-coverage-group')).toHaveLength(0)
    expect(messages.text()).not.toContain('channelEditor.protocolModels.coverageGroupShared')
    expect(messages.text()).toContain('sk-a***001')
    expect(messages.text()).toContain('sk-b***002')
    expect(messages.text().match(/model-a/g)).toHaveLength(1)
    expect(messages.text().match(/model-b/g)).toHaveLength(1)
  })

  it('多 Key 归组展示时不再重复渲染平铺模型列表', () => {
    const wrapper = mount(ProtocolModelAvailability, {
      props: {
        routes: [{
          kind: 'messages', index: 0, name: 'multi-key', serviceType: 'claude',
          discoveredModels: ['grok-4.5'],
          modelBindings: [
            { credentialUid: 'cred-a', keyMask: 'sk-a***001', models: ['grok-4.5'] },
            { credentialUid: 'cred-b', keyMask: 'sk-b***002', models: ['grok-4.5'] },
          ],
        }],
      },
      global: {
        stubs: baseStubs,
      },
    })

    const messages = wrapper.get('[data-kind="messages"]')
    expect(messages.text().match(/grok-4\.5/g)).toHaveLength(1)
    expect(messages.text()).not.toContain('channelEditor.protocolModels.specificEmpty')
  })

  it('多 Key 归组展示时不再出现"尚无自动发现结果"兜底文案', () => {
    const wrapper = mount(ProtocolModelAvailability, {
      props: {
        routes: (['messages', 'chat', 'gemini'] as const).map((kind, index) => ({
          kind,
          index,
          name: `metapi-${kind}`,
          serviceType: kind === 'messages' ? 'claude' : kind === 'chat' ? 'openai' : 'gemini',
          modelInventoryKnown: true,
          discoveredModels: ['glm-5.2', `${kind}-only`],
          modelBindings: [
            { credentialUid: 'cred-a', keyMask: '***835z', models: ['glm-5.2', `${kind}-only`] },
            { credentialUid: 'cred-b', keyMask: '***kLPU', models: ['glm-5.2', `${kind}-only`] },
          ],
        })),
      },
      global: {
        stubs: baseStubs,
      },
    })

    for (const kind of ['messages', 'chat', 'gemini']) {
      const route = wrapper.get(`[data-kind="${kind}"]`)
      expect(route.text()).toContain('channelEditor.protocolModels.consistent:2')
      expect(route.text()).not.toContain('channelEditor.protocolModels.empty')
      expect(route.text()).not.toContain('channelEditor.protocolModels.specificEmpty')
    }
  })

  it('展示模型清单的发现时间、来源和说明', () => {
    const wrapper = mount(ProtocolModelAvailability, {
      props: {
        routes: [{
          kind: 'messages', index: 0, name: 'volcengine-claude', serviceType: 'claude',
          modelInventoryKnown: true,
          discoveredModels: ['glm-5.2'],
          modelsDiscoveredAt: '2026-07-22T00:42:12Z',
          modelDiscoverySource: 'control_plane',
          modelDiscoveryMessage: '火山管控面 Coding Plan 模型清单',
        }],
      },
      global: {
        stubs: baseStubs,
      },
    })

    const messages = wrapper.get('[data-kind="messages"]')
    expect(messages.text()).toContain('channelEditor.protocolModels.lastDiscovered')
    expect(messages.text()).toContain('channelEditor.protocolModels.source.controlPlane')
    expect(messages.text()).toContain('火山管控面 Coding Plan 模型清单')
  })

  it('只在区块头部重新发现，并标记发现但未配置的协议', async () => {
    autopilotMocks.autoDiscoverChannel.mockResolvedValue({ discoveryStarted: true })
    autopilotMocks.getChannelAutoStatus.mockResolvedValue({
      autoManaged: true,
      discovery: { status: 'done' },
    })
    const wrapper = mount(ProtocolModelAvailability, {
      props: {
        routes: [
          {
            kind: 'responses', upstreamKind: 'responses', index: 2, channelUid: 'ch-responses',
            name: 'multi-protocol', serviceType: 'responses', configured: true,
            modelInventoryKnown: true, discoveredModels: ['gpt-5.4'],
          },
          {
            kind: 'messages', upstreamKind: 'messages', index: -1, channelUid: 'ch-responses',
            name: 'multi-protocol', serviceType: 'claude', configured: false,
            modelInventoryKnown: true, discoveredModels: ['claude-sonnet-4-6'],
          },
        ],
      },
      global: {
        stubs: {
          ...baseStubs,
          VAlert: passthroughStub,
          VBtn: buttonStub,
          VProgressCircular: passthroughStub,
        },
      },
    })

    expect(wrapper.findAll('.protocol-model-availability__rediscover-all')).toHaveLength(1)
    expect(wrapper.get('[data-kind="messages"]').classes()).toContain('protocol-model-route--unconfigured')
    expect(wrapper.get('[data-kind="messages"]').text()).toContain('channelEditor.protocolModels.unconfiguredProtocol')

    await wrapper.get('.protocol-model-availability__rediscover-all').trigger('click')
    await vi.waitFor(() => {
      expect(autopilotMocks.autoDiscoverChannel).toHaveBeenCalledWith('responses', 'ch-responses')
    })
  })
})

describe('整组重新发现（triggered 契约）', () => {
  const freshTimestamp = () => new Date().toISOString()

  const mountWithRoutes = (routes: ChannelProtocolRoute[]) => mount(ProtocolModelAvailability, {
    props: { routes },
    global: {
      stubs: {
        ...baseStubs,
        VAlert: passthroughStub,
        VBtn: buttonStub,
        VProgressCircular: passthroughStub,
      },
    },
  })

  it('响应带 triggered 时并行轮询全部兄弟上游，全部完成后统一刷新', async () => {
    autopilotMocks.autoDiscoverChannel.mockResolvedValue({
      channelUid: 'ch-msg',
      discoveryStarted: true,
      triggered: [
        { kind: 'messages', channelUid: 'ch-msg' },
        { kind: 'chat', channelUid: 'ch-chat' },
      ],
    })
    autopilotMocks.getChannelAutoStatus.mockResolvedValue({
      autoManaged: true,
      discovery: { status: 'done' },
    })
    const wrapper = mountWithRoutes([
      {
        kind: 'messages', index: 0, channelUid: 'ch-msg', name: 'ch', serviceType: 'claude',
        modelInventoryKnown: true, discoveredModels: ['m1'], modelsDiscoveredAt: freshTimestamp(),
      },
      {
        kind: 'chat', index: 1, channelUid: 'ch-chat', name: 'ch', serviceType: 'openai',
        modelInventoryKnown: true, discoveredModels: ['m1'], modelsDiscoveredAt: freshTimestamp(),
      },
    ])

    await wrapper.get('.protocol-model-availability__rediscover-all').trigger('click')
    await vi.waitFor(() => {
      expect(wrapper.emitted('refreshed')).toHaveLength(1)
    })
    expect(autopilotMocks.getChannelAutoStatus).toHaveBeenCalledWith('messages', 'ch-msg')
    expect(autopilotMocks.getChannelAutoStatus).toHaveBeenCalledWith('chat', 'ch-chat')
  })

  it('旧后端无 triggered 字段时回退为只轮询主路由', async () => {
    autopilotMocks.autoDiscoverChannel.mockResolvedValue({
      channelUid: 'ch-msg-fb',
      discoveryStarted: true,
    })
    autopilotMocks.getChannelAutoStatus.mockResolvedValue({
      autoManaged: true,
      discovery: { status: 'done' },
    })
    const wrapper = mountWithRoutes([
      {
        kind: 'messages', index: 0, channelUid: 'ch-msg-fb', name: 'ch', serviceType: 'claude',
        modelInventoryKnown: true, discoveredModels: ['m1'], modelsDiscoveredAt: freshTimestamp(),
      },
      {
        kind: 'chat', index: 1, channelUid: 'ch-chat-fb', name: 'ch', serviceType: 'openai',
        modelInventoryKnown: true, discoveredModels: ['m1'], modelsDiscoveredAt: freshTimestamp(),
      },
    ])

    await wrapper.get('.protocol-model-availability__rediscover-all').trigger('click')
    await vi.waitFor(() => {
      expect(wrapper.emitted('refreshed')).toHaveLength(1)
    })
    expect(autopilotMocks.getChannelAutoStatus).toHaveBeenCalledWith('messages', 'ch-msg-fb')
    expect(autopilotMocks.getChannelAutoStatus).not.toHaveBeenCalledWith('chat', 'ch-chat-fb')
  })

  it('整组轮询中任一协议失败时在错误区展示该协议，其余继续等待', async () => {
    autopilotMocks.autoDiscoverChannel.mockResolvedValue({
      channelUid: 'ch-msg-f',
      discoveryStarted: true,
      triggered: [
        { kind: 'messages', channelUid: 'ch-msg-f' },
        { kind: 'chat', channelUid: 'ch-chat-f' },
      ],
    })
    autopilotMocks.getChannelAutoStatus.mockImplementation((kind: string, channelUid: string) => Promise.resolve({
      autoManaged: true,
      discovery: channelUid === 'ch-chat-f'
        ? { status: 'failed', error: 'upstream boom' }
        : { status: 'done' },
    }))
    const wrapper = mountWithRoutes([
      {
        kind: 'messages', index: 0, channelUid: 'ch-msg-f', name: 'ch', serviceType: 'claude',
        modelInventoryKnown: true, discoveredModels: ['m1'], modelsDiscoveredAt: freshTimestamp(),
      },
      {
        kind: 'chat', index: 1, channelUid: 'ch-chat-f', name: 'ch', serviceType: 'openai',
        modelInventoryKnown: true, discoveredModels: ['m1'], modelsDiscoveredAt: freshTimestamp(),
      },
    ])

    await wrapper.get('.protocol-model-availability__rediscover-all').trigger('click')
    await vi.waitFor(() => {
      expect(wrapper.text()).toContain('channelEditor.protocolModels.rediscoverProtocolFailed')
    })
    // 失败的兄弟协议完成轮询后才展示错误，且不做整体刷新。
    expect(autopilotMocks.getChannelAutoStatus).toHaveBeenCalledWith('messages', 'ch-msg-f')
    expect(wrapper.emitted('refreshed')).toBeUndefined()
  })
})

describe('查看时静默自愈', () => {
  it('缺失或超过 24h 的发现时间触发后台刷新，新鲜路由不触发', async () => {
    autopilotMocks.autoDiscoverChannel.mockResolvedValue({ channelUid: 'x', discoveryStarted: true })
    autopilotMocks.getChannelAutoStatus.mockResolvedValue({
      autoManaged: true,
      discovery: { status: 'done' },
    })
    const wrapper = mount(ProtocolModelAvailability, {
      props: {
        routes: [
          {
            kind: 'messages', index: 0, channelUid: 'ch-heal-stale', name: 'ch', serviceType: 'claude',
            modelInventoryKnown: true, discoveredModels: ['m1'],
            modelsDiscoveredAt: new Date(Date.now() - 25 * 60 * 60 * 1000).toISOString(),
          },
          {
            kind: 'chat', index: 1, channelUid: 'ch-heal-fresh', name: 'ch', serviceType: 'openai',
            modelInventoryKnown: true, discoveredModels: ['m1'],
            modelsDiscoveredAt: new Date().toISOString(),
          },
          {
            kind: 'responses', index: 2, channelUid: 'ch-heal-missing', name: 'ch', serviceType: 'responses',
            modelInventoryKnown: true, discoveredModels: ['m1'],
          },
          {
            kind: 'gemini', index: 3, channelUid: 'ch-heal-invalid', name: 'ch', serviceType: 'gemini',
            modelInventoryKnown: true, discoveredModels: ['m1'],
            modelsDiscoveredAt: 'not-a-date',
          },
        ],
      },
      global: { stubs: baseStubs },
    })

    await vi.waitFor(() => {
      expect(autopilotMocks.autoDiscoverChannel).toHaveBeenCalledWith('messages', 'ch-heal-stale')
      expect(autopilotMocks.autoDiscoverChannel).toHaveBeenCalledWith('responses', 'ch-heal-missing')
      expect(autopilotMocks.autoDiscoverChannel).toHaveBeenCalledWith('gemini', 'ch-heal-invalid')
    })
    expect(autopilotMocks.autoDiscoverChannel).not.toHaveBeenCalledWith('chat', 'ch-heal-fresh')
    // 多条陈旧路由完成后只统一刷新一次。
    await vi.waitFor(() => {
      expect(wrapper.emitted('refreshed')).toHaveLength(1)
    })
  })

  it('自愈进行中展示轻量状态，缺时间戳路由显示自动更新文案', async () => {
    autopilotMocks.autoDiscoverChannel.mockResolvedValue({ channelUid: 'x', discoveryStarted: true })
    // 首次调用是挂载时的状态查询（done，不进入「自动检测中」），后续为自愈轮询（running）。
    let statusCalls = 0
    autopilotMocks.getChannelAutoStatus.mockImplementation(() => Promise.resolve({
      autoManaged: true,
      discovery: statusCalls++ === 0 ? { status: 'done' } : { status: 'running' },
    }))
    const wrapper = mount(ProtocolModelAvailability, {
      props: {
        routes: [{
          kind: 'messages', index: 0, channelUid: 'ch-heal-ui', name: 'ch', serviceType: 'claude',
          modelInventoryKnown: true, discoveredModels: ['m1'],
        }],
      },
      global: { stubs: baseStubs },
    })
    await nextTick()

    expect(wrapper.text()).toContain('channelEditor.protocolModels.autoRefreshing')
    expect(wrapper.get('[data-kind="messages"]').text())
      .toContain('channelEditor.protocolModels.discoveryUpdating')
    wrapper.unmount()
  })

  it('自愈触发失败时静默保留旧数据并显示中性的发现时间未知', async () => {
    autopilotMocks.autoDiscoverChannel.mockRejectedValue(new Error('network down'))
    autopilotMocks.getChannelAutoStatus.mockResolvedValue({
      autoManaged: true,
      discovery: { status: 'done' },
    })
    const wrapper = mount(ProtocolModelAvailability, {
      props: {
        routes: [{
          kind: 'messages', index: 0, channelUid: 'ch-heal-fail', name: 'ch', serviceType: 'claude',
          modelInventoryKnown: true, discoveredModels: ['m1'],
        }],
      },
      global: { stubs: baseStubs },
    })

    await vi.waitFor(() => {
      expect(wrapper.get('[data-kind="messages"]').text())
        .toContain('channelEditor.protocolModels.discoveryTimeUnknown')
    })
    expect(wrapper.get('[data-kind="messages"]').text()).toContain('m1')
    expect(wrapper.emitted('refreshed')).toBeUndefined()
  })

  it('同一路由 1 小时内不重复触发自愈（模块级冷却）', async () => {
    autopilotMocks.autoDiscoverChannel.mockResolvedValue({ channelUid: 'x', discoveryStarted: true })
    autopilotMocks.getChannelAutoStatus.mockResolvedValue({
      autoManaged: true,
      discovery: { status: 'done' },
    })
    const staleRoute: ChannelProtocolRoute = {
      kind: 'messages', index: 0, channelUid: 'ch-heal-cooldown', name: 'ch', serviceType: 'claude',
      modelInventoryKnown: true, discoveredModels: ['m1'],
    }
    const first = mount(ProtocolModelAvailability, {
      props: { routes: [staleRoute] },
      global: { stubs: baseStubs },
    })
    await vi.waitFor(() => {
      expect(autopilotMocks.autoDiscoverChannel).toHaveBeenCalledWith('messages', 'ch-heal-cooldown')
    })
    first.unmount()

    autopilotMocks.autoDiscoverChannel.mockClear()
    const second = mount(ProtocolModelAvailability, {
      props: { routes: [staleRoute] },
      global: { stubs: baseStubs },
    })
    await nextTick()
    await new Promise(resolve => setTimeout(resolve, 20))
    expect(autopilotMocks.autoDiscoverChannel).not.toHaveBeenCalled()
    second.unmount()
  })
})

describe('滚动别名展示', () => {
  it('滚动别名排在具体版本之后并带别名徽标', () => {
    const wrapper = mount(ProtocolModelAvailability, {
      props: {
        routes: [{
          kind: 'messages', index: 0, name: 'alias-test', serviceType: 'claude',
          modelInventoryKnown: true,
          discoveredModels: ['zeta-model', 'alpha-latest', 'beta-evolving', 'a-model'],
        }],
      },
      global: { stubs: baseStubs },
    })

    const chips = wrapper.get('[data-kind="messages"]').findAll('.model-chip-list__model')
    const texts = chips.map(chip => chip.text())
    expect(texts).toHaveLength(4)
    expect(texts[0]).toContain('a-model')
    expect(texts[1]).toContain('zeta-model')
    expect(texts[2]).toContain('alpha-latest')
    expect(texts[3]).toContain('beta-evolving')
    expect(texts[0]).not.toContain('channelEditor.protocolModels.aliasBadge')
    expect(texts[1]).not.toContain('channelEditor.protocolModels.aliasBadge')
    expect(texts[2]).toContain('channelEditor.protocolModels.aliasBadge')
    expect(texts[3]).toContain('channelEditor.protocolModels.aliasBadge')
  })
})
