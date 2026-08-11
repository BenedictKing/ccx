// @vitest-environment jsdom
import { mount } from '@vue/test-utils'
import { defineComponent, nextTick } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import ApiKeyManagementSection from './ApiKeyManagementSection.vue'

const apiMocks = vi.hoisted(() => ({
  patchKeyMultiplier: vi.fn(),
}))

vi.mock('../../services/api', () => ({
  api: apiMocks,
  ApiError: class extends Error {},
  ApiService: class {
    async patchKeyMultiplier(...args: unknown[]) {
      return apiMocks.patchKeyMultiplier(...args)
    }
  },
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
const selectStub = defineComponent({
  props: ['modelValue', 'items', 'itemTitle', 'itemValue', 'label'],
  emits: ['update:modelValue'],
  template: '<select :value="modelValue" @change="$emit(\'update:modelValue\', $event.target.value)"><option v-for="item in items" :key="item.value" :value="item.value">{{ item.title }}</option></select>',
})
const buttonStub = defineComponent({
  props: ['disabled', 'variant', 'color'],
  emits: ['click'],
  template: '<button :disabled="disabled" @click="$emit(\'click\')"><slot /></button>',
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
      VListItem: passthroughStub,
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
})
