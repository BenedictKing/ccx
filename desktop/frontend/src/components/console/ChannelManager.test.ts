import { createApp, defineComponent, h, nextTick, ref, type Ref } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import ChannelManager from './ChannelManager.vue'
import type { ManagedChannelType } from '@/utils/channel-type-api'

const status = ref({ running: true })
const isConsoleChannelsActive = ref(true)
const activeTab = ref('messages')
const channelsByType = ref({
  messages: {
    channels: [
      {
        index: 1,
        name: 'messages-channel',
        serviceType: 'openai',
        baseUrl: 'https://example.test',
        apiKeys: [],
        status: 'active',
      },
    ],
    current: 0,
  },
  chat: { channels: [], current: 0 },
  images: { channels: [], current: 0 },
  vectors: { channels: [], current: 0 },
  responses: { channels: [], current: 0 },
  gemini: { channels: [], current: 0 },
})
const dashboardCache = ref({
  messages: { metrics: [], stats: { multiChannelMode: false }, recentActivity: [] },
  chat: { metrics: [], stats: undefined, recentActivity: [] },
  images: { metrics: [], stats: undefined, recentActivity: [] },
  vectors: { metrics: [], stats: undefined, recentActivity: [] },
  responses: { metrics: [], stats: undefined, recentActivity: [] },
  gemini: { metrics: [], stats: undefined, recentActivity: [] },
})
const apiGet = vi.fn()
const chartUpdateData = vi.fn()
const chartSetLoading = vi.fn()

vi.mock('@/composables/useStatus', () => ({
  useStatus: () => ({ status }),
}))

vi.mock('@/composables/useDesktopActivity', () => ({
  useDesktopActivity: () => ({ isConsoleChannelsActive }),
}))

vi.mock('@/composables/useLanguage', () => ({
  useLanguage: () => ({ t: (key: string) => key }),
}))

vi.mock('@/composables/useAdminApi', () => ({
  useAdminApi: () => ({
    get: apiGet,
    put: vi.fn().mockResolvedValue(undefined),
  }),
}))

vi.mock('@/composables/useConsoleChannels', () => ({
  useConsoleChannels: () => ({
    activeTab,
    channelsByType,
    dashboardCache,
    refreshChannels: vi.fn().mockResolvedValue(undefined),
    deleteChannel: vi.fn().mockResolvedValue(undefined),
    setChannelStatus: vi.fn().mockResolvedValue(undefined),
    resumeChannel: vi.fn().mockResolvedValue(undefined),
    promoteChannel: vi.fn().mockResolvedValue(undefined),
    reorderChannels: vi.fn().mockResolvedValue(undefined),
  }),
}))

vi.mock('@/components/ui/button', () => ({
  Button: defineComponent({
    props: {
      disabled: { type: Boolean, default: false },
    },
    setup(props, { attrs, slots }) {
      return () => h('button', { ...attrs, disabled: props.disabled }, slots.default?.())
    },
  }),
}))

vi.mock('@/components/ui/input', () => ({
  Input: defineComponent({
    props: {
      modelValue: { type: String, default: '' },
      placeholder: { type: String, default: '' },
    },
    emits: ['update:modelValue'],
    setup(props, { emit, attrs }) {
      return () =>
        h('input', {
          ...attrs,
          value: props.modelValue,
          placeholder: props.placeholder,
          onInput: (event: Event) => emit('update:modelValue', (event.target as HTMLInputElement).value),
        })
    },
  }),
}))

vi.mock('@/components/ui/alert', () => ({
  Alert: defineComponent({
    setup(_props, { slots }) {
      return () => h('div', { role: 'alert' }, slots.default?.())
    },
  }),
}))

vi.mock('@/components/ui/skeleton', () => ({
  Skeleton: defineComponent({
    setup() {
      return () => h('div', { 'data-testid': 'skeleton' })
    },
  }),
}))

vi.mock('@/components/console/ChannelCard.vue', () => ({
  default: defineComponent({
    props: {
      channel: { type: Object, required: true },
      expanded: { type: Boolean, default: false },
    },
    emits: ['toggle'],
    setup(props, { emit }) {
      return () => {
        const channel = props.channel as { index: number; name: string }
        return h('article', { 'data-testid': 'channel-card' }, [
          h('span', channel.name),
          h('button', { type: 'button', onClick: () => emit('toggle') }, props.expanded ? 'collapse' : 'expand'),
        ])
      }
    },
  }),
}))

vi.mock('@/components/console/ChannelEditDialog.vue', () => ({
  default: defineComponent({ setup: () => () => h('div') }),
}))

vi.mock('@/components/console/ChannelLogsDialog.vue', () => ({
  default: defineComponent({ setup: () => () => h('div') }),
}))

vi.mock('@/components/console/CapabilityTestDialog.vue', () => ({
  default: defineComponent({ setup: () => () => h('div') }),
}))

vi.mock('@/components/console/CircuitBreakerDialog.vue', () => ({
  default: defineComponent({ setup: () => () => h('div') }),
}))

vi.mock('@/components/console/charts/KeyTrendChart.vue', () => ({
  default: defineComponent({ setup: () => () => h('div', { 'data-testid': 'key-trend-chart' }) }),
}))

vi.mock('@/components/console/charts/GlobalStatsChart.vue', () => ({
  default: defineComponent({
    props: {
      apiType: { type: String, required: true },
      chartInterval: { type: String, default: undefined },
      compact: { type: Boolean, default: false },
    },
    emits: ['refresh'],
    setup(_props, { emit, expose }) {
      expose({
        updateData: chartUpdateData,
        setLoading: chartSetLoading,
      })
      return () =>
        h('button', { type: 'button', 'data-testid': 'global-stats-chart', onClick: () => emit('refresh', '6h') }, 'chart')
    },
  }),
}))

describe('ChannelManager', () => {
  let root: HTMLDivElement
  let app: ReturnType<typeof createApp> | undefined
  let currentType: Ref<ManagedChannelType>

  beforeEach(() => {
    status.value = { running: true }
    isConsoleChannelsActive.value = true
    activeTab.value = 'messages'
    apiGet.mockReset()
    chartUpdateData.mockReset()
    chartSetLoading.mockReset()
    root = document.createElement('div')
    document.body.append(root)
  })

  afterEach(() => {
    app?.unmount()
    app = undefined
    document.body.innerHTML = ''
    vi.clearAllMocks()
  })

  it('does not load global stats while the chart is collapsed', async () => {
    apiGet.mockResolvedValue(createStatsResponse())
    mountChannelManager()
    await nextTick()
    await Promise.resolve()

    expect(apiGet).not.toHaveBeenCalledWith(expect.stringContaining('/global/stats/history'))
    expect(chartUpdateData).not.toHaveBeenCalled()
  })

  it('ignores stale global stats responses after switching protocol', async () => {
    const firstRequest = deferred<ReturnType<typeof createStatsResponse>>()
    let statsRequests = 0
    apiGet.mockImplementation((path: string) => {
      if (!path.includes('/global/stats/history')) {
        return Promise.resolve({})
      }
      statsRequests += 1
      return statsRequests === 1 ? firstRequest.promise : Promise.resolve(createStatsResponse())
    })

    mountChannelManager()
    await nextTick()

    clickButton('chart.globalStats')
    await nextTick()
    expect(apiGet).toHaveBeenCalledWith('/api/messages/global/stats/history?duration=6h')

    await updateProps({ type: 'chat' })
    expect(apiGet).toHaveBeenCalledWith('/api/chat/global/stats/history?duration=6h')

    firstRequest.resolve(createStatsResponse([{ timestamp: '2026-06-25T00:00:00.000Z', requestCount: 7 }]))
    await nextTick()
    await Promise.resolve()

    expect(chartUpdateData).toHaveBeenCalledTimes(1)
    expect(chartUpdateData).toHaveBeenCalledWith([], expect.any(Object), undefined)
  })

  function mountChannelManager(type: ManagedChannelType = 'messages') {
    currentType = ref(type)
    app = createApp(defineComponent({
      setup() {
        return () => h(ChannelManager, { type: currentType.value })
      },
    }))
    app.mount(root)
  }

  async function updateProps(props: { type: ManagedChannelType }) {
    currentType.value = props.type
    await nextTick()
    await Promise.resolve()
  }

  function clickButton(text: string) {
    const button = [...root.querySelectorAll('button')]
      .find(item => item.textContent?.trim() === text)
    expect(button).toBeTruthy()
    button!.click()
  }
})

function createStatsResponse(dataPoints: Array<Record<string, unknown>> = []) {
  return {
    dataPoints,
    modelDataPoints: undefined,
    summary: {
      totalRequests: 0,
      avgSuccessRate: 100,
      totalInputTokens: 0,
      totalOutputTokens: 0,
      totalCacheReadTokens: 0,
      totalCacheCreationTokens: 0,
      intervalSeconds: 300,
    },
  }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(res => {
    resolve = res
  })
  return { promise, resolve }
}
