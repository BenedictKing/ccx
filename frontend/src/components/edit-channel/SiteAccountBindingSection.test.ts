// @vitest-environment jsdom
/* eslint-disable vue/one-component-per-file, vue/require-prop-types */
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import SiteAccountBindingSection from './SiteAccountBindingSection.vue'
import api from '../../services/api'
import type { Channel } from '../../services/api-types'

vi.mock('../../i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('../../services/api', () => ({ default: { listSiteAccounts: vi.fn(), updateSiteChannelBinding: vi.fn() } }))

const box = defineComponent({ setup(_, { slots }) { return () => h('div', slots.default?.()) } })
const field = defineComponent({ props: ['label', 'modelValue'], emits: ['update:modelValue'], setup: () => () => h('div') })
const button = defineComponent({ emits: ['click'], setup(_, { slots, emit }) { return () => h('button', { onClick: () => emit('click') }, slots.default?.()) } })
const channel = { index: 2, channelUid: 'channel-1', name: 'relay', baseUrl: 'https://relay.example', serviceType: 'openai', apiKeys: ['sk-key'] } as Channel

function renderSection() {
  return mount(SiteAccountBindingSection, {
    props: { channel, channelType: 'chat' },
    global: { stubs: {
      VIcon: box, VAlert: box, VRow: box, VCol: box, VProgressLinear: box,
      VSelect: field, VBtn: button,
    } },
  })
}

async function setField(wrapper: ReturnType<typeof renderSection>, label: string, value: string) {
  const input = wrapper.findAllComponents(field).find(item => item.props('label') === label)
  expect(input, label).toBeDefined()
  input!.vm.$emit('update:modelValue', value)
  await flushPromises()
}

async function click(wrapper: ReturnType<typeof renderSection>, label: string) {
  const target = wrapper.findAll('button').find(item => item.text() === label)
  expect(target, label).toBeDefined()
  await target!.trigger('click')
  await flushPromises()
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.listSiteAccounts).mockResolvedValue({ accounts: [{
    uid: 'account-1', siteType: 'newapi', baseUrl: 'https://relay.example', createdAt: '', updatedAt: '',
    provisionedKeys: [{ name: 'ccx-default', protocol: 'chat', group: 'default' }],
  }] })
  vi.mocked(api.updateSiteChannelBinding).mockResolvedValue(undefined)
})

describe('渠道账号绑定', () => {
  it('选择账号和分组 Key 后绑定当前渠道', async () => {
    const wrapper = renderSection()
    await flushPromises()
    await setField(wrapper, 'channelEditor.accountBinding.account', 'account-1')
    await setField(wrapper, 'channelEditor.accountBinding.group', 'default')
    await click(wrapper, 'channelEditor.accountBinding.bind')
    expect(api.updateSiteChannelBinding).toHaveBeenCalledWith('chat', 2, { accountUid: 'account-1', group: 'default' })
    expect(wrapper.emitted('updated')).toHaveLength(1)
  })

  it('绑定失败不更新本地状态，已绑定渠道可解绑且保留渠道', async () => {
    vi.mocked(api.updateSiteChannelBinding).mockRejectedValueOnce(new Error('key mismatch'))
    const wrapper = renderSection()
    await flushPromises()
    await setField(wrapper, 'channelEditor.accountBinding.account', 'account-1')
    await setField(wrapper, 'channelEditor.accountBinding.group', 'default')
    await click(wrapper, 'channelEditor.accountBinding.bind')
    expect(wrapper.text()).toContain('key mismatch')
    expect(wrapper.emitted('updated')).toBeUndefined()

    await wrapper.setProps({ channel: { ...channel, siteAccountUid: 'account-1', siteGroup: 'default' } })
    vi.mocked(api.updateSiteChannelBinding).mockResolvedValueOnce(undefined)
    await click(wrapper, 'channelEditor.accountBinding.unbind')
    expect(api.updateSiteChannelBinding).toHaveBeenLastCalledWith('chat', 2, { accountUid: '', group: '' })
    expect(wrapper.emitted('updated')).toHaveLength(1)
  })
})
