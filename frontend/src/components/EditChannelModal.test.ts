// @vitest-environment jsdom
import { mount } from '@vue/test-utils'
import { defineComponent, nextTick } from 'vue'
import { expect, it, vi } from 'vitest'
import EditChannelModal from './EditChannelModal.vue'
import type { Channel } from '../services/api'

vi.mock('../i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('vuetify', () => ({ useTheme: () => ({ global: { current: { value: { dark: false } } } }) }))
vi.mock('../stores/channel', () => ({ useChannelStore: () => ({}) }))
vi.mock('../stores/dialog', () => ({ useDialogStore: () => ({}) }))
vi.mock('../composables/useDialogHotkeys', () => ({ useDialogHotkeys: vi.fn() }))

const slotStub = defineComponent({ template: '<div><slot /></div>' })
const switchStub = defineComponent({
  props: ['modelValue'],
  emits: ['update:modelValue'],
  template: '<button :aria-pressed="modelValue" @click="$emit(\'update:modelValue\', !modelValue)" />'
})

it('实际编辑界面按上游类型显示可切换的 Responses 续接设置', async () => {
  const wrapper = mount(EditChannelModal, {
    props: {
      show: false,
      channelType: 'responses',
      channel: {
        index: 0,
        name: 'test',
        baseUrl: 'https://example.com',
        apiKeys: ['test'],
        serviceType: 'responses',
        responsesReasoningFold: false
      } as Channel
    },
    global: {
      stubs: {
        VDialog: slotStub,
        VCard: slotStub,
        VCardText: slotStub,
        VCardActions: slotStub,
        VForm: slotStub,
        VIcon: slotStub,
        VSpacer: true,
        VBtn: slotStub,
        VTextField: true,
        VRow: slotStub,
        VCol: slotStub,
        VSelect: true,
        VSwitch: switchStub,
        VTooltip: true,
        VAlert: true,
        AddChannelHeader: true,
        AddChannelSidebarNav: true,
        BasicInfoSection: true,
        ProtocolModelAvailability: true,
        ApiKeyManagementSection: true,
        CustomHeadersSection: true,
        NewApiAccountPanel: true
      }
    }
  })
  try {
    await wrapper.setProps({ show: true })
    await nextTick()
    const setting = wrapper
      .findAll('.proxy-direct-row')
      .find(row => row.text().includes('channelEditor.compat.responsesReasoningFold.label'))
    expect(setting).toBeDefined()
    expect(setting!.get('button').attributes('aria-pressed')).toBe('false')
    await setting!.get('button').trigger('click')
    expect(setting!.get('button').attributes('aria-pressed')).toBe('true')
    await wrapper.setProps({ channelType: 'chat' })
    expect(wrapper.text()).not.toContain('channelEditor.compat.responsesReasoningFold.label')
    await wrapper.setProps({
      channel: {
        ...wrapper.props('channel')!,
        serviceType: 'openai',
        protocolRoutes: [
          { kind: 'chat', index: 0, channelUid: 'chat', name: 'test', serviceType: 'openai' },
          {
            kind: 'responses',
            index: 1,
            channelUid: 'responses',
            name: 'test',
            serviceType: 'responses',
            responsesReasoningFold: true
          }
        ]
      } as Channel
    })
    expect(wrapper.text()).toContain('channelEditor.compat.responsesReasoningFold.label')
  } finally {
    wrapper.unmount()
  }
})
