// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { writeClipboardText } from './clipboard'

describe('writeClipboardText', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('优先使用 navigator.clipboard.writeText', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })

    await writeClipboardText('hello')

    expect(writeText).toHaveBeenCalledWith('hello')
  })

  it('clipboard API 抛错时回退 execCommand', async () => {
    const writeText = vi.fn().mockRejectedValue(new Error('denied'))
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    const execCommand = vi.fn().mockReturnValue(true)
    document.execCommand = execCommand

    await writeClipboardText('fallback')

    expect(writeText).toHaveBeenCalled()
    expect(execCommand).toHaveBeenCalledWith('copy')
  })

  it('无 clipboard API 时直接走 execCommand', async () => {
    Object.defineProperty(navigator, 'clipboard', { value: undefined, configurable: true })
    const execCommand = vi.fn().mockReturnValue(true)
    document.execCommand = execCommand

    await writeClipboardText('legacy')

    expect(execCommand).toHaveBeenCalledWith('copy')
  })

  it('execCommand 返回 false 时抛错且清理 textarea', async () => {
    Object.defineProperty(navigator, 'clipboard', { value: undefined, configurable: true })
    document.execCommand = vi.fn().mockReturnValue(false)

    await expect(writeClipboardText('x')).rejects.toThrow('Copy command failed')
    expect(document.querySelector('textarea')).toBeNull()
  })
})
