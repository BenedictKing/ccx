// @vitest-environment jsdom
/* eslint-disable vue/one-component-per-file, vue/require-prop-types */
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import SiteAccountBindingSection from './SiteAccountBindingSection.vue'
import api from '../../services/api'
import type { Channel } from '../../services/api-types'

vi.mock('../../i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('../../services/api', () => ({ default: {
  listSiteAccounts: vi.fn(), upsertSiteAccount: vi.fn(), getSiteAccountGroups: vi.fn(), getSiteAccountKeys: vi.fn(),
  bindSiteKey: vi.fn(), unbindSiteKey: vi.fn(), copySiteChannelGroup: vi.fn(),
} }))

const box = defineComponent({ setup(_, { slots }) { return () => h('div', slots.default?.()) } })
const field = defineComponent({ props: ['label', 'modelValue'], emits: ['update:modelValue'], setup: () => () => h('div') })
const select = defineComponent({ props: ['label', 'modelValue'], emits: ['update:modelValue'], setup: () => () => h('div') })
const button = defineComponent({ emits: ['click'], setup(_, { slots, emit }) { return () => h('button', { onClick: () => emit('click') }, slots.default?.()) } })
const toggle = defineComponent({ props: ['modelValue'], emits: ['update:modelValue'], setup(_, { slots }) { return () => h('div', slots.default?.()) } })
const channel = { index: 2, name: 'relay', baseUrl: 'https://relay.example', serviceType: 'openai', apiKeys: ['sk-key'] } as Channel
const account = { uid: 'account-1', siteType: 'newapi', credentialMode: 'management' as const, baseUrl: 'https://relay.example', createdAt: '', updatedAt: '' }

function renderSection() {
  return mount(SiteAccountBindingSection, {
    props: { show: true, channel, channelType: 'chat', apiKey: 'sk-key' },
    global: { stubs: {
      VDialog: box, VCard: box, VCardTitle: box, VCardSubtitle: box, VCardText: box, VCardActions: box,
      VIcon: box, VAlert: box, VRow: box, VCol: box, VSpacer: box, VBtnToggle: toggle,
      VSelect: select, VTextField: field, VBtn: button,
    } },
  })
}

async function setField(wrapper: ReturnType<typeof renderSection>, label: string, value: unknown) {
  const input = [...wrapper.findAllComponents(field), ...wrapper.findAllComponents(select)].find(item => item.props('label') === label)
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
  vi.mocked(api.listSiteAccounts).mockResolvedValue({ accounts: [account] })
  vi.mocked(api.upsertSiteAccount).mockResolvedValue({ account })
  vi.mocked(api.getSiteAccountGroups).mockResolvedValue({ identified: true, groups: [{ name: 'default', ratio: 1 }, { name: 'vip', ratio: 0.5 }] })
  vi.mocked(api.getSiteAccountKeys).mockResolvedValue({ keys: [{ id: 7, name: 'remote-key', group: 'default', status: 1, model_limits_enabled: false, model_limits: '' }] })
  vi.mocked(api.bindSiteKey).mockResolvedValue(undefined)
  vi.mocked(api.unbindSiteKey).mockResolvedValue(undefined)
  vi.mocked(api.copySiteChannelGroup).mockResolvedValue({ channelIndex: 0, channelName: 'relay / vip', accountUid: 'account-1', group: 'vip', reused: false })
})

describe('Key 账号绑定与分组复制', () => {
  it('可直接输入站点 auth，读取远端记录后绑定当前 Key', async () => {
    const wrapper = renderSection()
    await flushPromises()
    await setField(wrapper, 'siteOnboarding.accessToken', 'management-token')
    await click(wrapper, 'channelEditor.accountBinding.loadRemote')
    await setField(wrapper, 'channelEditor.accountBinding.group', 'default')
    await setField(wrapper, 'channelEditor.accountBinding.remoteKey', 7)
    await click(wrapper, 'channelEditor.accountBinding.confirmBind')
    expect(api.upsertSiteAccount).toHaveBeenCalledWith(expect.objectContaining({ accessToken: 'management-token', credentialMode: 'management', baseUrl: channel.baseUrl }))
    expect(api.bindSiteKey).toHaveBeenCalledWith('chat', 2, { accountUid: 'account-1', apiKey: 'sk-key', group: 'default', remoteTokenId: 7 })
    expect(wrapper.emitted('updated')).toHaveLength(1)
  })

  it('选择其他分组时创建独立渠道副本，可自动创建该组专用 Key', async () => {
    const wrapper = renderSection()
    await flushPromises()
    await setField(wrapper, 'channelEditor.accountBinding.account', 'account-1')
    await click(wrapper, 'channelEditor.accountBinding.loadRemote')
    wrapper.findComponent(toggle).vm.$emit('update:modelValue', 'copy')
    await setField(wrapper, 'channelEditor.accountBinding.group', 'vip')
    await click(wrapper, 'channelEditor.accountBinding.createCopy')
    expect(api.copySiteChannelGroup).toHaveBeenCalledWith('chat', 2, { accountUid: 'account-1', apiKey: 'sk-key', group: 'vip', remoteTokenId: 0 })
    expect(api.bindSiteKey).not.toHaveBeenCalled()
  })

  it('多账号渠道中解绑只提交当前 Key', async () => {
    const boundChannel = { ...channel, apiKeyConfigs: [{ key: 'sk-key', sourceAccountUid: 'account-1', sourceRemoteTokenId: 7 }] }
    const wrapper = renderSection()
    await wrapper.setProps({ channel: boundChannel })
    await click(wrapper, 'channelEditor.accountBinding.unbindKey')
    expect(api.unbindSiteKey).toHaveBeenCalledWith('chat', 2, 'sk-key')
  })
})
