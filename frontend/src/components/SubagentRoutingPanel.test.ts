// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError, type Channel, type SubagentRoutingView } from '../services/api'
import SubagentRoutingPanel from './SubagentRoutingPanel.vue'

vi.mock('../i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('vue-router', () => ({ onBeforeRouteLeave: vi.fn(), onBeforeRouteUpdate: vi.fn() }))
vi.mock('../services/api', async importOriginal => {
  const actual = await importOriginal<typeof import('../services/api')>()
  return { ...actual, api: { getSubagentRouting: vi.fn(), updateSubagentRouting: vi.fn(), getProtocolChannelModels: vi.fn() } }
})
const { api } = await import('../services/api')
const cloneView = () => JSON.parse(JSON.stringify(view)) as SubagentRoutingView

const view: SubagentRoutingView = {
  protocol: 'responses',
  activated: true,
  revision: 'r1',
  legacyMixed: false,
  policy: {
    model: 'sol',
    channels: [
      { channelUid: 'a', enabled: true },
      { channelUid: 'b', enabled: true }
    ]
  },
  channels: [
    {
      channelUid: 'a',
      channelIndex: 0,
      channelName: 'A',
      serviceType: 'responses',
      status: 'active',
      effectiveState: 'active',
      enabled: true,
      requestModel: 'sol',
      resolvedModel: 'actual-a',
      modelSupported: true
    },
    {
      channelUid: 'b',
      channelIndex: 1,
      channelName: 'B',
      serviceType: 'responses',
      status: 'active',
      effectiveState: 'active',
      enabled: true,
      requestModel: 'sol',
      resolvedModel: 'actual-b',
      modelSupported: true
    }
  ]
}

const wrappers: ReturnType<typeof mount>[] = []
function mountPanel(availableChannels: Channel[] = []) {
  const wrapper = mount(SubagentRoutingPanel, {
    props: { channelType: 'responses', availableChannels },
    attachTo: document.body,
    global: {
      stubs: {
        draggable: {
          props: ['modelValue'],
          template:
            '<div><template v-for="(element, index) in modelValue" :key="element.channelUid"><slot name="item" :element="element" :index="index" /></template></div>'
        },
        'v-switch': {
          props: ['modelValue', 'disabled'],
          emits: ['update:modelValue'],
          template:
            '<input type="checkbox" class="policy-switch" :checked="modelValue" :disabled="disabled" @change="$emit(\'update:modelValue\', $event.target.checked)">'
        },
        'v-btn': {
          props: ['disabled'],
          emits: ['click'],
          template: '<button :disabled="disabled" @click="$emit(\'click\')"><slot /></button>'
        },
        'v-autocomplete': {
          name: 'ModelSelect',
          props: ['modelValue', 'disabled', 'items'],
          emits: ['update:modelValue', 'update:menu', 'update:search'],
          template: '<input class="policy-model" :value="modelValue" :disabled="disabled" @input="$emit(\'update:modelValue\', $event.target.value)">'
        },
        'v-text-field': true,
        'v-icon': true,
        'v-chip': { template: '<div><slot /></div>' },
        'v-alert': { template: '<div><slot /></div>' },
        'v-card': { template: '<div><slot /></div>' },
        'v-spacer': true,
        'v-divider': true,
        'v-skeleton-loader': true
      }
    }
  })
  wrappers.push(wrapper)
  return wrapper
}

describe('SubagentRoutingPanel', () => {
  afterEach(() => {
    wrappers.splice(0).forEach(wrapper => wrapper.unmount())
    vi.unstubAllGlobals()
  })
  beforeEach(() => {
    vi.mocked(api.getSubagentRouting).mockReset().mockResolvedValue(cloneView())
    vi.mocked(api.updateSubagentRouting).mockReset()
    vi.mocked(api.getProtocolChannelModels).mockReset()
  })

  it('接受未改动的迁移草稿也可直接启用', async () => {
    const draft = { ...cloneView(), activated: false }
    vi.mocked(api.getSubagentRouting).mockResolvedValue(draft)
    vi.mocked(api.updateSubagentRouting).mockResolvedValue(cloneView())
    const wrapper = mountPanel()
    await flushPromises()
    const save = wrapper.findAll('button').find(button => button.text().includes('app.actions.save'))!
    expect(save.attributes('disabled')).toBeUndefined()
    await save.trigger('click')
    await flushPromises()
    expect(api.updateSubagentRouting).toHaveBeenCalledWith('responses', 'r1', draft.policy)
  })

  it('按 UID 和开关保存草稿，并采用服务端返回的 revision', async () => {
    const wrapper = mountPanel()
    await flushPromises()
    await wrapper.findAll('.policy-switch')[0].setValue(false)
    const expectedPolicy = {
      model: 'sol',
      channels: [
        { channelUid: 'a', enabled: false },
        { channelUid: 'b', enabled: true }
      ]
    }
    vi.mocked(api.updateSubagentRouting).mockResolvedValue({ ...cloneView(), revision: 'r2', policy: expectedPolicy })
    await wrapper
      .findAll('button')
      .find(button => button.text().includes('app.actions.save'))!
      .trigger('click')
    await flushPromises()
    expect(api.updateSubagentRouting).toHaveBeenCalledWith('responses', 'r1', expectedPolicy)
    expect(wrapper.emitted('success')).toHaveLength(1)
  })

  it('保存冲突时保留草稿并自动获取最新配置', async () => {
    vi.mocked(api.updateSubagentRouting).mockRejectedValue(new ApiError('conflict', 409))
    const wrapper = mountPanel()
    await flushPromises()
    const firstSwitch = wrapper.findAll('.policy-switch')[0]
    await firstSwitch.setValue(false)
    await wrapper
      .findAll('button')
      .find(button => button.text().includes('app.actions.save'))!
      .trigger('click')
    await flushPromises()
    expect((wrapper.findAll('.policy-switch')[0].element as unknown as { checked: boolean }).checked).toBe(false)
    expect(wrapper.text()).toContain('subagentRouting.conflictHint')
    expect(api.getSubagentRouting).toHaveBeenCalledTimes(2)
  })

  it('慢速保存期间锁定所有草稿操作，失败后恢复编辑并保留输入', async () => {
    let rejectSave!: (_error: Error) => void
    vi.mocked(api.updateSubagentRouting).mockImplementation(() => new Promise((_, reject) => { rejectSave = reject }))
    const wrapper = mountPanel()
    await flushPromises()
    await wrapper.find('.policy-model').setValue('luna')
    await wrapper.findAll('button').find(button => button.text().includes('app.actions.save'))!.trigger('click')
    expect(wrapper.find('.policy-model').attributes('disabled')).toBeDefined()
    expect(wrapper.findAll('.policy-switch').every(input => input.attributes('disabled') !== undefined)).toBe(true)
    expect(wrapper.findAll('button').every(button => button.attributes('disabled') !== undefined)).toBe(true)
    rejectSave(new Error('offline'))
    await flushPromises()
    expect(wrapper.find('.policy-model').attributes('disabled')).toBeUndefined()
    expect((wrapper.find('.policy-model').element as unknown as { value: string }).value).toBe('luna')
    expect(wrapper.text()).toContain('offline')
  })

  it('暂停状态复用已存在的翻译，映射平局采用固定规则', async () => {
    const data = cloneView()
    data.policy.model = 'sol-sun'
    data.channels[0].status = 'suspended'
    data.channels[0].modelMapping = { sun: 'sun-target', sol: 'sol-target' }
    vi.mocked(api.getSubagentRouting).mockResolvedValue(data)
    const wrapper = mountPanel()
    await flushPromises()
    expect(wrapper.text()).toContain('status.paused')
    expect(wrapper.text()).not.toContain('status.suspended')
    expect(wrapper.findAll('.model-preview')[0].text()).toContain('sol-target')
  })

  it('协议切换后忽略前一协议的延迟保存响应', async () => {
    let resolveSave!: (_view: SubagentRoutingView) => void
    vi.mocked(api.updateSubagentRouting).mockImplementation(() => new Promise(resolve => { resolveSave = resolve }))
    const wrapper = mountPanel()
    await flushPromises()
    await wrapper.find('.policy-model').setValue('luna')
    await wrapper.findAll('button').find(button => button.text().includes('app.actions.save'))!.trigger('click')
    const nextView = cloneView()
    nextView.protocol = 'messages'
    nextView.policy.model = 'haiku'
    vi.mocked(api.getSubagentRouting).mockResolvedValue(nextView)
    await wrapper.setProps({ channelType: 'messages' })
    await flushPromises()
    resolveSave(cloneView())
    await flushPromises()
    expect((wrapper.find('.policy-model').element as unknown as { value: string }).value).toBe('haiku')
    expect(wrapper.emitted('success')).toBeUndefined()
  })

  it('后台更新渠道时保留模型、开关与顺序，取消后使用最新配置', async () => {
    const wrapper = mountPanel()
    await flushPromises()
    await wrapper.find('.policy-model').setValue('luna')
    await wrapper.findAll('.policy-switch')[0].setValue(false)
    const next = cloneView()
    next.revision = 'r2'
    next.policy.model = 'astra'
    next.policy.channels.reverse()
    next.channels.reverse()
    next.channels[1].channelName = 'A updated'
    vi.mocked(api.getSubagentRouting).mockResolvedValue(next)
    await wrapper.setProps({ availableChannels: [] })
    await flushPromises()
    expect(wrapper.text()).toContain('A updated')
    expect(wrapper.find('.policy-model').element).toHaveProperty('value', 'luna')
    expect(wrapper.findAll('.policy-switch')[0].element).toHaveProperty('checked', false)
    expect(wrapper.text().indexOf('A updated')).toBeLessThan(wrapper.text().indexOf('B'))
    await wrapper.findAll('button').find(button => button.text().includes('app.actions.cancel'))!.trigger('click')
    expect(wrapper.find('.policy-model').element).toHaveProperty('value', 'astra')
    await wrapper.findAll('.policy-switch')[0].setValue(false)
    vi.mocked(api.updateSubagentRouting).mockResolvedValue(next)
    await wrapper.findAll('button').find(button => button.text().includes('app.actions.save'))!.trigger('click')
    await flushPromises()
    expect(api.updateSubagentRouting).toHaveBeenCalledWith('responses', 'r2', expect.objectContaining({ model: 'astra' }))
  })

  it('干净页面自动采用后端变化，刷新途中开始编辑也不会被覆盖', async () => {
    const wrapper = mountPanel()
    await flushPromises()
    const next = cloneView()
    next.policy.model = 'astra'
    vi.mocked(api.getSubagentRouting).mockResolvedValue(next)
    await wrapper.setProps({ availableChannels: [] })
    await flushPromises()
    expect(wrapper.find('.policy-model').element).toHaveProperty('value', 'astra')
    let resolveLoad!: (_view: SubagentRoutingView) => void
    vi.mocked(api.getSubagentRouting).mockImplementation(() => new Promise(resolve => { resolveLoad = resolve }))
    await wrapper.setProps({ availableChannels: [] })
    expect(wrapper.find('.policy-model').attributes('disabled')).toBeUndefined()
    await wrapper.find('.policy-model').setValue('luna')
    resolveLoad(cloneView())
    await flushPromises()
    expect(wrapper.find('.policy-model').element).toHaveProperty('value', 'luna')
  })

  it('模型搜索不修改保存值，清空选择保存为空而非占位文案', async () => {
    const wrapper = mountPanel()
    await flushPromises()
    const select = wrapper.findComponent({ name: 'ModelSelect' })
    select.vm.$emit('update:search', 'uuu')
    await flushPromises()
    expect(wrapper.find('.policy-model').element).toHaveProperty('value', 'sol')
    select.vm.$emit('update:modelValue', null)
    await flushPromises()
    vi.mocked(api.updateSubagentRouting).mockResolvedValue({ ...cloneView(), policy: { ...view.policy, model: '' } })
    await wrapper.findAll('button').find(button => button.text().includes('app.actions.save'))!.trigger('click')
    await flushPromises()
    expect(api.updateSubagentRouting).toHaveBeenCalledWith('responses', 'r1', expect.objectContaining({ model: '' }))
  })
})
