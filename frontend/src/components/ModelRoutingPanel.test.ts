// @vitest-environment jsdom
/* eslint-disable vue/one-component-per-file */
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, nextTick, ref } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ModelRoutingPanel from './ModelRoutingPanel.vue'
import { api } from '../services/api'
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
function render() {
  return mount(ModelRoutingPanel, {
    props: { apiType: 'chat', duration: '6h', refreshVersion: 0, resetVersion: 0 },
    global: {
      stubs: {
        VIcon: Slot,
        VAlert: Slot,
        VProgressLinear: true,
        VBtnToggle: Slot,
        VBtn: Button,
        VChip: Slot,
        VNavigationDrawer: true,
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
  it('loads only when expanded, rejects stale time-window responses and stops on collapse', async () => {
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
    expect(api.getModelRouting).not.toHaveBeenCalled()
    await wrapper.find('.routing-heading').trigger('click')
    await wrapper.setProps({ duration: '1h' })
    await flushPromises()
    expect(wrapper.find('.routing-content').text()).toContain('new-window')
    resolveOld(result('stale-window'))
    await flushPromises()
    expect(wrapper.find('.routing-content').text()).not.toContain('stale-window')
    await wrapper.find('.routing-heading').trigger('click')
    await wrapper.setProps({ refreshVersion: 1 })
    await flushPromises()
    expect(api.getModelRouting).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })
  it('shows unsupported ranges without querying and clears filters on manual refresh', async () => {
    vi.mocked(api.getModelRouting).mockResolvedValue(result('source'))
    const wrapper = render()
    await wrapper.setProps({ duration: '7d' })
    await wrapper.find('.routing-heading').trigger('click')
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
    expect(wrapper.find('.routing-filters').text()).toContain('routing.interactionHint')
    wrapper.unmount()
  })
})
