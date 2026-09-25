// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { defineComponent, h, onMounted } from 'vue'
import { mount } from '@vue/test-utils'
import { registerGlobalTick, useGlobalTick, __resetForTests__ } from './useGlobalTick'

/**
 * 模拟 document.visibilityState 变化并触发 visibilitychange 事件
 */
function setVisibility(state: 'visible' | 'hidden'): void {
  Object.defineProperty(document, 'visibilityState', {
    configurable: true,
    get: () => state,
  })
  document.dispatchEvent(new Event('visibilitychange'))
}

describe('registerGlobalTick', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    setVisibility('visible')
  })

  afterEach(() => {
    __resetForTests__()
    vi.restoreAllMocks()
    vi.useRealTimers()
  })

  it('基本触发：间隔到达时回调被调用', () => {
    const fn = vi.fn()
    const unsubscribe = registerGlobalTick(1000, fn)

    vi.advanceTimersByTime(999)
    expect(fn).not.toHaveBeenCalled()

    vi.advanceTimersByTime(1)
    expect(fn).toHaveBeenCalledTimes(1)

    vi.advanceTimersByTime(1000)
    expect(fn).toHaveBeenCalledTimes(2)

    unsubscribe()
  })

  it('相同 interval 的多订阅者共用一个 setInterval', () => {
    const spy = vi.spyOn(globalThis, 'setInterval')
    const a = vi.fn()
    const b = vi.fn()
    const c = vi.fn()

    const u1 = registerGlobalTick(500, a)
    const u2 = registerGlobalTick(500, b)
    const u3 = registerGlobalTick(500, c)

    // 只应创建一个 setInterval
    expect(spy).toHaveBeenCalledTimes(1)

    vi.advanceTimersByTime(500)
    expect(a).toHaveBeenCalledTimes(1)
    expect(b).toHaveBeenCalledTimes(1)
    expect(c).toHaveBeenCalledTimes(1)

    u1(); u2(); u3()
  })

  it('不同 interval 各自独立', () => {
    const spy = vi.spyOn(globalThis, 'setInterval')
    const fastCb = vi.fn()
    const slowCb = vi.fn()

    const u1 = registerGlobalTick(100, fastCb)
    const u2 = registerGlobalTick(300, slowCb)

    expect(spy).toHaveBeenCalledTimes(2)

    vi.advanceTimersByTime(100)
    expect(fastCb).toHaveBeenCalledTimes(1)
    expect(slowCb).not.toHaveBeenCalled()

    vi.advanceTimersByTime(200) // 共 300ms
    expect(fastCb).toHaveBeenCalledTimes(3)
    expect(slowCb).toHaveBeenCalledTimes(1)

    u1(); u2()
  })

  it('unsubscribe 后不再接收回调', () => {
    const fn = vi.fn()
    const unsubscribe = registerGlobalTick(1000, fn)

    vi.advanceTimersByTime(1000)
    expect(fn).toHaveBeenCalledTimes(1)

    unsubscribe()
    vi.advanceTimersByTime(5000)
    expect(fn).toHaveBeenCalledTimes(1) // 不再增加
  })

  it('最后一个订阅者退订后，底层 setInterval 被清理', () => {
    const clearSpy = vi.spyOn(globalThis, 'clearInterval')
    const fn = vi.fn()
    const unsubscribe = registerGlobalTick(2000, fn)

    expect(clearSpy).not.toHaveBeenCalled()
    unsubscribe()
    expect(clearSpy).toHaveBeenCalledTimes(1)
  })

  it('多订阅者退订：仅最后一个触发 clearInterval', () => {
    const clearSpy = vi.spyOn(globalThis, 'clearInterval')
    const a = vi.fn()
    const b = vi.fn()
    const u1 = registerGlobalTick(1500, a)
    const u2 = registerGlobalTick(1500, b)

    u1()
    expect(clearSpy).not.toHaveBeenCalled()
    u2()
    expect(clearSpy).toHaveBeenCalledTimes(1)
  })

  it('visibility hidden 时暂停，不触发回调', () => {
    const fn = vi.fn()
    const unsubscribe = registerGlobalTick(1000, fn)

    setVisibility('hidden')
    vi.advanceTimersByTime(5000)
    expect(fn).not.toHaveBeenCalled()

    unsubscribe()
  })

  it('visibility 恢复：已超期则立即补触发一次', () => {
    const fn = vi.fn()
    const unsubscribe = registerGlobalTick(1000, fn)

    vi.advanceTimersByTime(1000)
    expect(fn).toHaveBeenCalledTimes(1)

    setVisibility('hidden')
    vi.advanceTimersByTime(5000) // 5s 没触发
    expect(fn).toHaveBeenCalledTimes(1)

    setVisibility('visible')
    // 距上次 tick 已 5s，超过 1s 间隔 → 立即补一次
    expect(fn).toHaveBeenCalledTimes(2)

    // 恢复后 1s 正常触发
    vi.advanceTimersByTime(1000)
    expect(fn).toHaveBeenCalledTimes(3)

    unsubscribe()
  })

  it('visibility 恢复：未超期则不补触发', () => {
    const fn = vi.fn()
    const unsubscribe = registerGlobalTick(1000, fn)

    vi.advanceTimersByTime(1000)
    expect(fn).toHaveBeenCalledTimes(1)

    setVisibility('hidden')
    vi.advanceTimersByTime(300) // 只过了 300ms（未满 1s）
    setVisibility('visible')
    // 距上次 tick 仅 300ms，不应立即补
    expect(fn).toHaveBeenCalledTimes(1)

    unsubscribe()
  })

  it('回调抛错不影响其他订阅者', () => {
    const bad = vi.fn(() => { throw new Error('boom') })
    const good = vi.fn()

    // 静默 console.warn
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})

    const u1 = registerGlobalTick(500, bad)
    const u2 = registerGlobalTick(500, good)

    vi.advanceTimersByTime(500)
    expect(bad).toHaveBeenCalledTimes(1)
    expect(good).toHaveBeenCalledTimes(1) // 仍然被调用
    expect(warnSpy).toHaveBeenCalled()

    u1(); u2()
  })

  it('P1 fix: tab 隐藏时首次注册订阅不应启动 setInterval', () => {
    const spy = vi.spyOn(globalThis, 'setInterval')
    const fn = vi.fn()

    // 模拟后台 tab 打开场景：visibilityState === 'hidden'
    setVisibility('hidden')
    const unsubscribe = registerGlobalTick(1000, fn)

    // setInterval 不应被调用
    expect(spy).not.toHaveBeenCalled()

    // 即使时间推进也不触发回调
    vi.advanceTimersByTime(10_000)
    expect(fn).not.toHaveBeenCalled()

    // visibility 恢复时：启动定时器 + 因已超期立即补触发一次
    setVisibility('visible')
    expect(spy).toHaveBeenCalledTimes(1)
    expect(fn).toHaveBeenCalledTimes(1) // 立即补触发

    // 此后每 1000ms 正常触发
    vi.advanceTimersByTime(1000)
    expect(fn).toHaveBeenCalledTimes(2)

    unsubscribe()
  })
})

describe('useGlobalTick (组件级)', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    setVisibility('visible')
  })

  afterEach(() => {
    __resetForTests__()
    vi.restoreAllMocks()
    vi.useRealTimers()
  })

  it('组件 unmount 时自动退订', () => {
    const fn = vi.fn()

    const TestComp = defineComponent({
      setup() {
        const tick = useGlobalTick(1000, 'test')
        tick.onTick(fn)
        return () => h('div')
      },
    })

    const wrapper = mount(TestComp)
    vi.advanceTimersByTime(1000)
    expect(fn).toHaveBeenCalledTimes(1)

    wrapper.unmount()
    vi.advanceTimersByTime(5000)
    expect(fn).toHaveBeenCalledTimes(1) // unmount 后不再触发
  })

  it('pause/resume 只影响自己，不影响其他订阅者', () => {
    const selfCb = vi.fn()
    const otherCb = vi.fn()

    const TestComp = defineComponent({
      setup() {
        const tick = useGlobalTick(500, 'self')
        tick.onTick(selfCb)
        // 暴露 pause/resume 到测试
        return { pause: tick.pause, resume: tick.resume, render: () => h('div') }
      },
      render() { return h('div') },
    })

    const wrapper = mount(TestComp)
    // 另一个订阅者（来自模块外）
    const unsubOther = registerGlobalTick(500, otherCb)

    vi.advanceTimersByTime(500)
    expect(selfCb).toHaveBeenCalledTimes(1)
    expect(otherCb).toHaveBeenCalledTimes(1)

    wrapper.vm.pause()
    vi.advanceTimersByTime(500)
    expect(selfCb).toHaveBeenCalledTimes(1) // 暂停了
    expect(otherCb).toHaveBeenCalledTimes(2) // 其他人继续

    wrapper.vm.resume()
    vi.advanceTimersByTime(500)
    expect(selfCb).toHaveBeenCalledTimes(2)
    expect(otherCb).toHaveBeenCalledTimes(3)

    unsubOther()
    wrapper.unmount()
  })

  it('P1 fix: onTick 在 onMounted 回调中调用时也能正确退订', () => {
    // 回归：useGlobalTick 必须在 setup 同步阶段注册 onUnmounted，
    // 不能依赖 onTick 内部注册（否则放在 onMounted 里时 Vue 会丢弃 onUnmounted）
    const fn = vi.fn()

    const TestComp = defineComponent({
      setup() {
        const tick = useGlobalTick(1000, 'late-register')
        // 模拟常见用法：在 onMounted 回调里调 onTick（因为 refreshData 等可能在下方定义）
        onMounted(() => {
          tick.onTick(fn)
        })
        return () => h('div')
      },
    })

    const wrapper = mount(TestComp)
    vi.advanceTimersByTime(1000)
    expect(fn).toHaveBeenCalledTimes(1)

    // 卸载后应立即停止接收 tick
    wrapper.unmount()
    vi.advanceTimersByTime(10_000)
    expect(fn).toHaveBeenCalledTimes(1) // 不再增加，说明退订成功
  })

  it('P2 fix: onTick 的 async 拒绝走 fireListeners 的 warn 路径，不产生 unhandled rejection', async () => {
    // 回归：wrapped 必须 `return fn()`，否则 Promise 不会冒到 fireListeners
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const rejectingAsync = vi.fn(async () => {
      throw new Error('async boom')
    })

    const TestComp = defineComponent({
      setup() {
        const tick = useGlobalTick(500, 'async-reject')
        tick.onTick(rejectingAsync)
        return () => h('div')
      },
    })

    const wrapper = mount(TestComp)
    vi.advanceTimersByTime(500)
    expect(rejectingAsync).toHaveBeenCalledTimes(1)

    // 等微任务队列冲刷，让 .catch 运行
    await Promise.resolve()
    await Promise.resolve()

    expect(warnSpy).toHaveBeenCalled()
    const warnMsg = warnSpy.mock.calls[0].join(' ')
    expect(warnMsg).toContain('[useGlobalTick]')

    wrapper.unmount()
  })
})
