import { describe, expect, it } from 'vitest'
import { ref } from 'vue'
import type { ActivitySegment, ChannelRecentActivity } from '../services/api'
import { useChannelActivity } from './useChannelActivity'

const segment = (requestCount: number, successCount: number, failureCount: number): ActivitySegment => ({
  requestCount,
  successCount,
  failureCount,
  inputTokens: 0,
  outputTokens: 0,
})

function makeActivity(overrides: Partial<ChannelRecentActivity> = {}): ChannelRecentActivity {
  return {
    channelIndex: 7,
    segments: {},
    totalSegs: 4,
    rpm: 0,
    tpm: 0,
    ...overrides,
  }
}

describe('useChannelActivity', () => {
  it('生成匹配 150x100 SVG viewBox 的活动柱和 0..6 渐变档位', () => {
    const activity = makeActivity({
      segments: {
        0: segment(2, 2, 0),
        1: segment(2, 1, 1),
        2: segment(1, 0, 1),
      },
    })
    const { getActivityBars } = useChannelActivity(ref([activity]))

    const bars = getActivityBars(7)

    expect(bars).toHaveLength(3)
    expect(bars[0]).toMatchObject({
      index: 0,
      g: 0,
      x: 3.75,
      y: 15,
      width: 30,
      height: 85,
    })
    expect(bars[1].index).toBe(1)
    expect(bars[1].g).toBe(3)
    expect(bars[2].index).toBe(2)
    expect(bars[2].g).toBe(6)
    expect(bars[2].height).toBe(42.5)

    for (const bar of bars) {
      expect(Number.isInteger(bar.g)).toBe(true)
      expect(bar.g).toBeGreaterThanOrEqual(0)
      expect(bar.g).toBeLessThanOrEqual(6)
      expect(bar.x).toBeGreaterThanOrEqual(0)
      expect(bar.x + bar.width).toBeLessThanOrEqual(150)
      expect(bar.y).toBeGreaterThanOrEqual(0)
      expect(bar.y + bar.height).toBeLessThanOrEqual(100)
    }
  })

  it('仅为稀疏活动数据中的非零分段生成柱形', () => {
    const activity = makeActivity({
      totalSegs: 150,
      segments: {
        42: segment(3, 3, 0),
        99: segment(0, 0, 0),
      },
    })
    const { getActivityBars } = useChannelActivity(ref([activity]))

    expect(getActivityBars(7)).toMatchObject([
      { index: 42, x: 42.1, width: 0.8 },
    ])
    expect(getActivityBars(8)).toEqual([])
  })

  it('兼容旧数组格式并保留原始分段位置', () => {
    const activity = makeActivity({
      segments: [
        segment(0, 0, 0),
        segment(2, 2, 0),
        segment(0, 0, 0),
        segment(1, 0, 1),
      ],
    })
    const { getActivityBars } = useChannelActivity(ref([activity]))

    expect(getActivityBars(7).map(bar => bar.index)).toEqual([1, 3])
  })

  it('保持拆分前的 RPM/TPM 展示格式', () => {
    const activities = ref([
      makeActivity({ channelIndex: 7, rpm: 0, tpm: 0 }),
      makeActivity({ channelIndex: 8, rpm: 9.44, tpm: 1234 }),
      makeActivity({ channelIndex: 9, rpm: 12.6, tpm: 1234567 }),
    ])
    const { formatRPM, formatTPM } = useChannelActivity(activities)

    expect(formatRPM(6)).toBe('--')
    expect(formatTPM(6)).toBe('--')
    expect(formatRPM(7)).toBe('--')
    expect(formatTPM(7)).toBe('--')
    expect(formatRPM(8)).toBe('9.4')
    expect(formatTPM(8)).toBe('1.2K')
    expect(formatRPM(9)).toBe('13')
    expect(formatTPM(9)).toBe('1.2M')
  })

  it('保留拆分前的 activity gradient 和 area path 契约', () => {
    const activity = makeActivity({
      segments: {
        0: segment(3, 3, 0),
        1: segment(4, 1, 3),
      },
    })
    const { _getActivityAreaPath, _getActivityGradient } = useChannelActivity(ref([activity]))

    expect(_getActivityAreaPath(7)).toContain('L 3 100 L 0 100 Z')
    expect(_getActivityGradient(7)).toContain('linear-gradient(to right')
    expect(_getActivityGradient(7)).toContain('rgba(74, 222, 128, 0.38)')
    expect(_getActivityGradient(7)).toContain('rgba(239, 68, 68, 0.24000000000000002)')
    expect(_getActivityGradient(8)).toBe('transparent')
  })
})
