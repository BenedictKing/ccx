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

  it('订阅凭证行作为列表首行展示余额和脱敏 token，展开仅详情无凭证表单', async () => {
    const wrapper = mountPanel()
    await vi.waitFor(() => expect(apiMocks.getSubscription).toHaveBeenCalledWith('sub-main'))
    await nextTick()

    // 订阅凭证行默认在账号列表中：行内展示余额与脱敏 token，无主账号徽章
    expect(wrapper.text()).toContain('50,000')
    expect(wrapper.text()).toContain('****oken')
    expect(wrapper.text()).not.toContain('subscription.newApi.primaryBadge')

    // 展开订阅凭证行：详情含已用额度；账号平权后不再有更新凭证表单
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
    await wrapper.find('input:not([type="password"])').setValue('42')
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

  it('订阅凭证行可删除：删除后重拉订阅与账号列表', async () => {
    apiMocks.deleteSubscriptionPrimaryAccount.mockResolvedValue(undefined)
    apiMocks.getSubscriptionAccounts.mockResolvedValue({ accounts: [] })
    const wrapper = mountPanel()
    await vi.waitFor(() => expect(apiMocks.getSubscription).toHaveBeenCalledWith('sub-main'))
    await nextTick()

    // 订阅凭证行上的删除按钮触发删除，成功后重拉订阅与账号列表
    const deleteBtn = wrapper.findAll('button').find(button => button.text().includes('app.actions.delete'))
    expect(deleteBtn).toBeTruthy()
    await deleteBtn!.trigger('click')

    await vi.waitFor(() => expect(apiMocks.deleteSubscriptionPrimaryAccount).toHaveBeenCalledWith('sub-main'))
    await vi.waitFor(() => expect(wrapper.emitted('updated')).toBeTruthy())
  })

  it('订阅无凭证且账号列表为空时显示平权重加提示，凭证行不渲染', async () => {
    apiMocks.getSubscription.mockResolvedValue({
      ...(await apiMocks.getSubscription()),
      accessTokenMasked: '',
    })
    apiMocks.getSubscriptionAccounts.mockResolvedValue({ accounts: [] })
    const wrapper = mountPanel()
    await vi.waitFor(() => expect(apiMocks.getSubscription).toHaveBeenCalled())
    await nextTick()

    expect(wrapper.text()).toContain('subscription.newApi.primaryAccountRemoved')
    expect(wrapper.text()).not.toContain('subscription.newApi.primaryBadge')
  })

  it('订阅无凭证但已添加账号时不显示未设置提示（平权正常态）', async () => {
    apiMocks.getSubscription.mockResolvedValue({
      ...(await apiMocks.getSubscription()),
      accessTokenMasked: '',
    })
    apiMocks.getSubscriptionAccounts.mockResolvedValue({
      accounts: [{
        accountUid: 'acc-1',
        displayName: 'BenedictKing',
        status: 'active',
        balance: 250000001,
        accessTokenMasked: '****kQ==',
        provisionedKeys: [{ tokenId: 1, name: 'ccx-default', group: 'default', groupMultiplier: 1 }],
      }],
    })
    const wrapper = mountPanel()
    await vi.waitFor(() => expect(apiMocks.getSubscriptionAccounts).toHaveBeenCalled())
    await nextTick()

    expect(wrapper.text()).not.toContain('subscription.newApi.primaryAccountRemoved')
    expect(wrapper.text()).toContain('BenedictKing')
  })

  it('已有主账号凭证且暂无子账号时不显示空账号提示', async () => {
    apiMocks.getSubscriptionAccounts.mockResolvedValue({ accounts: [] })
    const wrapper = mountPanel()
    await vi.waitFor(() => expect(apiMocks.getSubscription).toHaveBeenCalled())
    await nextTick()

    // 主账号凭证行在列（有脱敏 token），空账号提示不再出现，避免误读为“没有账号”
    expect(wrapper.text()).toContain('****oken')
    expect(wrapper.text()).not.toContain('subscription.newApi.noAccounts')
  })

  it('添加账号成功后重拉主账号与账号列表（自动接入 Key 统计同步刷新）', async () => {
    apiMocks.verifyNewApiSubscription.mockResolvedValue({
      userId: 42,
      username: 'linuxdo_3388',
      groups: { default: 1 },
      availableModels: ['gpt-4o'],
    })
    apiMocks.addSubscriptionAccount.mockResolvedValue(undefined)
    const wrapper = mountPanel()
    await vi.waitFor(() => expect(apiMocks.getSubscription).toHaveBeenCalledWith('sub-main'))
    await nextTick()
    const fetchPrimaryCalls = apiMocks.getSubscription.mock.calls.length

    await wrapper.find('input[type="password"]').setValue('second-account-token')
    await wrapper.find('input:not([type="password"])').setValue('42')
    await wrapper.findAll('button').find(button => button.text().includes('app.actions.add'))!.trigger('click')

    await vi.waitFor(() => expect(apiMocks.addSubscriptionAccount).toHaveBeenCalledWith('sub-main', expect.objectContaining({
      accessToken: 'second-account-token',
      userId: '42',
      displayName: 'linuxdo_3388',
    })))
    await vi.waitFor(() => expect(apiMocks.getSubscription.mock.calls.length).toBeGreaterThan(fetchPrimaryCalls))
    expect(apiMocks.getSubscriptionAccounts.mock.calls.length).toBeGreaterThanOrEqual(2)
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
    // 删除子账号会剔除其自动接入 Key：主账号订阅同步重拉
    await vi.waitFor(() => expect(apiMocks.getSubscription.mock.calls.filter(c => c[0] === 'sub-main').length).toBeGreaterThanOrEqual(2))
  })

  it('主保存钩子：绑定表单已填写时 maybeBindBeforeSave 执行绑定', async () => {
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
    const vm = wrapper.vm as { hasFilledBindForm: () => boolean; maybeBindBeforeSave: () => Promise<{ attempted: boolean; ok: boolean }> }
    await vi.waitFor(() => expect(vm.hasFilledBindForm()).toBe(false))

    await wrapper.find('input[type="password"]').setValue('new-api-access-token')
    await wrapper.find('input:not([type="password"])').setValue('42')
    expect(vm.hasFilledBindForm()).toBe(true)

    const result = await vm.maybeBindBeforeSave()
    expect(result).toEqual({ attempted: true, ok: true })
    await vi.waitFor(() => expect(apiMocks.provisionNewApiSubscription).toHaveBeenCalled())
  })

  it('主保存钩子：绑定失败返回 ok=false，主保存据此中止', async () => {
    apiMocks.verifyNewApiSubscription.mockRejectedValue(new Error('校验失败: timeout'))
    const wrapper = mountPanel({
      subscriptionUid: '',
      channelName: 'Legacy relay',
      baseUrl: 'https://relay.example.com',
      channelUid: 'ch-existing',
      channelKind: 'messages',
      isGeneric: true,
    })
    const vm = wrapper.vm as { hasFilledBindForm: () => boolean; maybeBindBeforeSave: () => Promise<{ attempted: boolean; ok: boolean }> }
    await wrapper.find('input[type="password"]').setValue('new-api-access-token')
    await wrapper.find('input:not([type="password"])').setValue('42')
    expect(vm.hasFilledBindForm()).toBe(true)

    const result = await vm.maybeBindBeforeSave()
    expect(result).toEqual({ attempted: true, ok: false })
    expect(apiMocks.provisionNewApiSubscription).not.toHaveBeenCalled()
    // 错误已渲染在表单 alert 中
    expect(wrapper.text()).toContain('校验失败: timeout')
  })

  it('主保存钩子：表单未填写时直接放行不触发绑定', async () => {
    const wrapper = mountPanel({
      subscriptionUid: '',
      channelName: 'Legacy relay',
      baseUrl: 'https://relay.example.com',
      channelUid: 'ch-existing',
      channelKind: 'messages',
      isGeneric: true,
    })
    const vm = wrapper.vm as { hasFilledBindForm: () => boolean; maybeBindBeforeSave: () => Promise<{ attempted: boolean; ok: boolean }> }
    await vi.waitFor(() => expect(vm.hasFilledBindForm()).toBe(false))
    const result = await vm.maybeBindBeforeSave()
    expect(result).toEqual({ attempted: false, ok: true })
    expect(apiMocks.verifyNewApiSubscription).not.toHaveBeenCalled()
    expect(apiMocks.provisionNewApiSubscription).not.toHaveBeenCalled()
  })
})
