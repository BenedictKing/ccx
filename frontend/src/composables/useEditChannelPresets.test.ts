import { computed, reactive } from 'vue'
import { describe, expect, it, vi } from 'vitest'
import { useEditChannelPresets } from './useEditChannelPresets'

type Options = Parameters<typeof useEditChannelPresets>[0]
const claudeSources = ['fable', 'haiku', 'opus', 'sonnet']
const codexSources = ['astra', 'codex-auto-review', 'luna', 'sol', 'terra']

function makeForm(serviceType: Options['form']['serviceType']): Options['form'] {
  return reactive({
    serviceType, fastMode: false, textVerbosity: '', passbackReasoningContent: false,
    passbackThinkingBlocks: false, stripEmptyTextBlocks: false, normalizeSystemRoleToTopLevel: false,
    normalizeMetadataUserId: false, stripBillingHeader: false, stripImageGenerationTool: false,
    noVision: false, noVisionModels: [], visionFallbackModel: '', visionFallbackReasoningEffort: '',
    modelMapping: {}, reasoningMapping: {}, reasoningParamStyle: 'reasoning',
    codexNativeToolPassthrough: false, codexToolCompat: false, stripCodexClientTools: false,
    normalizeNonstandardChatRoles: false, authHeader: 'auto',
  })
}

describe('网关一键预设', () => {
  for (const channelType of ['messages', 'responses'] as const) {
    for (const serviceType of ['openai', 'claude'] as const) {
      it.each([
        ['deepseek', 'deepseek-flash'], ['kimi', 'kimi-k3.1-pro'], ['glm', 'glm-5.4'],
      ])(`${channelType}/${serviceType} 按入口选择 %s 源模型类别`, (provider, target) => {
        const form = makeForm(serviceType)
        const sync = vi.fn()
        useEditChannelPresets({ form, channelType: computed(() => channelType),
          supportsOpenAIAdvancedOptions: computed(() => serviceType === 'openai'),
          syncModelMappingRowsFromForm: sync,
        }).applyPreset(provider)
        expect(Object.keys(form.modelMapping).sort()).toEqual(channelType === 'responses' ? codexSources : claudeSources)
        if (provider === 'glm' && channelType === 'messages') {
          expect(form.modelMapping).toEqual({
            fable: 'glm-5.4', haiku: 'glm-5.3-flash', opus: 'glm-5.4', sonnet: 'glm-5.3-flash',
          })
        } else if (provider === 'kimi' && channelType === 'messages') {
          expect(form.modelMapping).toEqual({
            fable: 'kimi-k3.1-pro', haiku: 'kimi-for-coding', opus: 'kimi-k3.1-pro', sonnet: 'kimi-k3.1-pro',
          })
        } else {
          expect(Object.values(form.modelMapping)).toEqual(Array(channelType === 'responses' ? 5 : 4).fill(target))
        }
        if (channelType === 'messages') {
          expect(form.reasoningMapping).toEqual({ fable: 'max', haiku: 'high', opus: 'max', sonnet: 'max' })
        }
        expect(form.serviceType).toBe(serviceType)
        expect(form.noVision).toBe(provider === 'glm')
        if (provider !== 'deepseek') expect(form.reasoningParamStyle).toBe('reasoning_effort')
        expect(sync).toHaveBeenCalledOnce()
      })
    }
  }
})
