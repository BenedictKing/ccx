// @vitest-environment jsdom
import { mount } from '@vue/test-utils'
import { defineComponent, nextTick } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import NewApiAccountPanel from './NewApiAccountPanel.vue'

const apiMocks = vi.hoisted(() => ({
  getSubscription: vi.fn(),
  refreshSubscription: vi.fn(),
  updateNewApiCredentials: vi.fn(),
  updateSubscriptionAccountCredentials: vi.fn(),
  provisionNewApiSubscription: vi.fn(),
  verifyNewApiSubscription: vi.fn(),
  getSubscriptionAccounts: vi.fn(),
  addSubscriptionAccount: vi.fn(),
  refreshSubscriptionAccount: vi.fn(),
  deleteSubscriptionAccount: vi.fn(),
}))

vi.mock('../../services/api', () => ({ api: apiMocks }))
vi.mock('../../i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

const passthroughStub = defineComponent({ template: '<div><slot /></div>' })
const inputStub = defineComponent({
  props: ['modelValue', 'type', 'placeholder'],
  emits: ['update:modelValue'],
  template: '<input :type="type" :placeholder="placeholder" :value="modelValue" @input="$emit(\'update:modelValue\', $event.target.value)" />',
})
const buttonStub = defineComponent({
  props: ['disabled'],
  emits: ['click'],
  template: '<button :disabled="disabled" @click="$emit(\'click\')"><slot /></button>',
})

const mountPanel = (props: Record<string, unknown> = {}) => mount(NewApiAccountPanel, {
  props: { subscriptionUid: 'sub-main', ...props },
  global: {
    stubs: {
      VCard: passthroughStub,
      VCardTitle: passthroughStub,
      VCardText: passthroughStub,
      VIcon: passthroughStub,
      VChip: passthroughStub,
      VTooltip: passthroughStub,
      VProgressLinear: passthroughStub,
      VAlert: passthroughStub,
      VForm: passthroughStub,
      VRow: passthroughStub,
      VCol: passthroughStub,
      VSelect: passthroughStub,
      VExpansionPanels: passthroughStub,
      VExpansionPanel: passthroughStub,
      VExpansionPanelTitle: passthroughStub,
      VExpansionPanelText: passthroughStub,
      VExpandTransition: passthroughStub,
      VDivider: passthroughStub,
      VTextField: inputStub,
      VBtn: buttonStub,
    },
  },
})

describe('NewApiAccountPanel', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    apiMocks.getSubscription.mockResolvedValue({
      subscriptionUid: 'sub-main',
      displayName: 'Main account',
      provider: 'new_api',
      version: 2,
      balance: 50000,
      usedQuota: 1000,
      baseUrl: 'https://new-api.example.com',
      userId: '7',
      authTokenMode: 'bearer',
      accessTokenMasked: '****oken',
      createdAt: '2026-08-06T00:00:00Z',
      updatedAt: '2026-08-06T00:00:00Z',
    })
    apiMocks.getSubscriptionAccounts.mockResolvedValue({ accounts: [] })
  })

  it('展示主账号余额和脱敏 token，不回填明文 token', async () => {
    const wrapper = mountPanel()
    await vi.waitFor(() => expect(apiMocks.getSubscription).toHaveBeenCalledWith('sub-main'))
    await nextTick()

    expect(wrapper.text()).toContain('50,000')
    expect(wrapper.text()).toContain('1,000')
    expect(wrapper.text()).toContain('****oken')
    expect(wrapper.text()).not.toContain('secret-token')
    const passwordInput = wrapper.find<HTMLInputElement>('input[type="password"]')
    expect(passwordInput.element.value).toBe('')
  })

  it('generic 渠道填写 token 后绑定已有渠道', async () => {
    apiMocks.verifyNewApiSubscription.mockResolvedValue({
      userId: 42,
      groups: { default: 1 },
      availableModels: ['gpt-4o'],
    })
    apiMocks.provisionNewApiSubscription = vi.fn().mockResolvedValue({
      subscription: await apiMocks.getSubscription(),
      channelUid: 'ch-existing',
      channelIndex: 0,
      provisionedKey: '',
      provisionedTokenId: 1,
      reused: true,
      discoveryStarted: true,
    })
    const wrapper = mountPanel({
      subscriptionUid: '',
      channelName: 'Legacy relay',
      baseUrl: 'https://relay.example.com',
      channelUid: 'ch-existing',
      channelKind: 'messages',
      isGeneric: true,
    })
    await wrapper.find('input[type="password"]').setValue('new-api-access-token')
    await wrapper.findAll('button').find(button => button.text().includes('subscription.newApi.bindAccount'))!.trigger('click')

    await vi.waitFor(() => expect(apiMocks.provisionNewApiSubscription).toHaveBeenCalledWith({
      subscriptionUid: 'newapi-ch-existing',
      displayName: 'Legacy relay',
      baseUrl: 'https://relay.example.com',
      accessToken: 'new-api-access-token',
      userId: '42',
      authTokenMode: 'bearer',
      channelKind: 'messages',
      channelName: 'Legacy relay',
      provisionAllEligibleGroups: true,
      maxGroupMultiplier: 1,
      provisionModels: ['gpt-4o'],
    }))
    expect(wrapper.emitted('updated')).toBeTruthy()
  })

  it('输入新 token 后保存，并携带乐观锁版本', async () => {
    apiMocks.updateNewApiCredentials.mockResolvedValue({
      ...(await apiMocks.getSubscription()),
      version: 3,
      accessTokenMasked: '****new1',
    })
    const wrapper = mountPanel()
    await vi.waitFor(() => expect(apiMocks.getSubscription).toHaveBeenCalled())
    await wrapper.find('input[type="password"]').setValue('token-new1')
    await wrapper.findAll('button').find(button => button.text().includes('subscription.newApi.saveCredentials'))!.trigger('click')

    await vi.waitFor(() => expect(apiMocks.updateNewApiCredentials).toHaveBeenCalledWith('sub-main', {
      accessToken: 'token-new1',
      userId: '7',
      authTokenMode: 'bearer',
      expectedVersion: 2,
    }))
  })

  it('点击账号行展开详情，更新子账号凭证走独立端点', async () => {
    const account = {
      accountUid: 'acct_sub_1',
      userId: '8',
      authTokenMode: 'raw',
      displayName: 'second',
      balance: 12000,
      status: 'active',
      accessTokenMasked: '****cc81',
      provisionedKeys: [{ name: 'ccx-default', group: 'default', groupMultiplier: 1, tokenId: 12 }],
      createdAt: '2026-08-20T00:00:00Z',
      lastCheckedAt: '2026-08-26T00:00:00Z',
    }
    apiMocks.getSubscriptionAccounts.mockResolvedValue({ accounts: [account] })
    apiMocks.updateSubscriptionAccountCredentials.mockResolvedValue({
      ...account,
      userId: '9',
      authTokenMode: 'bearer',
      accessTokenMasked: '****new8',
    })
    const wrapper = mountPanel()
    await vi.waitFor(() => expect(apiMocks.getSubscriptionAccounts).toHaveBeenCalledWith('sub-main'))
    await nextTick()

    // 点击行头展开详情
    await wrapper.find('[role="button"][aria-expanded="false"]').trigger('click')
    await nextTick()
    expect(wrapper.find('[aria-expanded="true"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('****cc81')
    expect(wrapper.text()).toContain('subscription.newApi.updateCredentials')

    // 表单基线来自账号当前值；填新 token 后保存
    const inputs = wrapper.findAll<HTMLInputElement>('input[type="password"]')
    const accountTokenInput = inputs[inputs.length - 1]
    await accountTokenInput.setValue('token-acct-new')
    // 面板内有主账号/子账号两个同文案保存按钮，取展开区（最后一个）
    const saveButtons = wrapper.findAll('button').filter(button => button.text().includes('subscription.newApi.saveCredentials'))
    await saveButtons[saveButtons.length - 1]!.trigger('click')

    await vi.waitFor(() => expect(apiMocks.updateSubscriptionAccountCredentials).toHaveBeenCalledWith('sub-main', 'acct_sub_1', {
      accessToken: 'token-acct-new',
      userId: '8',
      authTokenMode: 'raw',
    }))
    // 保存成功后明文不回填
    await vi.waitFor(() => expect(accountTokenInput.element.value).toBe(''))
    expect(wrapper.text()).toContain('****new8')
    expect(wrapper.emitted('updated')).toBeTruthy()
  })
})
