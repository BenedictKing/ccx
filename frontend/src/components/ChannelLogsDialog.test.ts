// @vitest-environment jsdom
/* eslint-disable vue/one-component-per-file */
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ChannelLogsDialog from './ChannelLogsDialog.vue'
import { api, type ChannelLogEntry } from '../services/api'

vi.mock('../i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
const tickCallbacks = vi.hoisted(() => [] as Array<() => void>)
vi.mock('../composables/useGlobalTick', () => ({
  useGlobalTick: () => ({ onTick: (callback: () => void) => { tickCallbacks.push(callback) } }),
}))
vi.mock('../services/api', () => ({ api: { getChannelLogs: vi.fn() } }))

const box = defineComponent({ setup: (_, { slots }) => () => h('div', slots.default?.()) })
const chip = defineComponent({ setup: (_, { slots }) => () => h('span', slots.default?.()) })
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
    tokensPerSecond: 42.5, isStream: true, costUSD: 0.031768, success: true, keyMask: 'sk-...masked', baseUrl: 'https://upstream.test',
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
      VSpacer: true, VChip: chip, VIcon: chip, VBtn: button,
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
    tickCallbacks.length = 0
    vi.mocked(api.getChannelLogs).mockResolvedValue({ channelIndex: 1, logs })
  })

  it('轮询插入新日志后展开状态仍跟随原日志', async () => {
    const wrapper = await openDialog()
    try {
      await wrapper.findAll('.log-item')[1]!.trigger('click')
      expect(wrapper.find('[data-log-key="req-failed"]').attributes('aria-expanded')).toBe('true')

      const newLog: ChannelLogEntry = { ...logs[0]!, requestId: 'req-new', model: 'claude-haiku-4-5' }
      vi.mocked(api.getChannelLogs).mockResolvedValue({ channelIndex: 1, logs: [newLog, ...logs] })
      tickCallbacks.forEach(callback => callback())
      await flushPromises()

      expect(wrapper.findAll('.log-item')).toHaveLength(3)
      expect(wrapper.find('[data-log-key="req-new"]').attributes('aria-expanded')).toBe('false')
      expect(wrapper.find('[data-log-key="req-success"]').attributes('aria-expanded')).toBe('false')
      expect(wrapper.find('[data-log-key="req-failed"]').attributes('aria-expanded')).toBe('true')
      expect(wrapper.find('#channel-log-details-2 .log-error-detail').text()).toBe('upstream overloaded')
    } finally {
      wrapper.unmount()
    }
  })

  it('只在有稳定渠道标识时提供请求历史入口', async () => {
    const wrapper = await openDialog()
    expect(wrapper.findAll('button').some(button => button.text() === 'routing.history')).toBe(false)
    await wrapper.setProps({ historyAvailable: true })
    await wrapper.findAll('button').find(button => button.text() === 'routing.history')!.trigger('click')
    expect(wrapper.emitted('requestHistory')).toHaveLength(1)
    wrapper.unmount()
  })

  it('默认显示请求摘要，点击后展开并收起完整诊断', async () => {
    const wrapper = await openDialog()
    try {
      const firstLog = wrapper.findAll('.log-item')[0]!
      expect(firstLog.attributes('aria-expanded')).toBe('false')
      expect(wrapper.text()).toContain('claude-sonnet-4-5')
      const summary = firstLog.find('.log-summary')
      expect(summary.text()).toContain('channelLogs.stream.yes')
      expect(summary.text()).toContain('42.5 t/s')
      expect(summary.text()).toContain(`${(1200).toLocaleString()} / 300`)
      expect(summary.text()).toContain('channelLogs.cache')
      expect(summary.text()).toContain(`↓ ${(500).toLocaleString()}`)
      expect(summary.text()).toContain('$0.0318')
      expect(summary.find('.log-latency--good').exists()).toBe(true)
      expect(wrapper.text()).not.toContain('https://upstream.test')
      expect(wrapper.text()).not.toContain('highest priority')

      await firstLog.trigger('click')
      expect(firstLog.attributes('aria-expanded')).toBe('true')
      expect(wrapper.find('#channel-log-details-0').text()).toContain('https://upstream.test')
      // 调度摘要仅随复制日志导出，界面不展示
      expect(wrapper.find('#channel-log-details-0').text()).not.toContain('highest priority')
      expect(wrapper.find('#channel-log-details-0').text()).toContain('channelLogs.duration.connect')
      expect(wrapper.find('#channel-log-details-0').text()).toContain('channelLogs.input')

      await firstLog.trigger('click')
      expect(firstLog.attributes('aria-expanded')).toBe('false')
      expect(wrapper.find('#channel-log-details-0').exists()).toBe(false)

      const failedLog = wrapper.findAll('.log-item')[1]!
      expect(failedLog.find('.log-latency--bad').exists()).toBe(true)
      // 旧日志未记录流式标识、无 usage 时不猜测流/非流与费用
      expect(failedLog.text()).not.toContain('channelLogs.stream')
      expect(failedLog.text()).not.toContain('$')
      expect(wrapper.find('.log-error-preview').exists()).toBe(true)
      await failedLog.trigger('click')
      expect(wrapper.find('.log-error-preview').exists()).toBe(false)
      expect(wrapper.find('#channel-log-details-1 .log-error-detail').text()).toBe('upstream overloaded')
      // 推理强度被重定向时在列表标签中显示，不再占用明细行
      expect(failedLog.find('.log-reasoning-chip').text()).toBe('medium → high')
      expect(wrapper.find('#channel-log-details-1').text()).not.toContain('medium → high')
    } finally {
      wrapper.unmount()
    }
  })

  it('支持键盘展开与非流式标识', async () => {
    vi.mocked(api.getChannelLogs).mockResolvedValue({ channelIndex: 1, logs: [{ ...logs[0]!, isStream: false, costUSD: 2.5 }] })
    const wrapper = await openDialog()
    try {
      const row = wrapper.find('.log-item')
      expect(row.text()).toContain('channelLogs.stream.no')
      expect(row.text()).toContain('$2.50')
      await row.trigger('keydown', { key: 'Enter' })
      expect(row.attributes('aria-expanded')).toBe('true')
      await row.trigger('keydown', { key: ' ' })
      expect(row.attributes('aria-expanded')).toBe('false')
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

  it('无 usage 的 400 错误在摘要、详情及复制日志中保留上下文估算', async () => {
    const failedLog: ChannelLogEntry = {
      ...logs[1]!, interfaceType: 'Responses', statusCode: 400, estimatedContextTokens: 120000,
      errorInfo: 'Your input exceeds the context window of this model.',
    }
    vi.mocked(api.getChannelLogs).mockResolvedValue({ channelIndex: 1, logs: [failedLog] })
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    const wrapper = await openDialog()
    try {
      expect(wrapper.find('.log-summary').text()).toContain('channelLogs.estimatedContextTokens')
      expect(wrapper.find('.log-summary').text()).toContain((120000).toLocaleString())
      await wrapper.find('.log-item').trigger('click')
      const details = wrapper.find('#channel-log-details-0')
      expect(details.text()).toContain('channelLogs.estimatedContextTokens')
      expect(details.text()).not.toContain('channelLogs.input')
      expect(details.text()).not.toContain('channelLogs.output')
      await wrapper.find('button[aria-label="channelLogs.copyEntry"]').trigger('click')
      const copied = JSON.parse(writeText.mock.calls[0]![0]!)
      expect(copied.estimatedContextTokens).toBe(120000)
      expect(copied.inputTokens).toBeUndefined()
    } finally {
      wrapper.unmount()
    }
  })

  it('有 usage 时显示上游计数，旧失败日志不会显示虚构的估算', async () => {
    vi.mocked(api.getChannelLogs).mockResolvedValue({ channelIndex: 1, logs: [{ ...logs[0]!, estimatedContextTokens: 120000 }, logs[1]!] })
    const wrapper = await openDialog()
    try {
      expect(wrapper.text()).not.toContain('channelLogs.estimatedContextTokens')
      await wrapper.find('.log-item').trigger('click')
      expect(wrapper.find('#channel-log-details-0').text()).toContain('1,800')
      expect(wrapper.find('#channel-log-details-0').text()).not.toContain('channelLogs.estimatedContextTokens')
    } finally {
      wrapper.unmount()
    }
  })
})
