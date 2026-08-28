// @vitest-environment jsdom
import { mount } from '@vue/test-utils'
import { defineComponent, nextTick } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import NewApiAccountPanel from './NewApiAccountPanel.vue'

const { apiMocks, ApiErrorStub } = vi.hoisted(() => {
  class ApiErrorStub extends Error {
    constructor(
      message: string,
      readonly status: number,
      readonly details?: unknown,
    ) {
      super(message)
      this.name = 'ApiError'
    }
  }
  return {
    ApiErrorStub,
    apiMocks: {
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
      deleteSubscriptionPrimaryAccount: vi.fn(),
    },
  }
})

vi.mock('../../services/api', () => ({ api: apiMocks, ApiError: ApiErrorStub }))

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
  // 透传原生事件，供父组件 .stop 修饰符访问 event
  template: '<button :disabled="disabled" @click="$emit(\'click\', $event)"><slot /></button>',
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

  it('主账号作为列表首行展示余额和脱敏 token，展开仅详情无凭证表单', async () => {
    const wrapper = mountPanel()
    await vi.waitFor(() => expect(apiMocks.getSubscription).toHaveBeenCalledWith('sub-main'))
    await nextTick()

    // 主账号默认在账号列表中：行内展示余额与脱敏 token
    expect(wrapper.text()).toContain('50,000')
    expect(wrapper.text()).toContain('****oken')
    expect(wrapper.text()).toContain('subscription.newApi.primaryBadge')

    // 展开主账号行：详情含已用额度；账号平权后不再有更新凭证表单
    await wrapper.find('[role="button"][aria-expanded="false"]').trigger('click')
    await nextTick()
    expect(wrapper.find('[aria-expanded="true"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('1,000')
    expect(wrapper.text()).not.toContain('subscription.newApi.updateCredentials')
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

  it('主账号可删除：删除后提示重新添加成为新主账号', async () => {
    apiMocks.deleteSubscriptionPrimaryAccount.mockResolvedValue(undefined)
    apiMocks.getSubscriptionAccounts.mockResolvedValue({ accounts: [] })
    const wrapper = mountPanel()
    await vi.waitFor(() => expect(apiMocks.getSubscription).toHaveBeenCalledWith('sub-main'))
    await nextTick()

    // 主账号行上的删除按钮触发删除，成功后重拉订阅与账号列表
    const deleteBtn = wrapper.findAll('button').find(button => button.text().includes('subscription.newApi.deletePrimaryAccount'))
    expect(deleteBtn).toBeTruthy()
    await deleteBtn!.trigger('click')

    await vi.waitFor(() => expect(apiMocks.deleteSubscriptionPrimaryAccount).toHaveBeenCalledWith('sub-main'))
    await vi.waitFor(() => expect(wrapper.emitted('updated')).toBeTruthy())
  })

  it('订阅无主凭证时显示重新添加提示，主账号行不渲染', async () => {
    apiMocks.getSubscription.mockResolvedValue({
      ...(await apiMocks.getSubscription()),
      accessTokenMasked: '',
    })
    const wrapper = mountPanel()
    await vi.waitFor(() => expect(apiMocks.getSubscription).toHaveBeenCalled())
    await nextTick()

    expect(wrapper.text()).toContain('subscription.newApi.primaryAccountRemoved')
    expect(wrapper.text()).not.toContain('subscription.newApi.primaryBadge')
  })

  it('new_api 渠道缺失 subscriptionUid 时按 channelUid 兜底拉取订阅', async () => {
    const wrapper = mountPanel({
      subscriptionUid: '',
      channelUid: 'ch-1',
      channelKind: 'messages',
      isGeneric: false,
      autoManagedKind: 'new_api',
    })
    await vi.waitFor(() => expect(apiMocks.getSubscription).toHaveBeenCalledWith('newapi-ch-1'))
    await nextTick()
    expect(wrapper.text()).toContain('50,000')
    expect(wrapper.text()).toContain('****oken')
  })

  it('订阅不存在时给出提示，添加账号不再静默无响应', async () => {
    apiMocks.getSubscription.mockRejectedValue(new ApiErrorStub('subscription not found', 404))
    const wrapper = mountPanel({
      subscriptionUid: '',
      channelUid: 'ch-1',
      channelKind: 'messages',
      isGeneric: false,
      autoManagedKind: 'new_api',
    })
    await vi.waitFor(() => expect(apiMocks.getSubscription).toHaveBeenCalledWith('newapi-ch-1'))
    await nextTick()
    expect(wrapper.text()).toContain('subscription.newApi.subscriptionNotFound')

    // 填写 token 点添加：因主账号订阅未就绪给出反馈，且不发起校验请求
    await wrapper.find('input[type="password"]').setValue('some-access-token')
    await wrapper.findAll('button').find(button => button.text().includes('app.actions.add'))!.trigger('click')
    await nextTick()
    expect(apiMocks.verifyNewApiSubscription).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('subscription.newApi.subscriptionNotFound')
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

    // 主账号行已默认展开定位之外，按 displayName 定位子账号行并展开详情
    const secondRow = wrapper.findAll('[role="button"]').find(row => row.text().includes('second'))
    expect(secondRow).toBeTruthy()
    await secondRow!.trigger('click')
    await nextTick()
    expect(secondRow!.attributes('aria-expanded')).toBe('true')
    // 展开仅展示详情（掩码 token/Key chips）；账号平权后不再有更新凭证表单
    expect(wrapper.text()).toContain('****cc81')
    expect(wrapper.text()).not.toContain('subscription.newApi.updateCredentials')

    // 子账号删除按钮走独立端点
    const deleteButtons = wrapper.findAll('button').filter(button => button.text().includes('app.actions.delete'))
    apiMocks.deleteSubscriptionAccount.mockResolvedValue(undefined)
    await deleteButtons[deleteButtons.length - 1]!.trigger('click')
    await vi.waitFor(() => expect(apiMocks.deleteSubscriptionAccount).toHaveBeenCalledWith('sub-main', 'acct_sub_1'))
  })
})
