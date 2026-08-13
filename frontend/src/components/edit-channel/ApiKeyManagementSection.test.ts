// @vitest-environment jsdom
import { defineComponent, nextTick } from 'vue'
import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import ApiKeyManagementSection from './ApiKeyManagementSection.vue'

const apiMocks = vi.hoisted(() => ({
  getManagedAccounts: vi.fn(),
  setKimiConsoleToken: vi.fn(),
}))

vi.mock('../../services/api', () => ({
  api: apiMocks,
  ApiService: vi.fn().mockImplementation(() => ({
    getManagedAccounts: apiMocks.getManagedAccounts,
    setKimiConsoleToken: apiMocks.setKimiConsoleToken,
  })),
}))

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
  props: ['disabled', 'loading', 'title'],
  emits: ['click'],
  template: '<button :disabled="disabled" :title="title" @click="$emit(\'click\')"><slot /></button>',
})

const mountSection = (props: Record<string, unknown> = {}) => mount(ApiKeyManagementSection, {
  props: {
    apiKeys: ['sk-kimi-alpha', 'sk-kimi-beta'],
    disabledKeys: [],
    keyModelsStatus: new Map(),
    isEditing: true,
    restoringKey: '',
    dialogOpen: true,
    serviceType: 'claude',
    providerId: 'kimi',
    accountUid: 'acct_kimi',
    apiKeyConfigs: [
      { key: 'sk-kimi-alpha', credentialUid: 'cred_kimi_alpha', baseUrl: 'https://api.kimi.com/coding' },
      { key: 'sk-kimi-beta', credentialUid: 'cred_kimi_beta', baseUrl: 'https://api.kimi.com/coding' },
    ],
    ...props,
  },
  global: {
    stubs: {
      VAlert: passthroughStub,
      VBtn: buttonStub,
      VCard: passthroughStub,
      VCardText: passthroughStub,
      VCardTitle: passthroughStub,
      VChip: passthroughStub,
      VCol: passthroughStub,
      VDialog: passthroughStub,
      VExpandTransition: passthroughStub,
      VIcon: passthroughStub,
      VList: passthroughStub,
      VListItem: passthroughStub,
      VListItemTitle: passthroughStub,
      VProgressCircular: passthroughStub,
      VProgressLinear: passthroughStub,
      VRow: passthroughStub,
      VTextField: inputStub,
      VTooltip: passthroughStub,
      UsageQuotaRows: passthroughStub,
    },
  },
})

describe('ApiKeyManagementSection', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    apiMocks.getManagedAccounts.mockResolvedValue({
      accounts: [{
        accountUid: 'acct_kimi',
        providerId: 'kimi',
        name: 'kimi',
        credentials: [
          {
            credentialUid: 'cred_kimi_alpha',
            keyMask: 'sk-ki***pha',
            hasKimiConsoleToken: true,
            kimiCodeUsage: {
              weeklyUsage: { used: 12, limit: 100, remaining: 88, resetTime: '2026-08-20T00:00:00Z' },
              totalQuota: { used: 12, limit: 100, remaining: 88 },
              rateLimits: [],
              validatedAt: '2026-08-13T00:00:00Z',
            },
          },
          {
            credentialUid: 'cred_kimi_beta',
            keyMask: 'sk-ki***eta',
          },
        ],
        channels: [],
        endpointCount: 0,
      }],
    })
    apiMocks.setKimiConsoleToken.mockResolvedValue({
      accountUid: 'acct_kimi',
      credentialUid: 'cred_kimi_alpha',
      usage: {
        weeklyUsage: { used: 12, limit: 100, remaining: 88, resetTime: '2026-08-20T00:00:00Z' },
        totalQuota: { used: 12, limit: 100, remaining: 88 },
        rateLimits: [],
        validatedAt: '2026-08-13T00:00:00Z',
      },
    })
  })

  it('保存后重载仍保持 Kimi 凭证绑定到正确 key 行', async () => {
    const wrapper = mountSection()
    await vi.waitFor(() => expect(apiMocks.getManagedAccounts).toHaveBeenCalled())
    await nextTick()

    const rows = wrapper.findAll('code.text-caption')
    expect(rows.map(row => row.text())).toEqual(expect.arrayContaining(['sk-kimi-alpha', 'sk-kimi-beta']))

    const alphaRow = wrapper.get('[data-key-row="sk-kimi-alpha"]')
    const betaRow = wrapper.get('[data-key-row="sk-kimi-beta"]')
    await alphaRow.get('button[aria-label="kimiConsoleToken.title"]').trigger('click')
    await nextTick()

    expect(alphaRow.text()).toContain('kimiConsoleToken.configured')
    expect(alphaRow.text()).toContain('kimiConsoleToken.verifyAndSave')
    expect(alphaRow.text()).toContain('kimiConsoleToken.validatedAt')
    expect(betaRow.text()).not.toContain('kimiConsoleToken.configured')

    const tokenInput = alphaRow.get('input[type="password"]')
    await tokenInput.setValue('Bearer web-session-secret')
    const saveButton = alphaRow.findAll('button').find(button => button.text().includes('kimiConsoleToken.verifyAndSave'))
    expect(saveButton).toBeTruthy()
    await saveButton!.trigger('click')

    await vi.waitFor(() => expect(apiMocks.setKimiConsoleToken).toHaveBeenCalledWith(
      'acct_kimi',
      'cred_kimi_alpha',
      'Bearer web-session-secret',
    ))
    expect(wrapper.text()).not.toContain('web-session-secret')

    apiMocks.getManagedAccounts.mockResolvedValueOnce({
      accounts: [{
        accountUid: 'acct_kimi',
        providerId: 'kimi',
        name: 'kimi',
        credentials: [
          {
            credentialUid: 'cred_kimi_beta',
            keyMask: 'sk-ki***eta',
          },
          {
            credentialUid: 'cred_kimi_alpha',
            keyMask: 'sk-ki***pha',
            hasKimiConsoleToken: true,
            kimiCodeUsage: {
              weeklyUsage: { used: 12, limit: 100, remaining: 88, resetTime: '2026-08-20T00:00:00Z' },
              totalQuota: { used: 12, limit: 100, remaining: 88 },
              rateLimits: [],
              validatedAt: '2026-08-13T00:00:00Z',
            },
          },
        ],
        channels: [],
        endpointCount: 0,
      }],
    })

    await wrapper.setProps({ accountUid: 'acct_kimi-reload', providerId: 'kimi' })
    await wrapper.setProps({ accountUid: 'acct_kimi', providerId: 'kimi' })
    await vi.waitFor(() => expect(apiMocks.getManagedAccounts).toHaveBeenCalledTimes(3))
    await nextTick()

    const reloadedAlphaRow = wrapper.get('[data-key-row="sk-kimi-alpha"]')
    const reloadedBetaRow = wrapper.get('[data-key-row="sk-kimi-beta"]')
    await reloadedAlphaRow.get('button[aria-label="kimiConsoleToken.title"]').trigger('click')
    await nextTick()
    expect(reloadedAlphaRow.text()).toContain('kimiConsoleToken.configured')
    expect(reloadedAlphaRow.text()).toContain('kimiConsoleToken.validatedAt')
    expect(reloadedBetaRow.text()).not.toContain('kimiConsoleToken.configured')
  })
})
