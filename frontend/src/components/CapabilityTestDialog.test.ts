// @vitest-environment jsdom
/* eslint-disable vue/one-component-per-file, vue/require-prop-types */
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h, nextTick } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import CapabilityTestDialog from './CapabilityTestDialog.vue'
import { api, type CapabilityTestJob, type Channel, type ModelsResponse } from '../services/api'

vi.mock('../i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('../services/api', () => ({
  api: {
    getChannelModels: vi.fn(), getResponsesChannelModels: vi.fn(),
    getChatChannelModels: vi.fn(), getGeminiChannelModels: vi.fn(),
  },
  ApiError: class extends Error {},
}))

const box = defineComponent({ setup: (_, { slots }) => () => h('div', slots.default?.()) })
const field = defineComponent({
  props: ['modelValue', 'clearable', 'items', 'returnObject', 'loading', 'errorMessages', 'menuProps'],
  emits: ['update:modelValue'],
  setup: (_, { slots }) => () => h('div', [h('input'), slots.append?.()]),
})
const button = defineComponent({
  props: ['disabled'], emits: ['click'],
  setup: (props, { slots, emit }) => () => h('button', {
    disabled: props.disabled, onClick: () => emit('click'),
  }, slots.default?.()),
})

const channel: Channel = {
  index: 6, name: 'test', serviceType: 'responses', baseUrl: 'https://upstream.test',
  apiKeys: ['test-key'], proxyUrl: 'http://proxy.test', authHeader: 'bearer',
  customHeaders: { 'x-test': 'models' }, insecureSkipVerify: true,
}

function renderDialog(protocol: string, channel?: Channel) {
  const job: CapabilityTestJob = {
    jobId: '', channelId: 0, channelName: 'test', channelKind: 'responses', sourceType: 'openai',
    status: 'idle', lifecycle: 'pending', outcome: 'unknown', compatibleProtocols: [],
    totalDuration: 0, updatedAt: '',
    progress: {
      totalModels: 0, queuedModels: 0, runningModels: 0, successModels: 0,
      failedModels: 0, skippedModels: 0, completedModels: 0,
    },
    tests: [{
      protocol, status: 'idle', lifecycle: 'pending', outcome: 'unknown',
      success: false, latency: 0, streamingSupported: false, testedModel: '', testedAt: '',
    }],
  }
  return mount(CapabilityTestDialog, {
    props: { modelValue: true, channelName: 'test', channel, currentTab: 'responses', capabilityJob: job, capabilityRpm: 30 },
    global: { stubs: {
      VDialog: box, VCard: box, VCardTitle: box, VCardText: box, VDivider: box,
      VIcon: box, VChip: box, VTable: box, VTooltip: true, VBtn: button,
      VCombobox: field, CapabilityModelResults: true,
    } },
  })
}

describe('能力测试自选模型', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    for (const method of [api.getChannelModels, api.getResponsesChannelModels, api.getChatChannelModels, api.getGeminiChannelModels]) {
      vi.mocked(method).mockResolvedValue({ object: 'list', data: [{ id: 'deepseek-flash', object: 'model', created: 0, owned_by: 'test' }] })
    }
  })

  it.each([
    ['claude', 'getChannelModels'], ['responses', 'getResponsesChannelModels'],
    ['openai', 'getChatChannelModels'], ['gemini', 'getGeminiChannelModels'],
  ] as const)('复用 %s 上游的模型列表读取逻辑', async (serviceType, method) => {
    const wrapper = renderDialog('responses->chat', { ...channel, serviceType })
    try {
      await flushPromises()
      expect(api[method]).toHaveBeenCalledWith(6, {
        key: 'test-key', baseUrl: channel.baseUrl, proxyUrl: channel.proxyUrl,
        authHeader: 'bearer', customHeaders: channel.customHeaders, insecureSkipVerify: true,
      })
      const input = wrapper.findComponent(field)
      expect(input.props('items')).toEqual([{ title: 'deepseek-flash', value: 'deepseek-flash' }])
      expect(input.props('returnObject')).toBe(false)
      expect(input.props('menuProps')).toMatchObject({ attach: 'body', zIndex: 2500 })
      input.vm.$emit('update:modelValue', 'deepseek-flash')
      await nextTick()
      await wrapper.findAll('button').find(item => item.text() === 'capability.testSelectedModel')!.trigger('click')
      expect(wrapper.emitted('testProtocol')).toEqual([['responses->chat', ['deepseek-flash'], { force: true }]])
    } finally {
      wrapper.unmount()
    }
  })

  it.each(['responses', 'responses->chat'])('向 %s 协议提交输入的模型', async protocol => {
    const wrapper = renderDialog(protocol)
    try {
      wrapper.findComponent(field).vm.$emit('update:modelValue', ' custom-model ')
      await nextTick()
      const buttons = wrapper.findAll('button').filter(item => item.text() === 'capability.testSelectedModel')
      expect(buttons).toHaveLength(2)
      for (const target of buttons) await target.trigger('click')
      expect(wrapper.emitted('testProtocol')).toEqual([
        [protocol, ['custom-model'], { force: true }], [protocol, ['custom-model'], { force: true }],
      ])
    } finally {
      wrapper.unmount()
    }
  })

  it('读取模型失败后仍可手动输入，并可刷新重试', async () => {
    vi.mocked(api.getResponsesChannelModels).mockRejectedValueOnce(new Error('upstream unavailable'))
    const wrapper = renderDialog('responses', channel)
    try {
      await flushPromises()
      const input = wrapper.findComponent(field)
      expect(input.props('errorMessages')).toBe('addChannel.allApiKeysModelsFailed')
      input.vm.$emit('update:modelValue', 'private-model')
      await nextTick()
      await wrapper.findAll('button').find(item => item.text() === 'capability.testSelectedModel')!.trigger('click')
      expect(wrapper.emitted('testProtocol')).toEqual([['responses', ['private-model'], { force: true }]])
      await wrapper.find('button[aria-label="capability.refreshModels"]').trigger('click')
      await flushPromises()
      expect(api.getResponsesChannelModels).toHaveBeenCalledTimes(2)
      expect(input.props('errorMessages')).toBe('')
      expect(input.props('items')).toEqual([{ title: 'deepseek-flash', value: 'deepseek-flash' }])
    } finally {
      wrapper.unmount()
    }
  })

  it('切换渠道时忽略旧渠道延迟返回的模型列表', async () => {
    let finishOld!: (_value: ModelsResponse) => void
    vi.mocked(api.getResponsesChannelModels).mockReturnValueOnce(new Promise(resolve => { finishOld = resolve }))
    const wrapper = renderDialog('responses', channel)
    try {
      await wrapper.setProps({ channel: { ...channel, index: 7, name: 'other' } })
      await flushPromises()
      const input = wrapper.findComponent(field)
      expect(input.props('loading')).toBe(false)
      finishOld({ object: 'list', data: [{ id: 'old-channel-model', object: 'model', created: 0, owned_by: 'test' }] })
      await flushPromises()
      expect(input.props('items')).toEqual([{ title: 'deepseek-flash', value: 'deepseek-flash' }])
    } finally {
      wrapper.unmount()
    }
  })

  it('清空自选模型后可以使用默认模型发起测试', async () => {
    const wrapper = renderDialog('responses')
    try {
      const input = wrapper.findComponent(field)
      input.vm.$emit('update:modelValue', 'custom-model')
      await nextTick()
      input.vm.$emit('update:modelValue', null)
      await nextTick()
      const target = wrapper.findAll('button').find(item => item.text() === 'capability.startTest')!
      await target.trigger('click')
      expect(wrapper.emitted('testProtocol')).toEqual([['responses', undefined]])
    } finally {
      wrapper.unmount()
    }
  })
})
