// @vitest-environment jsdom
/* eslint-disable vue/one-component-per-file, vue/require-prop-types */
import { mount } from '@vue/test-utils'
import { defineComponent, nextTick } from 'vue'
import { describe, expect, it, vi } from 'vitest'

import ModelMappingSection from './ModelMappingSection.vue'

vi.mock('../../i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key,
  }),
}))

interface MappingRow {
  id: number
  source: string
  target: string
  reasoning?: '' | 'none' | 'low' | 'medium' | 'high' | 'xhigh' | 'max'
  noVision?: boolean
}

const passthroughStub = defineComponent({
  template: '<div><slot /></div>',
})

const buttonStub = defineComponent({
  name: 'VBtn',
  emits: ['click'],
  template: '<button @click="$emit(\'click\')"><slot /></button>',
})

const comboboxStub = defineComponent({
  name: 'VCombobox',
  props: ['items', 'label', 'modelValue'],
  emits: ['update:modelValue'],
  template: '<div />',
})

const tooltipStub = defineComponent({
  template: '<div><slot name="activator" :props="{}" /><slot /></div>',
})

const vuetifyStubs = {
  VBtn: buttonStub,
  VCard: passthroughStub,
  VCardText: passthroughStub,
  VCardTitle: passthroughStub,
  VChip: passthroughStub,
  VCombobox: comboboxStub,
  VIcon: passthroughStub,
  VSelect: passthroughStub,
  VTooltip: tooltipStub,
}

const sourceModelOptions = [
  { title: 'astra', value: 'astra' },
  { title: 'sol', value: 'sol' },
  { title: 'terra', value: 'terra' },
]

const mountSection = (mappingRows: MappingRow[]) => mount(ModelMappingSection, {
  props: {
    mappingRows,
    sourceModelOptions,
    targetModelOptions: [],
    fetchingModels: false,
    sourceMappingError: '',
    fetchModelsError: '',
    modelMappingHint: '',
    targetModelPlaceholder: '',
    showModelMappingPresets: false,
    showMessagesOpenAIChannelPresets: false,
    showClaudeChannelPresets: false,
    showCodexResponsesChannelPresets: false,
    supportsReasoningMappingOptions: false,
    reasoningEffortOptions: [],
  },
  global: {
    stubs: vuetifyStubs,
  },
})

const findNewMappingInput = (wrapper: ReturnType<typeof mountSection>, label: string) => wrapper
  .findAllComponents({ name: 'VCombobox' })
  .find(combobox => combobox.props('label') === label)

describe('ModelMappingSection', () => {
  it('自动去除历史重复源模型并保留最后一项', () => {
    const duplicateRows: MappingRow[] = [
      { id: 1, source: 'sol', target: 'old-target' },
      { id: 2, source: 'astra', target: 'astra-target' },
      { id: 3, source: 'sol', target: 'new-target' },
    ]
    const wrapper = mountSection(duplicateRows)

    const emittedRows = wrapper.emitted('update:mappingRows')?.[0]?.[0] as MappingRow[]
    expect(emittedRows).toEqual([
      { id: 3, source: 'sol', target: 'new-target' },
      { id: 2, source: 'astra', target: 'astra-target' },
    ])
  })

  it('按实时映射行过滤源模型候选并在删除后恢复', async () => {
    const wrapper = mountSection([{ id: 1, source: 'sol', target: 'target' }])
    const sourceInput = findNewMappingInput(wrapper, 'channelEditor.mapping.source.label')

    expect(sourceInput?.props('items')).toEqual([
      { title: 'astra', value: 'astra' },
      { title: 'terra', value: 'terra' },
    ])

    await wrapper.setProps({ mappingRows: [] })
    expect(sourceInput?.props('items')).toEqual(sourceModelOptions)
  })

  it('新增相同源模型时更新原行而不是追加重复行', async () => {
    const wrapper = mountSection([
      { id: 1, source: 'sol', target: 'old-target', noVision: true },
      { id: 2, source: 'astra', target: 'astra-target' },
    ])
    const sourceInput = findNewMappingInput(wrapper, 'channelEditor.mapping.source.label')
    const targetInput = findNewMappingInput(wrapper, 'channelEditor.mapping.target.label')

    sourceInput?.vm.$emit('update:modelValue', 'sol')
    targetInput?.vm.$emit('update:modelValue', 'new-target')
    await nextTick()
    const addButton = wrapper.findAll('button').find(button => button.text().includes('app.actions.add'))
    await addButton?.trigger('click')

    const events = wrapper.emitted('update:mappingRows')
    const emittedRows = events?.[events.length - 1]?.[0] as MappingRow[]
    expect(emittedRows).toHaveLength(2)
    expect(emittedRows[0]).toMatchObject({
      id: 1,
      source: 'sol',
      target: 'new-target',
      noVision: true,
    })
    expect(emittedRows[1]).toMatchObject({ id: 2, source: 'astra' })
  })

  it.each([
    {
      props: { showModelMappingPresets: true, showMessagesOpenAIChannelPresets: true },
      presets: [['DeepSeek', 'deepseek'], ['Kimi', 'kimi'], ['GLM', 'glm']],
    },
    {
      props: { showClaudeChannelPresets: true },
      presets: [['DeepSeek', 'deepseek'], ['Kimi', 'kimi'], ['GLM', 'glm']],
    },
    {
      props: { showCodexResponsesChannelPresets: true },
      presets: [['DeepSeek', 'deepseek'], ['Kimi', 'kimi'], ['GLM', 'glm']],
    },
  ])('按网关约定展示并触发一键预设', async ({ props, presets }) => {
    const wrapper = mountSection([])
    await wrapper.setProps(props)

    const presetButtons = wrapper.findAll('button').filter(button => !button.text().includes('app.actions.add'))
    expect(presetButtons.map(button => button.text())).toEqual(presets.map(([label]) => label))

    for (const button of presetButtons) {
      await button.trigger('click')
    }
    expect(wrapper.emitted('apply-preset')).toEqual(presets.map(([, preset]) => [preset]))
  })
})
