// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '../services/api'
import {
  ensureRuntimePresetsLoaded,
  resetRuntimePresetState,
  setRuntimePresetState,
  useRuntimePresets,
} from './useRuntimePresets'
import { resolveBuiltinUpstreamModelCapability, setRuntimeUpstreamModelCapabilities } from '../utils/channelPayload'

vi.mock('../services/api', () => ({
  api: {
    getPresets: vi.fn(),
  },
}))

const runtimeBundle = {
  schemaVersion: 3,
  dataVersion: '2026-07-10',
  subscription: {
    originTypes: [{ value: 'relay', tier: 'second' }],
    billingModes: ['token_plan'],
    sources: ['manual'],
    autoRefreshProviders: ['anthropic'],
    newApiDefaults: { originType: 'relay', originTier: 'second', billingMode: 'token_plan' },
    originTypeAliases: {},
  },
  modelRegistry: {
    upstreamCapabilities: [
      {
        patterns: ['^gpt-5$', '^gpt-5@prod$'],
        contextWindowTokens: 42,
        maxOutputTokens: 24,
        displayName: 'runtime-gpt-5',
      },
    ],
    benchmarkProfiles: [
      {
        patterns: ['^gpt-5$'],
        canonicalModel: 'gpt-5',
        overallScore: 88,
      },
    ],
  },
  channelPresets: {
    schemaVersion: 3,
    openAIMessages: {
      schemaVersion: 1,
      presets: {
        'gpt-5.5': {
          fastMode: false,
          textVerbosity: 'high' as const,
        },
      },
    },
    openAIChat: {
      schemaVersion: 1,
      providers: {
        mimo: {
          reasoningParamStyle: 'thinking' as const,
          authHeader: '' as const,
          normalizeSystemRoleToTopLevel: false,
          noVision: false,
        },
      },
    },
    claudeMessages: {
      schemaVersion: 1,
      providers: {
        mimo: {
          reasoningParamStyle: 'thinking' as const,
          authHeader: '' as const,
          normalizeSystemRoleToTopLevel: true,
          noVision: false,
        },
      },
    },
    codexResponses: {
      schemaVersion: 1,
      providers: {
        mimo: {
          reasoningParamStyle: 'reasoning' as const,
          codexToolCompat: true,
          stripCodexClientTools: true,
          noVision: false,
        },
      },
    },
  },
}

afterEach(() => {
  resetRuntimePresetState()
  setRuntimeUpstreamModelCapabilities(null)
  vi.clearAllMocks()
})

describe('useRuntimePresets', () => {
  it('应解包 runtime model registry 与 channel preset wrapper', async () => {
    const getPresets = api.getPresets as ReturnType<typeof vi.fn>
    getPresets.mockResolvedValue(runtimeBundle as never)

    const { ensureLoaded, effectiveModelRegistry, effectiveBenchmarkProfiles, effectiveChannelPresets } = useRuntimePresets()
    await ensureLoaded()

    expect(effectiveModelRegistry.value['^gpt-5$']?.displayName).toBe('runtime-gpt-5')
    expect(effectiveModelRegistry.value['^gpt-5@prod$']?.maxOutputTokens).toBe(24)
    expect(effectiveBenchmarkProfiles.value['^gpt-5$']?.overallScore).toBe(88)
    expect(effectiveChannelPresets.value.openAIMessages['gpt-5.5']?.textVerbosity).toBe('high')
    expect(effectiveChannelPresets.value.openAIChat.mimo?.reasoningParamStyle).toBe('thinking')
    expect(effectiveChannelPresets.value.claudeMessages.mimo?.normalizeSystemRoleToTopLevel).toBe(true)
    expect(resolveBuiltinUpstreamModelCapability('gpt-5')?.capability.displayName).toBe('runtime-gpt-5')
  })

  it('API 失败时模型能力应保持为空而不是使用生成镜像', async () => {
    const getPresets = api.getPresets as ReturnType<typeof vi.fn>
    getPresets.mockRejectedValue(new Error('network error'))

    const { ensureLoaded, subscriptionPreset, effectiveChannelPresets } = useRuntimePresets()
    await ensureLoaded()

    expect(subscriptionPreset.value.originTypes.some((item: { value: string }) => item.value === 'official_api')).toBe(true)
    expect(effectiveChannelPresets.value.openAIMessages['gpt-5.5']?.fastMode).toBe(true)
    expect(resolveBuiltinUpstreamModelCapability('claude-sonnet-5')).toBeNull()
  })

  it('显式注入运行时状态时应覆盖 generated channel presets', () => {
    setRuntimePresetState({
      subscription: runtimeBundle.subscription,
      modelRegistry: {
        '^gpt-5$': {
          contextWindowTokens: 42,
          maxOutputTokens: 24,
          displayName: 'runtime-gpt-5',
        },
      },
      benchmarkProfiles: {},
      channelPresets: {
        schemaVersion: 3,
        claudeMessages: runtimeBundle.channelPresets.claudeMessages.providers,
        openAIChat: runtimeBundle.channelPresets.openAIChat.providers,
        codexResponses: runtimeBundle.channelPresets.codexResponses.providers,
        openAIMessages: runtimeBundle.channelPresets.openAIMessages.presets,
      },
    })

    const { effectiveChannelPresets } = useRuntimePresets()
    expect(effectiveChannelPresets.value.claudeMessages.mimo?.normalizeSystemRoleToTopLevel).toBe(true)
    expect(effectiveChannelPresets.value.codexResponses.mimo?.reasoningParamStyle).toBe('reasoning')
  })

  it('force 请求应防止旧响应覆盖新状态', async () => {
    const getPresets = api.getPresets as ReturnType<typeof vi.fn>
    let resolveFirst!: (value: typeof runtimeBundle) => void
    let resolveSecond!: (value: typeof runtimeBundle) => void

    getPresets
      .mockImplementationOnce(() => new Promise((resolve) => {
        resolveFirst = resolve
      }))
      .mockImplementationOnce(() => new Promise((resolve) => {
        resolveSecond = resolve
      }))

    const firstPromise = ensureRuntimePresetsLoaded(true)
    const secondPromise = ensureRuntimePresetsLoaded(true)

    resolveSecond({
      ...runtimeBundle,
      dataVersion: 'newer',
      channelPresets: {
        ...runtimeBundle.channelPresets,
        openAIMessages: {
          schemaVersion: 1,
          presets: {
            'gpt-5.5': {
              fastMode: true,
              textVerbosity: 'high' as const,
            },
          },
        },
      },
    })
    await secondPromise

    resolveFirst({
      ...runtimeBundle,
      dataVersion: 'older',
      channelPresets: {
        ...runtimeBundle.channelPresets,
        openAIMessages: {
          schemaVersion: 1,
          presets: {
            'gpt-5.5': {
              fastMode: false,
              textVerbosity: 'high' as const,
            },
          },
        },
      },
    })
    await firstPromise

    const { runtimePresets, loading } = useRuntimePresets()
    expect(runtimePresets.value.dataVersion).toBe('newer')
    expect(runtimePresets.value.channelPresets.openAIMessages['gpt-5.5']?.fastMode).toBe(true)
    expect(loading.value).toBe(false)
  })
})
