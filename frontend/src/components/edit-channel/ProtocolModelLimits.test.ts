// @vitest-environment jsdom
import { mount } from '@vue/test-utils'
import { defineComponent, nextTick } from 'vue'
import { describe, expect, it, vi } from 'vitest'
import ProtocolModelLimits from './ProtocolModelLimits.vue'
import type { ChannelProtocolRoute } from '../../services/api'

vi.mock('../../i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
const textarea = defineComponent({
  props: ['modelValue', 'rules', 'disabled'],
  emits: ['update:modelValue'],
  template: `<textarea :value="modelValue" :disabled="disabled" @input="$emit('update:modelValue', $event.target.value)" />`,
})
const routes: ChannelProtocolRoute[] = [
  { kind: 'responses', index: 1, channelUid: 'luna', name: 'aihub-luna', serviceType: 'responses', supportedModels: ['*luna'] },
  { kind: 'responses', index: 3, channelUid: 'code', name: 'aihub-code', serviceType: 'responses', supportedModels: ['*sol', '*astra'] },
]
const setup = () => mount(ProtocolModelLimits, { props: { modelValue: routes }, global: { stubs: { VTextarea: textarea } } })

describe('ProtocolModelLimits', () => {
  it('修改同协议的单条路由，保留其他路由且不直接调用接口', async () => {
    const wrapper = setup()
    await wrapper.get('[data-route-uid="luna"]').setValue('gpt*, !*luna\ngpt*')
    const edited = wrapper.emitted('update:modelValue')![0]![0] as ChannelProtocolRoute[]
    expect(edited[0]!.supportedModels).toEqual(['gpt*', '!*luna'])
    expect(edited[1]).toEqual(routes[1])
    expect(routes[0]!.supportedModels).toEqual(['*luna'])
  })
  it('清空白名单表示不限制', async () => {
    const wrapper = setup()
    await wrapper.get('[data-route-uid="code"]').setValue('')
    const edited = wrapper.emitted('update:modelValue')![0]![0] as ChannelProtocolRoute[]
    expect(edited[1]!.supportedModels).toEqual([])
  })
  it('输入中的换行在父组件同步后仍保留', async () => {
    const wrapper = setup()
    await wrapper.get('[data-route-uid="luna"]').setValue('gpt*\n')
    await wrapper.setProps({ modelValue: wrapper.emitted('update:modelValue')![0]![0] as ChannelProtocolRoute[] })
    await nextTick()
    expect((wrapper.get('[data-route-uid="luna"]').element as HTMLTextAreaElement).value).toBe('gpt*\n')
  })
  it('拒绝无效规则，允许空白、通配符和排除规则', () => {
    const wrapper = setup()
    const validate = wrapper.findComponent(textarea).props('rules')[0] as (value: string) => boolean | string
    expect(validate('')).toBe(true)
    expect(validate('gpt*, !*luna\n*deepseek*')).toBe(true)
    expect(validate('gpt*bad')).toBe('channelEditor.modelLimits.invalid')
    expect(validate('!')).toBe('channelEditor.modelLimits.invalid')
  })
})
