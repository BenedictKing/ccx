// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError, type SubagentRoutingView } from '../services/api'
import SubagentRoutingPanel from './SubagentRoutingPanel.vue'

vi.mock('../i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('vue-router', () => ({ onBeforeRouteLeave: vi.fn(), onBeforeRouteUpdate: vi.fn() }))
vi.mock('../services/api', async importOriginal => {
  const actual = await importOriginal<typeof import('../services/api')>()
  return { ...actual, api: { getSubagentRouting: vi.fn(), updateSubagentRouting: vi.fn() } }
})
const { api } = await import('../services/api')
const cloneView = () => JSON.parse(JSON.stringify(view)) as SubagentRoutingView

const view: SubagentRoutingView = {
  protocol: 'responses', activated: true, revision: 'r1', legacyMixed: false,
  policy: { model: 'sol', channels: [{ channelUid: 'a', enabled: true }, { channelUid: 'b', enabled: true }] },
  channels: [
    { channelUid: 'a', channelIndex: 0, channelName: 'A', serviceType: 'responses', status: 'active', effectiveState: 'active', enabled: true, requestModel: 'sol', resolvedModel: 'actual-a', modelSupported: true },
    { channelUid: 'b', channelIndex: 1, channelName: 'B', serviceType: 'responses', status: 'active', effectiveState: 'active', enabled: true, requestModel: 'sol', resolvedModel: 'actual-b', modelSupported: true },
  ],
}

function mountPanel() {
  return mount(SubagentRoutingPanel, {
    props: { channelType: 'responses' },
    global: {
      stubs: {
        draggable: { props: ['modelValue'], template: '<div><template v-for="(element, index) in modelValue" :key="element.channelUid"><slot name="item" :element="element" :index="index" /></template></div>' },
        'v-switch': { props: ['modelValue'], emits: ['update:modelValue'], template: '<input type="checkbox" class="policy-switch" :checked="modelValue" @change="$emit(\'update:modelValue\', $event.target.checked)">' },
        'v-btn': { emits: ['click'], template: '<button @click="$emit(\'click\')"><slot /></button>' },
        'v-combobox': true, 'v-text-field': true, 'v-icon': true, 'v-chip': true, 'v-alert': { template: '<div><slot /></div>' },
        'v-card': { template: '<div><slot /></div>' }, 'v-spacer': true, 'v-skeleton-loader': true,
      },
    },
  })
}

describe('SubagentRoutingPanel', () => {
  beforeEach(() => {
    vi.mocked(api.getSubagentRouting).mockResolvedValue(cloneView())
    vi.mocked(api.updateSubagentRouting).mockReset()
  })

  it('按 UID 和开关保存草稿，并采用服务端返回的 revision', async () => {
    const wrapper = mountPanel()
    await flushPromises()
    await wrapper.findAll('.policy-switch')[0].setValue(false)
    const expectedPolicy = { model: 'sol', channels: [{ channelUid: 'a', enabled: false }, { channelUid: 'b', enabled: true }] }
    vi.mocked(api.updateSubagentRouting).mockResolvedValue({ ...cloneView(), revision: 'r2', policy: expectedPolicy })
    await wrapper.findAll('button').find(button => button.text().includes('app.actions.save'))!.trigger('click')
    await flushPromises()
    expect(api.updateSubagentRouting).toHaveBeenCalledWith('responses', 'r1', expectedPolicy)
    expect(wrapper.emitted('success')).toHaveLength(1)
  })

  it('保存冲突时保留草稿并提示重新加载', async () => {
    vi.mocked(api.updateSubagentRouting).mockRejectedValue(new ApiError('conflict', 409))
    const wrapper = mountPanel()
    await flushPromises()
    const firstSwitch = wrapper.findAll('.policy-switch')[0]
    await firstSwitch.setValue(false)
    await wrapper.findAll('button').find(button => button.text().includes('app.actions.save'))!.trigger('click')
    await flushPromises()
    expect((wrapper.findAll('.policy-switch')[0].element as unknown as { checked: boolean }).checked).toBe(false)
    expect(wrapper.text()).toContain('subagentRouting.conflictHint')
  })
})
