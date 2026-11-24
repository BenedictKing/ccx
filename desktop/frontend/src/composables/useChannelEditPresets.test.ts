import { computed, reactive, ref } from 'vue'
import { describe, expect, it } from 'vitest'
import { useChannelEditPresets } from './useChannelEditPresets'
import type { ModelMappingRow } from './useChannelModelMapping'

type Options = Parameters<typeof useChannelEditPresets>[0]

function makeForm(serviceType: Options['form']['serviceType']): Options['form'] {
  return reactive({
    serviceType, fastMode: false, textVerbosity: '', passbackReasoningContent: false,
    passbackThinkingBlocks: false, stripEmptyTextBlocks: false, normalizeSystemRoleToTopLevel: false,
    normalizeMetadataUserId: false, stripBillingHeader: false, stripImageGenerationTool: false,
    noVision: false, visionFallbackModel: '', visionFallbackReasoningEffort: '', reasoningParamStyle: 'reasoning',
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
        const rows = ref<ModelMappingRow[]>([])
        let id = 0
        useChannelEditPresets({ form, channelType: () => channelType, modelMappingRows: rows,
          nextRowId: () => ++id, supportsOpenAIAdvanced: computed(() => serviceType === 'openai'),
        }).applyPreset(provider)
        const sources = channelType === 'responses'
          ? ['astra', 'codex-auto-review', 'luna', 'sol', 'terra'] : ['fable', 'haiku', 'opus', 'sonnet']
        expect(rows.value.map(row => row.source).sort()).toEqual(sources)
        if (provider === 'glm' && channelType === 'messages') {
          expect(Object.fromEntries(rows.value.map(row => [row.source, row.target]))).toEqual({
            fable: 'glm-5.4', haiku: 'glm-5.3-flash', opus: 'glm-5.4', sonnet: 'glm-5.3-flash',
          })
        } else if (provider === 'kimi' && channelType === 'messages') {
          expect(Object.fromEntries(rows.value.map(row => [row.source, row.target]))).toEqual({
            fable: 'kimi-k3.1-pro', haiku: 'kimi-for-coding', opus: 'kimi-k3.1-pro', sonnet: 'kimi-k3.1-pro',
          })
        } else {
          expect(rows.value.every(row => row.target === target)).toBe(true)
        }
        if (channelType === 'messages') {
          expect(Object.fromEntries(rows.value.map(row => [row.source, row.reasoning]))).toEqual({
            fable: 'max', haiku: 'high', opus: 'max', sonnet: provider === 'kimi' ? 'high' : 'max',
          })
        }
        expect(form.serviceType).toBe(serviceType)
        expect(form.noVision).toBe(provider === 'glm')
        if (provider !== 'deepseek') expect(form.reasoningParamStyle).toBe('reasoning_effort')
      })
    }
  }
})
