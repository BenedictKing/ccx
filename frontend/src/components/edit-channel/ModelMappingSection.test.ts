// @vitest-environment jsdom
import { mount } from '@vue/test-utils'
import { defineComponent } from 'vue'
import { describe, expect, it, vi } from 'vitest'
import ModelMappingSection from './ModelMappingSection.vue'

vi.mock('../../i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

const passthroughStub = defineComponent({
  template: '<div><slot /></div>',
})
const comboBoxStub = defineComponent({
  props: {
    modelValue: { type: [String, Object], default: '' },
    items: { type: Array, default: () => [] },
    label: { type: String, default: '' },
  },
  emits: ['update:modelValue', 'focus', 'update:menu'],
  template: '<select :aria-label="label" @focus="$emit(\'focus\')"><option v-for="item in items" :key="item.value" :value="item.value">{{ item.title }}</option></select>',
})

describe('ModelMappingSection', () => {
  it('uses built-in and upstream options and emits normalized selections', async () => {
    const wrapper = mount(ModelMappingSection, {
      props: {
        modelValue: { sonnet: 'old-upstream-model' },
        sourceModelOptions: [{ title: 'sonnet', value: 'sonnet' }],
        targetModelOptions: [{ title: 'new-upstream-model', value: 'new-upstream-model' }],
      },
      global: {
        stubs: {
          VCard: passthroughStub,
          VCardTitle: passthroughStub,
          VCardText: passthroughStub,
          VIcon: passthroughStub,
          VBtn: passthroughStub,
          VCombobox: comboBoxStub,
        },
      },
    })

    const [source, target] = wrapper.findAllComponents(comboBoxStub)
    expect(source.props('items')).toEqual([{ title: 'sonnet', value: 'sonnet' }])
    expect(target.props('items')).toEqual([{ title: 'new-upstream-model', value: 'new-upstream-model' }])

    await target.trigger('focus')
    expect(wrapper.emitted('target-focus')).toHaveLength(1)

    target.vm.$emit('update:modelValue', { title: 'new-upstream-model', value: 'new-upstream-model' })
    await wrapper.vm.$nextTick()
    const updates = wrapper.emitted('update:modelValue') || []
    expect(updates[updates.length - 1]).toEqual([{ sonnet: 'new-upstream-model' }])
  })
})
