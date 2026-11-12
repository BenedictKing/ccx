// @vitest-environment jsdom
/* eslint-disable vue/one-component-per-file */
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, nextTick, ref } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ModelRoutingPanel from './ModelRoutingPanel.vue'
import { api } from '../services/api'
import { ApiError } from '../services/api-helpers'
import type { RoutingDistribution } from '../services/routing-types'

vi.mock('../i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('../services/api', () => ({ api: { getModelRouting: vi.fn(), getRoutingRequests: vi.fn() } }))
vi.mock('vuetify', () => ({ useDisplay: () => ({ smAndDown: ref(false), width: ref(1400) }) }))
vi.mock('vuetify/components/VNavigationDrawer', () => ({ VNavigationDrawer: { template: '<aside><slot /></aside>' } }))

const Slot = defineComponent({ template: '<div><slot /></div>' })
const Button = defineComponent({ emits: ['click'], template: '<button @click="$emit(\'click\')"><slot /></button>' })
const result = (model: string): RoutingDistribution => ({
  from: '2026-11-13T00:00:00Z',
  to: '2026-11-13T01:00:00Z',
  basis: 'successful_requests',
  coverage: {
    startedAt: '2026-11-12T00:00:00Z',
    persistent: true,
    droppedWrites: 0,
    unknownRequests: 0,
    limited: false
  },
  summary: {
    requests: 1,
    success: 1,
    failed: 0,
    cancelled: 0,
    inProgress: 0,
    unknown: 0,
    attempts: 1,
    retriedRequests: 0,
    noAttemptRequests: 0,
    matchedCount: 1
  },
  paths: [
    {
      requestedModel: model,
      channelUid: 'channel',
      channelName: 'Test channel',
      sentModel: 'actual',
      count: 1,
      share: 100,
      mappingOutcomes: { changed: 1 },
      statuses: { success: 1 }
    }
  ]
})
function render(duration: '6h' | '7d' = '6h') {
  return mount(ModelRoutingPanel, {
    props: { apiType: 'chat', duration, refreshVersion: 0, resetVersion: 0 },
    global: {
      stubs: {
        VIcon: Slot,
        VAlert: Slot,
        VProgressLinear: true,
        VProgressCircular: true,
        VTooltip: Slot,
        VBtnToggle: Slot,
        VBtn: Button,
        VChip: Slot,
        VNavigationDrawer: Slot,
        teleport: true,
        VExpansionPanels: Slot,
        VExpansionPanel: Slot,
        VExpansionPanelTitle: Slot,
        VExpansionPanelText: Slot
      }
    }
  })
}
beforeEach(() => {
  vi.resetAllMocks()
})
describe('model routing query lifecycle', () => {
  it('loads on mount without a collapse control and rejects stale time-window responses', async () => {
    let resolveOld!: (_data: RoutingDistribution) => void
    vi.mocked(api.getModelRouting)
      .mockImplementationOnce(
        () =>
          new Promise(resolve => {
            resolveOld = resolve
          })
      )
      .mockResolvedValueOnce(result('new-window'))
    const wrapper = render()
    expect(wrapper.find('.routing-heading').element.tagName).toBe('H3')
    expect(wrapper.find('button.routing-heading').exists()).toBe(false)
    expect(api.getModelRouting).toHaveBeenCalledTimes(1)
    await wrapper.setProps({ duration: '1h' })
    await flushPromises()
    expect(wrapper.find('.routing-content').text()).toContain('new-window')
    resolveOld(result('stale-window'))
    await flushPromises()
    expect(wrapper.find('.routing-content').text()).not.toContain('stale-window')
    expect(api.getModelRouting).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })
  it('shows unsupported ranges without querying and clears filters on manual refresh', async () => {
    vi.mocked(api.getModelRouting).mockResolvedValue(result('source'))
    const wrapper = render('7d')
    await flushPromises()
    expect(api.getModelRouting).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('routing.rangeUnsupported')
    await wrapper.setProps({ duration: '24h' })
    await flushPromises()
    await wrapper.find('.flow-node').trigger('click')
    await flushPromises()
    expect(api.getModelRouting).toHaveBeenLastCalledWith('chat', expect.objectContaining({ requestedModel: 'source' }))
    await wrapper.setProps({ resetVersion: 1, refreshVersion: 1 })
    await nextTick()
    await flushPromises()
    expect(wrapper.find('.routing-filters').exists()).toBe(false)
    wrapper.unmount()
  })
  it('keeps the existing graph and reserves loading space while polling', async () => {
    const initial = result('source')
    let resolveRefresh!: (_data: RoutingDistribution) => void
    vi.mocked(api.getModelRouting)
      .mockResolvedValueOnce(initial)
      .mockImplementationOnce(
        () =>
          new Promise(resolve => {
            resolveRefresh = resolve
          })
      )
    const wrapper = render()
    await flushPromises()
    const graph = wrapper.find('.model-routing-flow').element
    const source = wrapper.find('.flow-source').element
    const status = wrapper.find('.routing-loading').element
    await wrapper.setProps({ refreshVersion: 1 })
    expect(wrapper.find('.model-routing-flow').element).toBe(graph)
    expect(wrapper.find('.flow-source').element).toBe(source)
    expect(wrapper.find('.routing-loading').element).toBe(status)
    expect(wrapper.find('.routing-loading').attributes('aria-busy')).toBe('true')
    expect(wrapper.text()).not.toContain('routing.scope')
    expect(wrapper.text()).not.toContain('routing.countNote')
    expect(wrapper.text()).not.toContain('routing.window')
    resolveRefresh({
      ...initial,
      summary: { ...initial.summary, requests: 2 },
      paths: [{ ...initial.paths[0]!, count: 2 }]
    })
    await flushPromises()
    expect(wrapper.find('.flow-source').element).toBe(source)
    expect(wrapper.find('.routing-loading').attributes('aria-busy')).toBe('false')
    expect(wrapper.find('.flow-source').text()).toContain('2')
    wrapper.unmount()
  })
  it('filters a sent model within its channel without triggering the channel handler twice', async () => {
    vi.mocked(api.getModelRouting).mockResolvedValue(result('source'))
    const wrapper = render()
    await flushPromises()
    await wrapper.find('.flow-model').trigger('click')
    await flushPromises()
    expect(api.getModelRouting).toHaveBeenLastCalledWith(
      'chat',
      expect.objectContaining({ channelUid: 'channel', sentModel: 'actual' })
    )
    wrapper.unmount()
  })
  it.each(['.flow-source', '.flow-channel', '.flow-model'])(
    'keeps the graph mounted while selecting and clearing %s',
    async selector => {
      const initial = result('source')
      let resolveQuery!: (_data: RoutingDistribution) => void
      vi.mocked(api.getModelRouting)
        .mockResolvedValueOnce(initial)
        .mockImplementation(
          () =>
            new Promise(resolve => {
              resolveQuery = resolve
            })
        )
      const wrapper = render()
      await flushPromises()
      const graph = wrapper.find('.model-routing-flow').element
      const node = wrapper.find(selector).element
      await wrapper.find(selector).trigger('click')
      expect(wrapper.find('.model-routing-flow').element).toBe(graph)
      expect(wrapper.find(selector).element).toBe(node)
      expect(wrapper.find('.routing-loading').attributes('aria-busy')).toBe('true')
      resolveQuery(initial)
      await flushPromises()
      expect(wrapper.find('.model-routing-flow').element).toBe(graph)
      expect(wrapper.find(selector).element).toBe(node)
      await wrapper.find('.routing-filters button').trigger('click')
      expect(wrapper.find('.model-routing-flow').element).toBe(graph)
      resolveQuery(initial)
      await flushPromises()
      expect(wrapper.find(selector).element).toBe(node)
      expect(wrapper.find('.routing-filters').exists()).toBe(false)
      wrapper.unmount()
    }
  )
  it('refreshes an expired drawer window before querying requests again', async () => {
    vi.mocked(api.getModelRouting)
      .mockResolvedValueOnce(result('source'))
      .mockResolvedValueOnce({
        ...result('source'),
        from: '2026-11-13T00:05:00Z',
        to: '2026-11-13T01:05:00Z'
      })
    vi.mocked(api.getRoutingRequests)
      .mockRejectedValueOnce(new ApiError('expired', 410))
      .mockResolvedValueOnce({
        requests: [],
        nextCursor: '',
        from: '2026-11-13T00:05:00Z',
        to: '2026-11-13T01:05:00Z',
        coverage: result('source').coverage
      })
    const wrapper = render()
    await flushPromises()
    await wrapper
      .findAll('button')
      .find(button => button.text() === 'routing.viewRequests')!
      .trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('routing.snapshotExpired')
    await wrapper
      .findAll('button')
      .find(button => button.text() === 'routing.refreshRequests')!
      .trigger('click')
    await flushPromises()
    expect(api.getRoutingRequests).toHaveBeenLastCalledWith(
      'chat',
      expect.objectContaining({ from: '2026-11-13T00:05:00Z', to: '2026-11-13T01:05:00Z' })
    )
    expect(wrapper.text()).not.toContain('routing.snapshotExpired')
    wrapper.unmount()
  })
})
