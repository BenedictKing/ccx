// @vitest-environment jsdom
/* eslint-disable vue/one-component-per-file */
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, type PropType } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import KeyOperationsDialog from './KeyOperationsDialog.vue'
import { api, type Channel, type KeyOverviewResponse, type KeyOperationsPage } from '../services/api'

vi.mock('../i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('../services/api', () => ({ api: { getKeyOverview: vi.fn(), getKeyOperations: vi.fn() } }))
const Slot = defineComponent({ template: '<div><slot /></div>' })
const Button = defineComponent({
  props: { disabled: { type: Boolean, default: false } },
  template: '<button :disabled="disabled"><slot /></button>'
})
const Toggle = defineComponent({
  props: { modelValue: { type: String, default: '' } },
  emits: ['update:modelValue'],
  template: '<div><slot /></div>'
})
const Select = defineComponent({
  props: {
    modelValue: { type: String, default: '' },
    items: { type: Array as PropType<{ title: string; value: string }[]>, default: () => [] }
  },
  emits: ['update:modelValue'],
  template:
    '<select :value="modelValue" @change="$emit(\'update:modelValue\', $event.target.value)"><option v-for="item in items" :key="item.value" :value="item.value">{{item.title}}</option></select>'
})
const channel = { channelUid: 'channel', name: 'Channel', index: 0 } as Channel
const keyId = 'a'.repeat(64)
const response = (uid = 'channel'): KeyOverviewResponse => ({
  channelUid: uid,
  keys: [
    {
      keyId,
      keyMask: uid + '-masked',
      state: 'active',
      stats: { requestCount: 0, successCount: 0, failureCount: 0, successRate: 100 },
      latestFailure: {
        attemptId: 'attempt',
        gatewayRequestId: 'request',
        occurredAt: new Date().toISOString(),
        statusCode: 429
      }
    }
  ],
  failureCoverage: {
    persistent: true,
    retentionDays: 14,
    startedAt: new Date().toISOString(),
    droppedWrites: 0,
    unknownRequests: 0,
    limited: false
  }
})
const page: KeyOperationsPage = {
  events: [],
  nextCursor: 5,
  coverage: {
    persistent: true,
    retentionDays: 14,
    startedAt: new Date().toISOString(),
    droppedWrites: 0,
    limited: false
  }
}
const render = () =>
  mount(KeyOperationsDialog, {
    props: { modelValue: true, channel, channelType: 'chat' },
    global: {
      stubs: {
        VDialog: Slot,
        VCard: Slot,
        VCardTitle: Slot,
        VCardText: Slot,
        VSpacer: Slot,
        VBtn: Button,
        VBtnToggle: Toggle,
        VSelect: Select,
        VChip: Slot,
        VAlert: Slot,
        VProgressLinear: true
      }
    }
  })
beforeEach(() => {
  vi.resetAllMocks()
  vi.mocked(api.getKeyOverview).mockResolvedValue(response())
  vi.mocked(api.getKeyOperations).mockResolvedValue(page)
})
describe('Key operations navigation', () => {
  it('opens the exact failed attempt with channel, Key and all-retained filters', async () => {
    const wrapper = render()
    await flushPromises()
    const button = wrapper.findAll('button').find(button => button.text().includes('keyOps.latestFailure'))!
    await button.trigger('click')
    expect(wrapper.emitted('requestHistory')?.[0]?.[0]).toMatchObject({
      channelUid: 'channel',
      keyId,
      q: 'attempt',
      duration: 'retained'
    })
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual([false])
    wrapper.unmount()
  })
  it('resets operation pagination when the selected Key changes', async () => {
    const wrapper = render()
    await flushPromises()
    wrapper.findComponent(Toggle).vm.$emit('update:modelValue', 'operations')
    await flushPromises()
    await wrapper
      .findAll('button')
      .find(button => button.text() === 'routing.nextPage')!
      .trigger('click')
    await flushPromises()
    expect(api.getKeyOperations).toHaveBeenLastCalledWith('chat', 'channel', '', 5)
    await wrapper.get('select').setValue(keyId)
    await flushPromises()
    expect(api.getKeyOperations).toHaveBeenLastCalledWith('chat', 'channel', keyId, 0)
    expect(
      wrapper
        .findAll('button')
        .find(button => button.text() === 'routing.previousPage')!
        .attributes('disabled')
    ).toBeDefined()
    wrapper.unmount()
  })
  it('ignores a delayed overview after switching channels', async () => {
    let finish!: (_response: KeyOverviewResponse) => void
    vi.mocked(api.getKeyOverview)
      .mockReturnValueOnce(
        new Promise(resolve => {
          finish = resolve
        })
      )
      .mockResolvedValueOnce(response('new-channel'))
    const wrapper = render()
    await flushPromises()
    await wrapper.setProps({ channel: { ...channel, channelUid: 'new-channel' } })
    await flushPromises()
    finish(response())
    await flushPromises()
    expect(wrapper.text()).toContain('new-channel-masked')
    expect(wrapper.findAll('code').map(code => code.text())).toEqual(['new-channel-masked · aaaaaaaa'])
    wrapper.unmount()
  })
  it('clears the previous Key records when a new Key query fails', async () => {
    const nextKeyId = 'b'.repeat(64)
    const overview = response()
    overview.keys.push({ ...overview.keys[0]!, keyId: nextKeyId, keyMask: 'next-key' })
    vi.mocked(api.getKeyOverview).mockResolvedValueOnce(overview)
    vi.mocked(api.getKeyOperations).mockResolvedValueOnce({
      ...page,
      events: [
        {
          sequence: 10,
          occurredAt: new Date().toISOString(),
          apiType: 'chat',
          channelUid: 'channel',
          keyId,
          keyMask: 'previous-key',
          action: 'pause',
          source: 'manual',
          from: 'active',
          to: 'paused',
          outcome: 'success'
        }
      ]
    })
    const wrapper = render()
    await flushPromises()
    wrapper.findComponent(Toggle).vm.$emit('update:modelValue', 'operations')
    await flushPromises()
    expect(wrapper.text()).toContain('previous-key')
    vi.mocked(api.getKeyOperations).mockRejectedValueOnce(new Error('query failed'))
    await wrapper.get('select').setValue(nextKeyId)
    await flushPromises()
    expect(wrapper.text()).toContain('query failed')
    expect(wrapper.findAll('.key-observation')).toHaveLength(0)
    wrapper.unmount()
  })
})
