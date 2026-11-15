// @vitest-environment jsdom
/* eslint-disable vue/one-component-per-file, vue/require-prop-types */
import { mount } from '@vue/test-utils'
import { defineComponent, h, nextTick } from 'vue'
import { describe, expect, it, vi } from 'vitest'
import CapabilityTestDialog from './CapabilityTestDialog.vue'
import type { CapabilityTestJob } from '../services/api'

vi.mock('../i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

const box = defineComponent({ setup: (_, { slots }) => () => h('div', slots.default?.()) })
const field = defineComponent({
  props: ['modelValue', 'clearable'], emits: ['update:modelValue'], setup: () => () => h('input'),
})
const button = defineComponent({
  props: ['disabled'], emits: ['click'],
  setup: (props, { slots, emit }) => () => h('button', {
    disabled: props.disabled, onClick: () => emit('click'),
  }, slots.default?.()),
})

function renderDialog(protocol: string) {
  const job: CapabilityTestJob = {
    jobId: '', channelId: 0, channelName: 'test', channelKind: 'responses', sourceType: 'openai',
    status: 'idle', lifecycle: 'pending', outcome: 'unknown', compatibleProtocols: [],
    totalDuration: 0, updatedAt: '',
    progress: {
      totalModels: 0, queuedModels: 0, runningModels: 0, successModels: 0,
      failedModels: 0, skippedModels: 0, completedModels: 0,
    },
    tests: [{
      protocol, status: 'idle', lifecycle: 'pending', outcome: 'unknown',
      success: false, latency: 0, streamingSupported: false, testedModel: '', testedAt: '',
    }],
  }
  return mount(CapabilityTestDialog, {
    props: { modelValue: true, channelName: 'test', currentTab: 'responses', capabilityJob: job, capabilityRpm: 30 },
    global: { stubs: {
      VDialog: box, VCard: box, VCardTitle: box, VCardText: box, VDivider: box,
      VIcon: box, VChip: box, VTable: box, VTooltip: true, VBtn: button,
      VTextField: field, CapabilityModelResults: true,
    } },
  })
}

describe('能力测试自选模型', () => {
  it.each(['responses', 'responses->chat'])('向 %s 协议提交输入的模型', async protocol => {
    const wrapper = renderDialog(protocol)
    try {
      wrapper.findComponent(field).vm.$emit('update:modelValue', ' custom-model ')
      await nextTick()
      const buttons = wrapper.findAll('button').filter(item => item.text() === 'capability.startTest')
      expect(buttons).toHaveLength(2)
      for (const target of buttons) await target.trigger('click')
      expect(wrapper.emitted('testProtocol')).toEqual([
        [protocol, ['custom-model']], [protocol, ['custom-model']],
      ])
    } finally {
      wrapper.unmount()
    }
  })

  it('清空自选模型后可以使用默认模型发起测试', async () => {
    const wrapper = renderDialog('responses')
    try {
      const input = wrapper.findComponent(field)
      input.vm.$emit('update:modelValue', 'custom-model')
      await nextTick()
      input.vm.$emit('update:modelValue', null)
      await nextTick()
      const target = wrapper.findAll('button').find(item => item.text() === 'capability.startTest')!
      await target.trigger('click')
      expect(wrapper.emitted('testProtocol')).toEqual([['responses', undefined]])
    } finally {
      wrapper.unmount()
    }
  })
})
