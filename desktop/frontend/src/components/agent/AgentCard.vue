<script setup lang="ts">
import { computed, ref, onMounted } from 'vue'
import { Card, CardContent, CardHeader } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Input } from '@/components/ui/input'
import { ExternalLink } from 'lucide-vue-next'
import { openProviderConsole, providerConsoleLinks } from '@/lib/external-link'
import { DetectEditors, OpenFileInEditor } from '@bindings/github.com/BenedictKing/ccx/desktop/desktopservice'
import ProviderForm from '@/components/agent/ProviderForm.vue'
import type { AgentPlatform, AgentConfigStatus, AgentProvider } from '@/types'
import { useLanguage } from '@/composables/useLanguage'

interface EditorInfo { id: string; name: string; path: string }

const props = defineProps<{
  platform: AgentPlatform
  agentStatus: AgentConfigStatus | null
  configLoading: boolean
  agentLabel: string
  agentStatusText: string
  agentStatusClass: string
  canApply: boolean
  selectedClaudeProvider?: AgentProvider
  claudeProviderKeys?: Record<AgentProvider, string>
  savedProviderKeys?: Record<string, string>
  claudeMimoBaseUrl?: string
  selectedMimoPlan?: string
  selectedMimoCodexPlan?: string
  selectedMimoOpenCodePlan?: string
  selectedDashScopePlan?: string
  claudeProviderLabel?: (value?: string) => string
  claudeTargetBaseUrl?: () => string
  selectedCodexProvider?: AgentProvider
  codexMode?: 'quick' | 'plugin'
  codexOpenAIKey?: string
  codexOpenAIUseOwnKey?: boolean
  codexProviderLabels?: Record<string, string>
  codexProviderLabel?: (value?: string) => string
  codexTargetBaseUrl?: () => string
  selectedOpenCodeProvider?: AgentProvider
  openCodeOpenAIKey?: string
  openCodeProviderLabels?: Record<string, string>
  openCodeProviderLabel?: (value?: string) => string
  openCodeTargetBaseUrl?: () => string
  migrateLoading?: boolean
  codexDiagnosticVisible?: boolean
  codexDiagnosticSeverity?: 'ok' | 'warn'
  codexDiagnosticSummary?: string
  codexDiagnosticSuggestions?: string[]
  responsesChannelDiagnosticVisible?: boolean
  responsesChannelDiagnosticSeverity?: 'ok' | 'warn'
  responsesChannelDiagnosticSummary?: string
  responsesChannelDiagnosticSuggestions?: string[]
  recentFailedLogsDiagnosticVisible?: boolean
  recentFailedLogsDiagnosticSeverity?: 'ok' | 'warn'
  recentFailedLogsDiagnosticSummary?: string
  recentFailedLogsDiagnosticSuggestions?: string[]
  codexTroubleshootingLoading?: boolean
}>()

const emit = defineEmits<{
  apply: []
  restore: []
  migrate: []
  troubleshoot: []
  'update:selectedClaudeProvider': [value: AgentProvider]
  'update:claudeProviderKeys': [value: Record<AgentProvider, string>]
  'update:claudeMimoBaseUrl': [value: string]
  'update:selectedMimoPlan': [value: string]
  'update:selectedMimoCodexPlan': [value: string]
  'update:selectedMimoOpenCodePlan': [value: string]
  'update:selectedDashScopePlan': [value: string]
  'update:selectedCodexProvider': [value: AgentProvider]
  'update:codexMode': [value: 'quick' | 'plugin']
  'update:codexOpenAIKey': [value: string]
  'update:codexOpenAIUseOwnKey': [value: boolean]
  'update:selectedOpenCodeProvider': [value: AgentProvider]
  'update:openCodeOpenAIKey': [value: string]
}>()

const { t } = useLanguage()

// OpenAI 直连「我有自己的 API Key」勾选状态（受控，默认不显示输入框）
const showCodexOwnKey = computed(() => props.codexOpenAIUseOwnKey ?? false)

const mimoCodexPlanOptions = [
  { label: t('agent.planPayAsYouGo'), value: 'https://api.xiaomimimo.com/v1' },
  { label: t('agent.planChina'), value: 'https://token-plan-cn.xiaomimimo.com/v1' },
  { label: t('agent.planSingapore'), value: 'https://token-plan-sgp.xiaomimimo.com/v1' },
  { label: t('agent.planEurope'), value: 'https://token-plan-ams.xiaomimimo.com/v1' },
]

const codexKeyRequired = computed(() => {
  const p = props.selectedCodexProvider
  return p !== 'ccx' && p !== 'openai'
})

// OpenAI 直连勾选「我有自己的 API Key」后必须输入；第三方 provider 始终必填
const codexKeyMandatory = computed(() =>
  codexKeyRequired.value || (props.selectedCodexProvider === 'openai' && showCodexOwnKey.value),
)

// API Key 输入框 placeholder：OpenAI 直连无可复用 saved key，勾选即必填
const codexKeyPlaceholder = computed(() => {
  if (props.selectedCodexProvider === 'openai') {
    return t('agent.codexPlaceholderRequired')
  }
  if (props.savedProviderKeys?.[`codex:${props.selectedCodexProvider}`]) {
    return t('agent.codexPlaceholderSaved')
  }
  return codexKeyRequired.value
    ? t('agent.codexPlaceholderRequired')
    : t('agent.codexPlaceholderWriteOnly')
})

// 支持快捷模式/插件模式切换的 provider 列表
const codexHasMode = computed(() => {
  const p = props.selectedCodexProvider
  return p === 'ccx' || p === 'deepseek' || p === 'mimo' || p === 'dashscope' || p === 'kimi' || p === 'glm' || p === 'minimax' || p === 'opencode-zen' || p === 'opencode-go' || p === 'xfyun' || p === 'tencent-lkeap' || p === 'volc-ark' || p === 'qianfan' || p === 'modelscope' || p === 'openrouter'
})

const badgeClass = computed(() => {
  if (props.agentStatusClass === 'running') return 'bg-accent text-accent-foreground border-0'
  if (props.agentStatusClass === 'starting') return 'bg-warning text-warning-foreground border-0'
  return 'bg-destructive text-destructive-foreground border-0'
})

const applyLabel = computed(() => t('agent.applyConfig'))

// 编辑器检测与文件打开
const editors = ref<EditorInfo[]>([])
const openingFile = ref('')

onMounted(async () => {
  try {
    editors.value = (await DetectEditors()) as EditorInfo[] ?? []
  } catch { editors.value = [] }
})

const openFileInEditor = async (editorPath: string, filePath: string) => {
  if (!editorPath || !filePath) return
  openingFile.value = filePath
  try {
    await OpenFileInEditor(editorPath, filePath)
  } catch { /* ignore */ }
  finally { openingFile.value = '' }
}
</script>

<template>
  <Card>
    <CardHeader class="pb-3">
      <div class="flex items-center justify-between">
        <div>
          <p class="text-xs text-muted-foreground uppercase tracking-widest">{{ platform }}</p>
          <h3 class="text-base font-semibold mt-1">{{ agentLabel }}</h3>
        </div>
        <Badge :class="badgeClass">{{ agentStatusText }}</Badge>
      </div>
    </CardHeader>

    <CardContent class="space-y-4">
      <div class="space-y-2 text-sm">
        <div v-for="detail in [
          { label: t('agent.currentProvider'), value: (platform === 'codex' ? codexProviderLabel : platform === 'opencode' ? openCodeProviderLabel : claudeProviderLabel)?.(agentStatus?.provider) || agentStatus?.provider || t('agent.notSet') },
          { label: t('agent.currentUrl'), value: agentStatus?.currentBaseUrl || t('agent.notSet') },
          { label: t('agent.targetUrl'), value: platform === 'claude' ? claudeTargetBaseUrl?.() : platform === 'opencode' ? openCodeTargetBaseUrl?.() : codexTargetBaseUrl?.() },
        ]" :key="detail.label" class="grid grid-cols-[8rem_minmax(0,1fr)] items-center gap-3">
          <span class="text-muted-foreground">{{ detail.label }}</span>
          <div class="min-w-0 text-right">
            <code class="inline-block max-w-full rounded bg-secondary px-2 py-0.5 text-right text-xs break-all">{{ detail.value }}</code>
          </div>
        </div>
        <!-- 配置文件 — 带编辑器打开按钮 -->
        <div class="grid grid-cols-[8rem_minmax(0,1fr)] items-center gap-3">
          <span class="text-muted-foreground">{{ t('agent.configPath') }}</span>
          <div class="flex min-w-0 items-center justify-end gap-2">
            <code class="inline-block min-w-0 max-w-full rounded bg-secondary px-2 py-0.5 text-right text-xs break-all">{{ agentStatus?.configPath || '--' }}</code>
            <div v-if="agentStatus?.configPath && editors.length > 0" class="relative shrink-0">
              <Button
                variant="ghost"
                size="icon-sm"
                :title="t('agent.openFileInEditor')"
                :disabled="openingFile === agentStatus.configPath"
                @click="editors.length === 1 && openFileInEditor(editors[0].path, agentStatus.configPath)"
              >
                <ExternalLink class="w-3.5 h-3.5" />
              </Button>
              <select
                v-if="editors.length > 1"
                class="absolute inset-0 w-full h-full opacity-0 cursor-pointer"
                :disabled="openingFile === agentStatus.configPath"
                @change="($event.target as HTMLSelectElement).value && openFileInEditor(($event.target as HTMLSelectElement).value, agentStatus!.configPath!); ($event.target as HTMLSelectElement).selectedIndex = 0"
              >
                <option value="" disabled selected></option>
                <option v-for="ed in editors" :key="ed.id" :value="ed.path">{{ ed.name }}</option>
              </select>
            </div>
          </div>
        </div>
        <!-- 认证文件 — 带编辑器打开按钮 -->
        <div v-if="agentStatus?.authPath" class="grid grid-cols-[8rem_minmax(0,1fr)] items-center gap-3">
          <span class="text-muted-foreground">{{ t('agent.authPath') }}</span>
          <div class="flex min-w-0 items-center justify-end gap-2">
            <code class="inline-block min-w-0 max-w-full rounded bg-secondary px-2 py-0.5 text-right text-xs break-all">{{ agentStatus.authPath }}</code>
            <div v-if="editors.length > 0" class="relative shrink-0">
              <Button
                variant="ghost"
                size="icon-sm"
                :title="t('agent.openFileInEditor')"
                :disabled="openingFile === agentStatus.authPath"
                @click="editors.length === 1 && openFileInEditor(editors[0].path, agentStatus.authPath!)"
              >
                <ExternalLink class="w-3.5 h-3.5" />
              </Button>
              <select
                v-if="editors.length > 1"
                class="absolute inset-0 w-full h-full opacity-0 cursor-pointer"
                :disabled="openingFile === agentStatus.authPath"
                @change="($event.target as HTMLSelectElement).value && openFileInEditor(($event.target as HTMLSelectElement).value, agentStatus!.authPath!); ($event.target as HTMLSelectElement).selectedIndex = 0"
              >
                <option value="" disabled selected></option>
                <option v-for="ed in editors" :key="ed.id" :value="ed.path">{{ ed.name }}</option>
              </select>
            </div>
          </div>
        </div>
      </div>

      <ProviderForm
        v-if="platform === 'claude'"
        :selected-provider="selectedClaudeProvider!"
        :provider-keys="claudeProviderKeys!"
        :saved-provider-keys="savedProviderKeys || {}"
        :mimo-base-url="claudeMimoBaseUrl!"
        :selected-mimo-plan="selectedMimoPlan || 'https://api.xiaomimimo.com/anthropic'"
        :selected-dash-scope-plan="selectedDashScopePlan || 'https://dashscope.aliyuncs.com/apps/anthropic'"
        @update:selected-provider="emit('update:selectedClaudeProvider', $event)"
        @update:provider-keys="emit('update:claudeProviderKeys', $event)"
        @update:mimo-base-url="emit('update:claudeMimoBaseUrl', $event)"
        @update:selected-mimo-plan="emit('update:selectedMimoPlan', $event)"
        @update:selected-dash-scope-plan="emit('update:selectedDashScopePlan', $event)"
      />
      <div v-else-if="platform === 'codex'" class="space-y-3">
        <div class="space-y-1.5">
          <Label class="text-xs text-muted-foreground">Provider</Label>
          <select
            :value="selectedCodexProvider"
            class="w-full h-9 rounded-md border border-input bg-background px-3 py-1 text-sm shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
            @change="emit('update:codexOpenAIUseOwnKey', false); emit('update:codexOpenAIKey', ''); emit('update:selectedCodexProvider', ($event.target as HTMLSelectElement).value as AgentProvider)"
          >
            <option value="ccx">{{ t('agent.provider.localGateway') }}</option>
            <option value="openai">{{ t('agent.provider.openaiDirect') }}</option>
            <option value="deepseek">{{ t('agent.provider.deepseekDirect') }}</option>
            <option value="mimo">{{ t('agent.provider.mimoDirect') }}</option>
            <option value="kimi">{{ t('agent.provider.kimiDirect') }}</option>
            <option value="glm">{{ t('agent.provider.glmDirect') }}</option>
            <option value="minimax">{{ t('agent.provider.minimaxDirect') }}</option>
            <option value="dashscope">{{ t('agent.provider.dashscopeDirect') }}</option>
            <option value="openrouter">{{ t('agent.provider.openrouterDirect') }}</option>
            <option value="modelscope">{{ t('agent.provider.modelscopeDirect') }}</option>
            <option value="xfyun">{{ t('agent.provider.xfyunDirect') }}</option>
            <option value="opencode-zen">{{ t('agent.provider.opencodeZenDirect') }}</option>
            <option value="opencode-go">{{ t('agent.provider.opencodeGoDirect') }}</option>
            <option value="tencent-lkeap">{{ t('agent.provider.tencentLkeapDirect') }}</option>
            <option value="volc-ark">{{ t('agent.provider.volcArkDirect') }}</option>
            <option value="qianfan">{{ t('agent.provider.qianfanDirect') }}</option>
          </select>
        </div>
        <div v-if="selectedCodexProvider === 'mimo'" class="space-y-1.5">
          <Label class="text-xs text-muted-foreground">{{ t('agent.billingModeMiMo') }}</Label>
          <select
            :value="selectedMimoCodexPlan || 'https://api.xiaomimimo.com/v1'"
            class="w-full h-9 rounded-md border border-input bg-background px-3 py-1 text-sm shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
            @change="emit('update:selectedMimoCodexPlan', ($event.target as HTMLSelectElement).value)"
          >
            <option
              v-for="opt in mimoCodexPlanOptions"
              :key="opt.value"
              :value="opt.value"
            >
              {{ opt.label }}
            </option>
          </select>
        </div>
        <div v-if="codexHasMode" class="space-y-2">
          <Label class="text-xs text-muted-foreground">{{ t('agent.codexMode') }}</Label>
          <div class="grid grid-cols-2 gap-2 rounded-lg bg-secondary/40 p-1">
            <button
              type="button"
              class="rounded-md px-3 py-1.5 text-xs font-medium transition-colors"
              :class="codexMode === 'quick' ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'"
              @click="emit('update:codexMode', 'quick')"
            >
              {{ t('agent.codexQuickMode') }}
            </button>
            <button
              type="button"
              class="rounded-md px-3 py-1.5 text-xs font-medium transition-colors"
              :class="codexMode === 'plugin' ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'"
              @click="emit('update:codexMode', 'plugin')"
            >
              {{ t('agent.codexPluginMode') }}
            </button>
          </div>
          <p class="text-xs text-muted-foreground leading-relaxed">
            {{ codexMode === 'plugin' ? t('agent.codexPluginModeHint') : t('agent.codexQuickModeHint') }}
          </p>
        </div>
        <div
          v-if="selectedCodexProvider && selectedCodexProvider !== 'ccx' && providerConsoleLinks[selectedCodexProvider]"
          class="inline-flex items-center gap-2"
        >
          <button
            type="button"
            class="inline-flex items-center gap-1.5 text-xs font-medium text-muted-foreground hover:text-foreground"
            @click="openProviderConsole(selectedCodexProvider)"
          >
            {{ t('agent.openConsole') }}
            <ExternalLink class="h-3 w-3" />
          </button>
          <span v-if="selectedCodexProvider === 'openai'" class="text-muted-foreground">|</span>
          <label
            v-if="selectedCodexProvider === 'openai'"
            class="inline-flex items-center gap-1 text-xs text-muted-foreground select-none cursor-pointer"
          >
            <input
              type="checkbox"
              class="h-3 w-3 rounded border-input accent-primary cursor-pointer"
              :checked="showCodexOwnKey"
              @change="emit('update:codexOpenAIUseOwnKey', ($event.target as HTMLInputElement).checked); if (!($event.target as HTMLInputElement).checked) emit('update:codexOpenAIKey', '')"
            >
            {{ t('agent.hasOwnApiKey') }}
          </label>
        </div>
        <div v-if="selectedCodexProvider !== 'ccx' && (codexKeyRequired || showCodexOwnKey)" class="space-y-1.5">
          <Label class="text-xs text-muted-foreground">API Key <span v-if="codexKeyMandatory" class="text-destructive">*</span></Label>
          <Input
            type="password"
            autocomplete="off"
            :placeholder="codexKeyPlaceholder"
            :model-value="codexOpenAIKey || ''"
            @update:model-value="emit('update:codexOpenAIKey', String($event))"
          />
        </div>
      </div>

      <div v-else-if="platform === 'opencode'" class="space-y-3">
        <div class="space-y-1.5">
          <Label class="text-xs text-muted-foreground">Provider</Label>
          <select
            :value="selectedOpenCodeProvider"
            class="w-full h-9 rounded-md border border-input bg-background px-3 py-1 text-sm shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
            @change="emit('update:selectedOpenCodeProvider', ($event.target as HTMLSelectElement).value as AgentProvider)"
          >
            <option value="ccx">{{ t('agent.provider.localGateway') }}</option>
            <option value="deepseek">{{ t('agent.provider.deepseekDirect') }}</option>
            <option value="mimo">{{ t('agent.provider.mimoDirect') }}</option>
            <option value="kimi">{{ t('agent.provider.kimiDirect') }}</option>
            <option value="glm">{{ t('agent.provider.glmDirect') }}</option>
            <option value="sensenova">{{ t('agent.provider.sensenovaDirect') }}</option>
            <option value="minimax">{{ t('agent.provider.minimaxDirect') }}</option>
            <option value="dashscope">{{ t('agent.provider.dashscopeDirect') }}</option>
            <option value="xfyun">{{ t('agent.provider.xfyunDirect') }}</option>
            <option value="openrouter">{{ t('agent.provider.openrouterDirect') }}</option>
            <option value="modelscope">{{ t('agent.provider.modelscopeDirect') }}</option>
            <option value="opencode-zen">{{ t('agent.provider.opencodeZenDirect') }}</option>
            <option value="opencode-go">{{ t('agent.provider.opencodeGoDirect') }}</option>
            <option value="tencent-lkeap">{{ t('agent.provider.tencentLkeapDirect') }}</option>
            <option value="volc-ark">{{ t('agent.provider.volcArkDirect') }}</option>
            <option value="qianfan">{{ t('agent.provider.qianfanDirect') }}</option>
          </select>
        </div>
        <div v-if="selectedOpenCodeProvider === 'mimo'" class="space-y-1.5">
          <Label class="text-xs text-muted-foreground">{{ t('agent.billingModeMiMo') }}</Label>
          <select
            :value="selectedMimoOpenCodePlan || 'https://api.xiaomimimo.com/v1'"
            class="w-full h-9 rounded-md border border-input bg-background px-3 py-1 text-sm shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
            @change="emit('update:selectedMimoOpenCodePlan', ($event.target as HTMLSelectElement).value)"
          >
            <option
              v-for="opt in mimoCodexPlanOptions"
              :key="opt.value"
              :value="opt.value"
            >
              {{ opt.label }}
            </option>
          </select>
        </div>
        <button
          v-if="selectedOpenCodeProvider && selectedOpenCodeProvider !== 'ccx' && providerConsoleLinks[selectedOpenCodeProvider]"
          type="button"
          class="inline-flex items-center gap-1.5 text-xs font-medium text-muted-foreground hover:text-foreground"
          @click="openProviderConsole(selectedOpenCodeProvider)"
        >
          {{ t('agent.openConsole') }}
          <ExternalLink class="h-3 w-3" />
        </button>
        <div v-if="selectedOpenCodeProvider !== 'ccx'" class="space-y-1.5">
          <Label class="text-xs text-muted-foreground">API Key</Label>
          <Input
            type="password"
            autocomplete="off"
            :placeholder="savedProviderKeys?.[`codex:${selectedOpenCodeProvider}`] ? t('agent.codexPlaceholderSaved') : t('agent.codexPlaceholderRequired')"
            :model-value="openCodeOpenAIKey || ''"
            @update:model-value="emit('update:openCodeOpenAIKey', String($event))"
          />
        </div>
      </div>

      <p v-if="agentStatus?.lastError" class="text-sm text-destructive-foreground">{{ agentStatus.lastError }}</p>

      <div v-if="platform === 'codex' && (codexDiagnosticVisible || responsesChannelDiagnosticVisible || recentFailedLogsDiagnosticVisible)" class="rounded-lg border border-border/60 bg-secondary/20 px-3 py-3">
        <p class="text-xs text-muted-foreground leading-relaxed">{{ t('agent.codexDiagnosticIntro') }}</p>
      </div>

      <div
        v-if="platform === 'codex' && codexDiagnosticVisible"
        class="rounded-lg border px-3 py-3 space-y-2"
        :class="codexDiagnosticSeverity === 'warn'
          ? 'border-amber-500/30 bg-amber-500/8'
          : 'border-emerald-500/30 bg-emerald-500/8'"
      >
        <div class="flex items-center justify-between gap-2">
          <h4 class="text-sm font-semibold">{{ t('agent.codexDiagnosticLayerConfig') }}</h4>
        </div>
        <p class="text-xs text-muted-foreground leading-relaxed">{{ codexDiagnosticSummary }}</p>
        <div class="grid grid-cols-[8rem_minmax(0,1fr)] items-center gap-3 text-xs">
          <span class="text-muted-foreground">{{ t('agent.codexDiagnosticAuthMode') }}</span>
          <code class="inline-block max-w-full rounded bg-secondary px-2 py-0.5 text-right break-all">{{ agentStatus?.authMode || t('agent.notSet') }}</code>
        </div>
        <ul v-if="codexDiagnosticSuggestions && codexDiagnosticSuggestions.length > 0" class="space-y-1 pt-1">
          <li
            v-for="(suggestion, i) in codexDiagnosticSuggestions"
            :key="`codex-${i}`"
            class="text-xs text-muted-foreground flex items-start gap-1.5"
          >
            <span class="text-muted mt-px">-</span>
            <span>{{ suggestion }}</span>
          </li>
        </ul>
      </div>

      <div
        v-if="platform === 'codex' && responsesChannelDiagnosticVisible"
        class="rounded-lg border px-3 py-3 space-y-2"
        :class="responsesChannelDiagnosticSeverity === 'warn'
          ? 'border-cyan-500/30 bg-cyan-500/8'
          : 'border-emerald-500/30 bg-emerald-500/8'"
      >
        <div class="flex items-center justify-between gap-2">
          <h4 class="text-sm font-semibold">{{ t('agent.codexDiagnosticLayerChannels') }}</h4>
        </div>
        <p class="text-xs text-muted-foreground leading-relaxed">{{ responsesChannelDiagnosticSummary }}</p>
        <ul v-if="responsesChannelDiagnosticSuggestions && responsesChannelDiagnosticSuggestions.length > 0" class="space-y-1 pt-1">
          <li
            v-for="(suggestion, i) in responsesChannelDiagnosticSuggestions"
            :key="`responses-${i}`"
            class="text-xs text-muted-foreground flex items-start gap-1.5"
          >
            <span class="text-muted mt-px">-</span>
            <span>{{ suggestion }}</span>
          </li>
        </ul>
      </div>

      <div
        v-if="platform === 'codex' && recentFailedLogsDiagnosticVisible"
        class="rounded-lg border px-3 py-3 space-y-2"
        :class="recentFailedLogsDiagnosticSeverity === 'warn'
          ? 'border-rose-500/30 bg-rose-500/8'
          : 'border-emerald-500/30 bg-emerald-500/8'"
      >
        <div class="flex items-center justify-between gap-2">
          <h4 class="text-sm font-semibold">{{ t('agent.codexDiagnosticLayerLogs') }}</h4>
        </div>
        <p class="text-xs text-muted-foreground leading-relaxed">{{ recentFailedLogsDiagnosticSummary }}</p>
        <ul v-if="recentFailedLogsDiagnosticSuggestions && recentFailedLogsDiagnosticSuggestions.length > 0" class="space-y-1 pt-1">
          <li
            v-for="(suggestion, i) in recentFailedLogsDiagnosticSuggestions"
            :key="`logs-${i}`"
            class="text-xs text-muted-foreground flex items-start gap-1.5"
          >
            <span class="text-muted mt-px">-</span>
            <span>{{ suggestion }}</span>
          </li>
        </ul>
      </div>

      <div class="flex flex-wrap gap-2">
        <Button size="sm" :disabled="!canApply" @click="emit('apply')">
          {{ applyLabel }}
        </Button>
        <Button size="sm" variant="secondary" :disabled="configLoading || !agentStatus?.hasState" @click="emit('restore')">
          {{ t('agent.restoreConfig') }}
        </Button>
        <Button v-if="platform === 'codex'" size="sm" variant="secondary" :disabled="configLoading || codexTroubleshootingLoading" @click="emit('troubleshoot')">
          {{ codexTroubleshootingLoading ? t('agent.codexTroubleshooting') : t('agent.codexTroubleshoot') }}
        </Button>
        <Button v-if="platform === 'codex'" size="sm" variant="outline" :disabled="configLoading || migrateLoading" @click="emit('migrate')">
          {{ t('agent.migrateSessions') }}
        </Button>
      </div>
    </CardContent>
  </Card>
</template>
