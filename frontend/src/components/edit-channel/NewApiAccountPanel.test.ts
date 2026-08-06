// @vitest-environment jsdom
import { mount } from '@vue/test-utils'
import { defineComponent, nextTick } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import NewApiAccountPanel from './NewApiAccountPanel.vue'

const apiMocks = vi.hoisted(() => ({
  getSubscription: vi.fn(),
  refreshSubscription: vi.fn(),
  updateNewApiCredentials: vi.fn(),
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

const mountPanel = () => mount(NewApiAccountPanel, {
  props: { subscriptionUid: 'sub-main' },
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
})
