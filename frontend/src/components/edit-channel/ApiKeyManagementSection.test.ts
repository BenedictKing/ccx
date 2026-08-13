// @vitest-environment jsdom
import { mount } from '@vue/test-utils'
import { defineComponent, nextTick } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import ApiKeyManagementSection from './ApiKeyManagementSection.vue'
import { maskApiKey } from '../../utils/apiKeyMask'

const apiMocks = vi.hoisted(() => ({
  patchKeyMultiplier: vi.fn(),
  getManagedAccounts: vi.fn(),
  setKimiConsoleToken: vi.fn(),
  sha256KeyHash: vi.fn(),
}))

vi.mock('../../services/api', () => ({
  api: apiMocks,
  ApiError: class extends Error {},
  ApiService: class {
    async patchKeyMultiplier(...args: unknown[]) {
      return apiMocks.patchKeyMultiplier(...args)
    }

    async getManagedAccounts(...args: unknown[]) {
      return apiMocks.getManagedAccounts(...args)
    }

    async setKimiConsoleToken(...args: unknown[]) {
      return apiMocks.setKimiConsoleToken(...args)
    }
  },
}))

vi.mock('../../utils/hash', () => ({
  sha256KeyHash: (...args: unknown[]) => apiMocks.sha256KeyHash(...args),
}))

vi.mock('../../i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

const passthroughStub = defineComponent({ template: '<div v-bind="$attrs"><slot /></div>' })
const inputStub = defineComponent({
  props: ['modelValue', 'type', 'placeholder'],
  emits: ['update:modelValue'],
  template: '<input :type="type" :placeholder="placeholder" :value="modelValue" @input="$emit(\'update:modelValue\', $event.target.value)" />',
})
const selectStub = defineComponent({
  props: ['modelValue', 'items', 'itemTitle', 'itemValue', 'label'],
  emits: ['update:modelValue'],
  template: '<select :value="modelValue" @change="$emit(\'update:modelValue\', $event.target.value)"><option v-for="item in items" :key="item.value" :value="item.value">{{ item.title }}</option></select>',
})
const buttonStub = defineComponent({
  props: ['disabled', 'variant', 'color'],
  emits: ['click'],
  template: '<button :disabled="disabled" v-bind="$attrs" @click="$emit(\'click\')" type="button"><slot /></button>',
})
const listItemStub = defineComponent({
  emits: ['click'],
  template: '<div v-bind="$attrs" @click="$emit(\'click\')"><slot name="prepend" /><slot /><slot name="append" /></div>',
})

const mountSection = (props: Record<string, unknown> = {}) => mount(ApiKeyManagementSection, {
  props: {
    apiKeys: ['sk-1'],
    disabledKeys: [],
    apiKeyConfigs: [],
    keyModelsStatus: new Map(),
    isEditing: true,
    restoringKey: '',
    dialogOpen: true,
    providerId: '',
    accountUid: '',
    ...props,
  },
  global: {
    stubs: {
      VCard: passthroughStub,
      VCardTitle: passthroughStub,
      VCardText: passthroughStub,
      VCardActions: passthroughStub,
      VIcon: passthroughStub,
      VChip: passthroughStub,
      VTooltip: passthroughStub,
      VProgressCircular: passthroughStub,
      VProgressLinear: passthroughStub,
      VAlert: passthroughStub,
      VForm: passthroughStub,
      VRow: passthroughStub,
      VCol: passthroughStub,
      VSpacer: passthroughStub,
      VList: passthroughStub,
      VListItem: listItemStub,
      VListItemTitle: passthroughStub,
      VListItemSubtitle: passthroughStub,
      VDialog: defineComponent({
        props: ['modelValue'],
        emits: ['update:modelValue'],
        template: '<div v-if="modelValue"><slot /></div>',
      }),
      VSelect: selectStub,
      VTextField: inputStub,
      VBtn: buttonStub,
      VExpandTransition: passthroughStub,
      VDivider: passthroughStub,
      VCombobox: passthroughStub,
      UsageQuotaRows: passthroughStub,
    },
  },
})

describe('ApiKeyManagementSection', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    apiMocks.patchKeyMultiplier.mockResolvedValue({
      keyUid: 'uid-1',
      group: '',
      groupMultiplier: 0,
      maxMultiplier: 0,
      consumptionPolicy: 'opportunistic',
      effectiveCostClass: 'zero',
      status: 'manual',
      reason: 'ok',
      eligible: true,
      updatedAt: '2026-08-11T00:00:00Z',
    })
    apiMocks.getManagedAccounts.mockResolvedValue({ accounts: [] })
    apiMocks.setKimiConsoleToken.mockResolvedValue({
      usage: { validatedAt: '2026-08-13T00:00:00Z' },
    })
    apiMocks.sha256KeyHash.mockResolvedValue('hash-stub')
  })

  it('renders opportunistic chip on key row', async () => {
    const wrapper = mountSection({
      apiKeys: ['sk-1'],
      disabledKeys: [],
      apiKeyConfigs: [
        { key: 'sk-1', keyUid: 'uid-1', consumptionPolicy: 'opportunistic', effectiveCostClass: 'zero', groupMultiplier: 0, maxGroupMultiplier: 0 },
      ],
      channelUid: 'ch-1',
      channelKind: 'messages',
    })
    await nextTick()
    await vi.waitFor(() => expect(wrapper.text()).toContain('subscription.keyMultiplier.policyChip'))
    expect(wrapper.text()).toContain('zero')
  })

  it('opens multiplier editor with current policy', async () => {
    const wrapper = mountSection({
      apiKeyConfigs: [
        { key: 'sk-1', keyUid: 'uid-1', groupMultiplier: 1, maxGroupMultiplier: 2, consumptionPolicy: 'normal' },
      ],
      channelUid: 'ch-1',
      channelKind: 'messages',
    })
    await nextTick()
    await wrapper.find('button').trigger('click')
    await nextTick()

    const select = wrapper.findComponent(selectStub)
    expect(select.exists()).toBe(true)
    expect(select.props('modelValue')).toBe('normal')
  })

  it('mark public key shortcut prefills zero and opportunistic', async () => {
    const wrapper = mountSection({
      apiKeyConfigs: [
        { key: 'sk-1', keyUid: 'uid-1', groupMultiplier: 1, maxGroupMultiplier: 2 },
      ],
      channelUid: 'ch-1',
      channelKind: 'messages',
    })
    await nextTick()
    await wrapper.find('button').trigger('click')
    await nextTick()

    const markButton = wrapper.findAllComponents(buttonStub)
      .find(b => b.text().includes('subscription.keyMultiplier.markPublic'))
    expect(markButton).toBeTruthy()
    await markButton!.trigger('click')
    await nextTick()

    expect(wrapper.findComponent(selectStub).props('modelValue')).toBe('opportunistic')
  })

  it('saves multiplier with consumption policy and applies response fields', async () => {
    const wrapper = mountSection({
      apiKeyConfigs: [
        { key: 'sk-1', keyUid: 'uid-1', groupMultiplier: 1, maxGroupMultiplier: 2 },
      ],
      channelUid: 'ch-1',
      channelKind: 'messages',
    })
    await nextTick()
    await wrapper.find('button').trigger('click')
    await nextTick()

    const select = wrapper.findComponent(selectStub)
    await select.find('select').setValue('opportunistic')

    const saveButton = wrapper.findAllComponents(buttonStub)
      .find(b => b.text().includes('app.actions.save'))
    await saveButton!.trigger('click')
    await vi.waitFor(() => expect(apiMocks.patchKeyMultiplier).toHaveBeenCalled())

    expect(apiMocks.patchKeyMultiplier).toHaveBeenCalledWith(
      'messages',
      'ch-1',
      'uid-1',
      expect.objectContaining({ groupMultiplier: 1, maxGroupMultiplier: 2, consumptionPolicy: 'opportunistic' }),
    )
  })

  it('does not show mark-public shortcut for new_api keys', async () => {
    const wrapper = mountSection({
      apiKeyConfigs: [
        { key: 'sk-1', keyUid: 'uid-1', multiplierSource: 'new_api', groupMultiplier: 0.5, maxGroupMultiplier: 1 },
      ],
      channelUid: 'ch-1',
      channelKind: 'messages',
    })
    await nextTick()
    await wrapper.find('button').trigger('click')
    await nextTick()

    const markButton = wrapper.findAllComponents(buttonStub)
      .find(b => b.text().includes('subscription.keyMultiplier.markPublic'))
    expect(markButton).toBeFalsy()
  })

  it('keeps Kimi credential bound to the correct key row after save and reload with reversed credential order', async () => {
    const alphaKey = 'sk-alpha-1234567890'
    const betaKey = 'sk-beta-0987654321'
    const alphaMask = maskApiKey(alphaKey)
    const betaMask = maskApiKey(betaKey)

    apiMocks.getManagedAccounts.mockResolvedValue({
      accounts: [
        {
          accountUid: 'acct-kimi',
          providerId: 'kimi',
          name: 'kimi',
          credentials: [
            {
              credentialUid: 'cred-alpha',
              keyMask: alphaMask,
              hasKimiConsoleToken: false,
            },
            {
              credentialUid: 'cred-beta',
              keyMask: betaMask,
              hasKimiConsoleToken: false,
            },
          ],
          channels: [],
          endpointCount: 0,
        },
      ],
    })

    apiMocks.setKimiConsoleToken.mockImplementation(async (_accountUid: string, credentialUid: string) => {
      apiMocks.getManagedAccounts.mockResolvedValue({
        accounts: [
          {
            accountUid: 'acct-kimi',
            providerId: 'kimi',
            name: 'kimi',
            credentials: [
              {
                credentialUid: 'cred-beta',
                keyMask: betaMask,
                hasKimiConsoleToken: false,
              },
              {
                credentialUid: 'cred-alpha',
                keyMask: alphaMask,
                hasKimiConsoleToken: credentialUid === 'cred-alpha',
                kimiCodeUsage: credentialUid === 'cred-alpha'
                  ? {
                      weeklyUsage: { used: 12, limit: 100, remaining: 88, resetTime: '2026-08-20T00:00:00Z' },
                      totalQuota: { used: 12, limit: 100, remaining: 88 },
                      rateLimits: [],
                      validatedAt: '2026-08-13T00:00:00Z',
                    }
                  : undefined,
              },
            ],
            channels: [],
            endpointCount: 0,
          },
        ],
      })
      return {
        usage: {
          weeklyUsage: { used: 12, limit: 100, remaining: 88, resetTime: '2026-08-20T00:00:00Z' },
          totalQuota: { used: 12, limit: 100, remaining: 88 },
          rateLimits: [],
          validatedAt: '2026-08-13T00:00:00Z',
        },
      }
    })

    const wrapper = mountSection({
      apiKeys: [alphaKey, betaKey],
      providerId: 'kimi',
      accountUid: 'acct-kimi',
      serviceType: 'claude',
    })

    await vi.waitFor(() => expect(apiMocks.getManagedAccounts).toHaveBeenCalledTimes(1))
    await vi.waitFor(() => expect(wrapper.html()).toContain('kimiConsoleToken.notConfigured'))

    const alphaRow = wrapper.get(`[data-key-row="${alphaKey}"]`)
    const betaRow = wrapper.get(`[data-key-row="${betaKey}"]`)

    await alphaRow.get('button[aria-label="kimiConsoleToken.title"]').trigger('click')
    await nextTick()

    const expandedAlphaRow = wrapper.get(`[data-key-row="${alphaKey}"]`)
    expect(expandedAlphaRow.html()).toContain('kimiConsoleToken.notConfigured')
    expect(betaRow.html()).not.toContain('kimiConsoleToken.configured')

    const tokenInput = expandedAlphaRow.get('input[type="password"]')
    await tokenInput.setValue('kimi-token-alpha')

    const saveButton = expandedAlphaRow.findAll('button')
      .find(button => button.text().includes('kimiConsoleToken.verifyAndSave'))
    expect(saveButton).toBeTruthy()
    await saveButton!.trigger('click')

    await vi.waitFor(() => expect(apiMocks.setKimiConsoleToken).toHaveBeenCalledWith(
      'acct-kimi',
      'cred-alpha',
      'kimi-token-alpha',
    ))

    apiMocks.getManagedAccounts.mockResolvedValueOnce({
      accounts: [
        {
          accountUid: 'acct-kimi',
          providerId: 'kimi',
          name: 'kimi',
          credentials: [
            {
              credentialUid: 'cred-beta',
              keyMask: betaMask,
              hasKimiConsoleToken: false,
            },
            {
              credentialUid: 'cred-alpha',
              keyMask: alphaMask,
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
        },
      ],
    })

    await wrapper.setProps({ accountUid: 'acct-kimi-reload' })
    await wrapper.setProps({ accountUid: 'acct-kimi' })
    await vi.waitFor(() => expect(apiMocks.getManagedAccounts).toHaveBeenCalledTimes(3))
    await nextTick()

    const reloadedAlphaRow = wrapper.get(`[data-key-row="${alphaKey}"]`)
    const reloadedBetaRow = wrapper.get(`[data-key-row="${betaKey}"]`)

    expect(reloadedAlphaRow.html()).toContain('kimiConsoleToken.configured')
    expect(reloadedAlphaRow.html()).toContain('kimiConsoleToken.validatedAt')
    expect(reloadedBetaRow.html()).not.toContain('kimiConsoleToken.configured')
  })
})
