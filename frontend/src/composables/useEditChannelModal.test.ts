// @vitest-environment jsdom
import { mount } from '@vue/test-utils'
import { defineComponent, h, nextTick, reactive } from 'vue'
import { expect, it, vi } from 'vitest'
import { useEditChannelModal } from './useEditChannelModal'
import type { Channel } from '../services/api'

vi.mock('../i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('vuetify', () => ({ useTheme: () => ({ global: { current: { value: { dark: false } } } }) }))
vi.mock('../stores/channel', () => ({ useChannelStore: () => ({}) }))
vi.mock('../stores/dialog', () => ({ useDialogStore: () => ({}) }))

it.each([false, true])('既有渠道续接配置为 %s 时保存表单中的修改', async original => {
  const props = reactive({
    show: false,
    channelType: 'responses' as const,
    channel: {
      index: 0,
      name: 'test',
      baseUrl: 'https://example.com',
      apiKeys: ['test'],
      serviceType: 'responses',
      responsesReasoningFold: original
    } as Channel
  })
  const emit = vi.fn()
  let editor!: ReturnType<typeof useEditChannelModal>
  const wrapper = mount(
    defineComponent({
      setup() {
        editor = useEditChannelModal(props, emit)
        return () => h('div')
      }
    })
  )
  try {
    props.show = true
    await nextTick()
    expect(editor.form.responsesReasoningFold).toBe(original)
    editor.formRef.value = { validate: async () => ({ valid: true }) }
    editor.updateForm({ responsesReasoningFold: !original })
    await editor.handleSubmit()
    const save = emit.mock.calls.find(call => call[0] === 'save')
    expect(save?.[1].responsesReasoningFold).toBe(!original)
  } finally {
    wrapper.unmount()
  }
})
