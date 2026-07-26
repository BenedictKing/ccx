// @vitest-environment jsdom
import { mount } from '@vue/test-utils'
import { defineComponent, nextTick } from 'vue'
import { afterEach, describe, expect, it, vi } from 'vitest'

import ModelChipList from './ModelChipList.vue'

vi.mock('../../i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

const passthroughStub = defineComponent({
  template: '<span><slot /></span>',
})

/**
 * jsdom 不做真实布局，clientHeight / scrollHeight 恒为 0。
 * 通过 prototype 桩注入高度，模拟"两行视口 + 实际内容高度"。
 */
const stubLayout = (viewportHeight: number, contentHeight: number) => {
  const clientSpy = vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get')
    .mockImplementation(function (this: HTMLElement) {
      return this.classList.contains('model-chip-list__viewport--collapsed') ? viewportHeight : 0
    })
  const scrollSpy = vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get')
    .mockImplementation(function (this: HTMLElement) {
      return this.classList.contains('model-chip-list__models') ? contentHeight : 0
    })
  return () => {
    clientSpy.mockRestore()
    scrollSpy.mockRestore()
  }
}

// 视口 watch 是 post flush，且测量内部还 await 一次 nextTick，需多等一轮。
const flushMeasure = async () => {
  await nextTick()
  await nextTick()
}

const mountList = (models: string[]) => mount(ModelChipList, {
  props: { models },
  global: { stubs: { VChip: passthroughStub, VIcon: passthroughStub } },
})

describe('ModelChipList', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('内容未超出两行时不显示展开按钮', async () => {
    const restore = stubLayout(54, 24)
    const wrapper = mountList(['model-a', 'model-b'])
    await flushMeasure()

    expect(wrapper.findAll('.model-chip-list__toggle')).toHaveLength(0)
    expect(wrapper.text()).toContain('model-a')
    expect(wrapper.text()).toContain('model-b')
    restore()
  })

  it('内容超出两行时显示展开按钮并可切换', async () => {
    const restore = stubLayout(54, 200)
    const wrapper = mountList(['model-a', 'model-b', 'model-c'])
    await flushMeasure()

    const toggle = wrapper.get('.model-chip-list__toggle')
    expect(toggle.text()).toContain('channelEditor.protocolModels.expand')
    expect(wrapper.get('.model-chip-list__viewport').classes())
      .toContain('model-chip-list__viewport--collapsed')

    await toggle.trigger('click')
    expect(wrapper.get('.model-chip-list__toggle').text())
      .toContain('channelEditor.protocolModels.collapse')
    expect(wrapper.get('.model-chip-list__viewport').classes())
      .not.toContain('model-chip-list__viewport--collapsed')
    restore()
  })

  it('始终渲染全部模型，折叠只靠视口裁剪', async () => {
    const restore = stubLayout(54, 200)
    const models = Array.from({ length: 24 }, (_, i) => `model-${i}`)
    const wrapper = mountList(models)
    await flushMeasure()

    expect(wrapper.findAll('.model-chip-list__model')).toHaveLength(24)
    restore()
  })
})
