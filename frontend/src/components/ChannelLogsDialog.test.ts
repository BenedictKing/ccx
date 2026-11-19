// @vitest-environment jsdom
/* eslint-disable vue/one-component-per-file */
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ChannelLogsDialog from './ChannelLogsDialog.vue'
import { api, type ChannelLogEntry } from '../services/api'

vi.mock('../i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('../composables/useGlobalTick', () => ({ useGlobalTick: () => ({ onTick: vi.fn() }) }))
vi.mock('../services/api', () => ({ api: { getChannelLogs: vi.fn() } }))

const box = defineComponent({ setup: (_, { slots }) => () => h('div', slots.default?.()) })
const chip = defineComponent({ setup: (_, { slots }) => () => h('span', slots.default?.()) })
const listItem = defineComponent({
  emits: ['click'],
  setup: (_, { slots, emit }) => () => h('div', { onClick: () => emit('click') }, [
    slots.prepend?.(), slots.default?.(), slots.append?.(),
  ]),
})
const button = defineComponent({
  emits: ['click'],
  setup: (_, { slots, emit }) => () => h('button', { onClick: (event: MouseEvent) => {
    event.stopPropagation()
    emit('click', event)
  } }, slots.default?.()),
})
const tooltip = defineComponent({
  inheritAttrs: false,
  setup: (_, { slots }) => () => slots.activator?.({ props: {} }),
})

const logs: ChannelLogEntry[] = [
  {
    requestId: 'req-success', timestamp: '2026-11-19T10:00:02Z', startTime: '2026-11-19T10:00:00Z',
    connectedAt: '2026-11-19T10:00:00.150Z', firstByteAt: '2026-11-19T10:00:01Z', completedAt: '2026-11-19T10:00:02Z',
    model: 'claude-sonnet-4-5', interfaceType: 'messages', statusCode: 200, durationMs: 2000,
    inputTokens: 1200, outputTokens: 300, contextTokens: 1800, cacheReadTokens: 500, cacheCreationTokens: 100,
    tokensPerSecond: 42.5, success: true, keyMask: 'sk-...masked', baseUrl: 'https://upstream.test',
    errorInfo: '', isRetry: false, status: 'completed', selectionReason: 'priority', selectionTraceSummary: 'highest priority',
  },
  {
    requestId: 'req-failed', timestamp: '2026-11-19T10:01:00Z', startTime: '2026-11-19T10:01:00Z',
    model: 'claude-opus-4-1', interfaceType: 'messages', statusCode: 529, durationMs: 800,
    success: false, keyMask: 'sk-...masked', baseUrl: 'https://upstream.test', errorInfo: 'upstream overloaded',
    isRetry: true, status: 'failed', originalReasoningEffort: 'medium', actualReasoningEffort: 'high',
  },
]

function renderDialog() {
  return mount(ChannelLogsDialog, {
    props: { modelValue: false, channelIndex: 1, channelName: 'test', channelType: 'messages' },
    global: { stubs: {
      VDialog: box, VCard: box, VCardTitle: box, VCardText: box, VDivider: box,
      VList: box, VListItem: listItem, VChip: chip, VIcon: chip, VBtn: button,
      VTooltip: tooltip, VExpandTransition: box, VProgressCircular: true,
    } },
  })
}

async function openDialog() {
  const wrapper = renderDialog()
  await wrapper.setProps({ modelValue: true })
  await flushPromises()
  return wrapper
}

describe('渠道日志列表', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    vi.mocked(api.getChannelLogs).mockResolvedValue({ channelIndex: 1, logs })
  })

  it('默认显示请求摘要，点击后展开并收起完整诊断', async () => {
    const wrapper = await openDialog()
    try {
      const firstLog = wrapper.findAll('.log-item')[0]!
      expect(firstLog.attributes('aria-expanded')).toBe('false')
      expect(wrapper.text()).toContain('claude-sonnet-4-5')
      expect(wrapper.text()).toContain('channelLogs.input')
      expect(wrapper.text()).not.toContain('https://upstream.test')
      expect(wrapper.text()).not.toContain('highest priority')

      await firstLog.trigger('click')
      expect(firstLog.attributes('aria-expanded')).toBe('true')
      expect(wrapper.find('#channel-log-details-0').text()).toContain('https://upstream.test')
      expect(wrapper.find('#channel-log-details-0').text()).toContain('highest priority')
      expect(wrapper.find('#channel-log-details-0').text()).toContain('channelLogs.duration.connect')

      await firstLog.trigger('click')
      expect(firstLog.attributes('aria-expanded')).toBe('false')
      expect(wrapper.find('#channel-log-details-0').exists()).toBe(false)

      const failedLog = wrapper.findAll('.log-item')[1]!
      expect(wrapper.find('.log-error-preview').exists()).toBe(true)
      await failedLog.trigger('click')
      expect(wrapper.find('.log-error-preview').exists()).toBe(false)
      expect(wrapper.find('#channel-log-details-1 .log-error-detail').text()).toBe('upstream overloaded')
    } finally {
      wrapper.unmount()
    }
  })

  it('复制完整日志且失败条目仍可发起模型复测', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    const wrapper = await openDialog()
    try {
      await wrapper.find('button[aria-label="channelLogs.copyEntry"]').trigger('click')
      expect(writeText).toHaveBeenCalledOnce()
      expect(JSON.parse(writeText.mock.calls[0]![0]!)).toMatchObject({ requestId: 'req-success', selectionTraceSummary: 'highest priority' })
      expect(wrapper.emitted('retestModel')).toBeUndefined()

      await wrapper.find('button[aria-label="channelLogs.retestModel"]').trigger('click')
      expect(wrapper.emitted('retestModel')).toEqual([[{
        channelId: 1, protocol: 'messages', model: 'claude-opus-4-1', reasoningEffort: 'high',
      }]])
    } finally {
      wrapper.unmount()
    }
  })
})
