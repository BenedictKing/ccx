<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import {
  AlertTriangle,
  ArrowRight,
  ChevronDown,
  ChevronRight,
  GitBranch,
  Loader2,
  Minus,
  Play,
  Radar,
  ScanSearch,
  Star,
} from 'lucide-vue-next'
import { Alert } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Textarea } from '@/components/ui/textarea'
import { useAdminApi } from '@/composables/useAdminApi'
import { useLanguage } from '@/composables/useLanguage'
import { getChannelTypeApi } from '@/utils/channel-type-api'
import {
  ROUTE_PREVIEW_PATH,
  SMART_ROUTING_DIAGNOSE_PATH,
} from '@/services/admin-api'
import type {
  Channel,
  RoutePreviewResponse,
  SmartRoutingDiagnoseChannelKind,
  SmartRoutingDiagnoseProfile,
  SmartRoutingDiagnoseRequest,
  SmartRoutingDiagnoseResponse,
} from '@/services/admin-api'

type PreviewMode = 'manual' | 'body'

interface DiagnoseForm {
  model: string
  channelKind: SmartRoutingDiagnoseChannelKind
  agentRole: 'main' | 'subagent' | ''
  estTokens: number
  toolUseNeed: boolean
  reasoningNeed: boolean
  hasImage: boolean
}

interface PreviewForm {
  channelKind: SmartRoutingDiagnoseChannelKind
  model: string
  operation: string
  bodyText: string
}

const { t } = useLanguage()
const api = useAdminApi()

// 预览模式：手工填写 / 请求体预演
const previewMode = ref<PreviewMode>('manual')

const featuredModelPresets = [
  'claude-opus-5',
  'claude-fable-5',
  'claude-sonnet-5',
  'gpt-5.6-sol',
  'gpt-5.6-terra',
  'gpt-5.6-luna',
  'gemini-3.7-flash',
  'gemini-3.6-flash',
  'gemini-3.5-flash',
  'gemini-3.1-pro',
  'grok-4.5',
  'glm-5.3',
  'glm-5.2',
  'kimi-k3',
  'kimi-k2.7-code',
  'deepseek-v4-pro',
  'deepseek-v4-flash',
  'qwen3.8-max',
  'qwen3.7-max',
  'qwen3-max',
  'minimax-m3',
  'mimo-v2.5-pro',
  'mimo-v2.5',
]
const modelPresets = featuredModelPresets

// 手工填写模式表单
const form = reactive<DiagnoseForm>({
  model: modelPresets[0],
  channelKind: 'messages',
  agentRole: 'main',
  estTokens: 20_000,
  toolUseNeed: true,
  reasoningNeed: true,
  hasImage: false,
})

// 请求体预演模式表单
const previewForm = reactive<PreviewForm>({
  channelKind: 'messages',
  model: '',
  operation: '',
  bodyText: JSON.stringify(
    {
      model: 'claude-opus-5',
      messages: [
        { role: 'user', content: 'Hello, please analyze this problem step by step.' },
      ],
      tools: [
        {
          name: 'example_tool',
          description: 'An example tool',
          input_schema: { type: 'object', properties: {} },
        },
      ],
    },
    null,
    2,
  ),
})

const loading = ref(false)
const error = ref('')

// 两种模式的响应数据
const manualResponse = ref<SmartRoutingDiagnoseResponse | null>(null)
const previewResponse = ref<RoutePreviewResponse | null>(null)

const channelNamesByUid = ref(new Map<string, string>())

const channelKinds: SmartRoutingDiagnoseChannelKind[] = ['messages', 'chat', 'responses', 'gemini', 'images', 'vectors']

const completionFeaturesEnabled = computed(() => (
  form.channelKind !== 'images' && form.channelKind !== 'vectors'
))

const agentRoleItems = computed(() => [
  { value: '', label: t('autopilot.diagnose.role.auto') },
  { value: 'main', label: t('autopilot.diagnose.role.main') },
  { value: 'subagent', label: t('autopilot.diagnose.role.subagent') },
])

const operationLabel = computed(() => {
  const kind = previewForm.channelKind
  if (kind === 'images') return 'Operation (image_generation / image_edit / image_variation)'
  if (kind === 'vectors') return 'Operation (embedding)'
  return 'Operation (completion / count_tokens / summarize)'
})

// 根据当前预览模式返回 plan / profile / candidates 等
const displayPlan = computed(() => {
  if (previewMode.value === 'manual') return manualResponse.value !== null
  return previewResponse.value !== null
})

const plan = computed(() => {
  if (previewMode.value === 'manual') return manualResponse.value?.plan ?? null
  return previewResponse.value?.plan ?? null
})

const profile = computed<SmartRoutingDiagnoseProfile | null | undefined>(() => {
  return plan.value?.requestProfile
})

const extractedProfile = computed<SmartRoutingDiagnoseProfile | null | undefined>(() => {
  if (previewMode.value === 'body') return previewResponse.value?.extractedProfile
  return undefined
})

const schedulerDiagnose = computed(() => {
  if (previewMode.value === 'body') return previewResponse.value?.schedulerDiagnose
  return undefined
})

const schedulerTrace = computed(() => schedulerDiagnose.value?.trace ?? { stages: [], candidates: [] })

const responseMode = computed(() => {
  if (previewMode.value === 'manual') return manualResponse.value?.mode ?? ''
  return previewResponse.value?.mode ?? ''
})

const responseMessage = computed(() => {
  if (previewMode.value === 'manual') return manualResponse.value?.message ?? ''
  return previewResponse.value?.message ?? ''
})

const candidates = computed(() => plan.value?.candidates ?? [])
const eligibleCount = computed(() => candidates.value.filter(candidate => candidate.selected).length)
const selectedCandidate = computed(() => candidates.value.find(
  candidate => candidate.channelUid === plan.value?.selectedChannelUid,
))

// 折叠区展开状态（无 collapsible 组件，用简单切换）
const extractedExpanded = ref(false)
const schedulerExpanded = ref(false)

function operationFor(kind: SmartRoutingDiagnoseChannelKind): string {
  if (kind === 'images') return 'image_generation'
  if (kind === 'vectors') return 'embedding'
  return 'completion'
}

async function loadChannelNames() {
  const responses = await Promise.all(
    channelKinds.map(kind => getChannelTypeApi(kind).getChannels()),
  )
  const names = new Map<string, string>()
  for (const resp of responses) {
    for (const channel of (resp.channels ?? []) as Channel[]) {
      if (channel.channelUid) names.set(channel.channelUid, channel.name)
    }
  }
  channelNamesByUid.value = names
}

// 挂载时预建渠道名映射，提交时刷新兜底（新建渠道也能映射）
onMounted(() => {
  void loadChannelNames()
})

// 手工填写模式：运行诊断
async function runDiagnose(model?: string) {
  if (model) form.model = model
  const requestedModel = String(form.model ?? '').trim()
  if (!requestedModel) {
    error.value = t('autopilot.diagnose.modelRequired')
    return
  }

  loading.value = true
  error.value = ''
  try {
    await loadChannelNames()
    const request: SmartRoutingDiagnoseRequest = {
      model: requestedModel,
      channelKind: form.channelKind,
      operation: operationFor(form.channelKind),
      agentRole: form.agentRole,
      estTokens: Math.max(0, Number(form.estTokens) || 0),
      hasImage: completionFeaturesEnabled.value && form.hasImage,
      visionNeed: completionFeaturesEnabled.value && form.hasImage,
      imageGenNeed: form.channelKind === 'images',
      embeddingNeed: form.channelKind === 'vectors',
      toolUseNeed: completionFeaturesEnabled.value && form.toolUseNeed,
      reasoningNeed: completionFeaturesEnabled.value && form.reasoningNeed,
    }
    manualResponse.value = await api.post<SmartRoutingDiagnoseResponse>(SMART_ROUTING_DIAGNOSE_PATH, request)
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : t('autopilot.diagnose.failed')
  } finally {
    loading.value = false
  }
}

// 请求体预演模式：运行预演
async function runBodyPreview() {
  const trimmed = previewForm.bodyText.trim()
  if (!trimmed) {
    error.value = t('autopilot.diagnose.preview.bodyLabel') + ' 不能为空。'
    return
  }

  let parsedBody: Record<string, unknown>
  try {
    parsedBody = JSON.parse(trimmed)
  } catch {
    error.value = '请求体 JSON 解析失败，请检查格式。'
    return
  }

  if (!parsedBody || typeof parsedBody !== 'object' || Array.isArray(parsedBody)) {
    error.value = '请求体必须是 JSON 对象。'
    return
  }

  loading.value = true
  error.value = ''
  try {
    await loadChannelNames()
    previewResponse.value = await api.post<RoutePreviewResponse>(ROUTE_PREVIEW_PATH, {
      channelKind: previewForm.channelKind,
      model: previewForm.model.trim() || undefined,
      operation: previewForm.operation.trim() || undefined,
      body: parsedBody,
    })
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : t('autopilot.diagnose.preview.failed')
  } finally {
    loading.value = false
  }
}

function shortenUid(uid?: string): string {
  if (!uid) return '-'
  const stripped = uid.replace(/^ch_/, '')
  return stripped.length > 12 ? `ch_${stripped.slice(0, 12)}…` : uid
}

function channelName(uid?: string): string {
  if (!uid) return '-'
  return channelNamesByUid.value.get(uid) ?? shortenUid(uid)
}

function formatScore(score: number): string {
  return Number.isFinite(score) ? score.toFixed(3) : '-'
}

function mappingSourceLabel(source?: string): string {
  if (source === 'explicit_mapping') return t('autopilot.diagnose.mapping.explicit')
  if (source === 'auto_resolve_preview') return t('autopilot.diagnose.mapping.preview')
  return t('autopilot.diagnose.mapping.original')
}

function mappingSourceClass(source?: string): string {
  if (source === 'auto_resolve_preview') return 'border-amber-500/40 bg-amber-500/10 text-amber-700 dark:text-amber-300'
  if (source === 'explicit_mapping') return 'border-cyan-500/40 bg-cyan-500/10 text-cyan-700 dark:text-cyan-300'
  return 'border-muted-foreground/40 bg-muted/30 text-muted-foreground'
}

function switchMode(mode: PreviewMode) {
  previewMode.value = mode
  error.value = ''
}
</script>

<template>
  <div class="rounded-xl border border-border/60 bg-card/40 p-4">
    <div class="mb-3 flex flex-wrap items-center justify-between gap-2">
      <h4 class="flex items-center gap-2 text-sm font-bold">
        <Radar class="size-4 text-primary" />
        {{ t('autopilot.diagnose.title') }}
      </h4>
      <div class="flex items-center gap-1">
        <Button
          size="sm"
          :variant="previewMode === 'manual' ? 'default' : 'outline'"
          @click="switchMode('manual')"
        >
          {{ t('autopilot.diagnose.modeTab.manual') }}
        </Button>
        <Button
          size="sm"
          :variant="previewMode === 'body' ? 'default' : 'outline'"
          @click="switchMode('body')"
        >
          {{ t('autopilot.diagnose.modeTab.bodyPreview') }}
        </Button>
      </div>
    </div>

    <!-- 手工填写模式 -->
    <template v-if="previewMode === 'manual'">
      <Alert class="mb-4">
        <div class="text-xs text-muted-foreground">
          {{ t('autopilot.diagnose.hint') }}
        </div>
      </Alert>

      <div class="mb-3 grid grid-cols-1 gap-3 md:grid-cols-3 lg:grid-cols-5">
        <div>
          <div class="mb-1 text-xs text-muted-foreground">{{ t('autopilot.diagnose.channelKind') }}</div>
          <Select v-model="form.channelKind">
            <SelectTrigger class="h-9 w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem v-for="kind in channelKinds" :key="kind" :value="kind">
                {{ t(`autopilot.diagnose.kind.${kind}`) }}
              </SelectItem>
            </SelectContent>
          </Select>
        </div>
        <div>
          <div class="mb-1 text-xs text-muted-foreground">{{ t('autopilot.diagnose.model') }}</div>
          <Input v-model="form.model" list="autopilot-diagnose-model-presets" class="h-9" />
          <datalist id="autopilot-diagnose-model-presets">
            <option v-for="model in modelPresets" :key="model" :value="model" />
          </datalist>
        </div>
        <div>
          <div class="mb-1 text-xs text-muted-foreground">{{ t('autopilot.diagnose.agentRole') }}</div>
          <Select v-model="form.agentRole">
            <SelectTrigger class="h-9 w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem v-for="item in agentRoleItems" :key="item.value" :value="item.value">
                {{ item.label }}
              </SelectItem>
            </SelectContent>
          </Select>
        </div>
        <div>
          <div class="mb-1 text-xs text-muted-foreground">{{ t('autopilot.diagnose.estTokens') }}</div>
          <Input v-model.number="form.estTokens" type="number" min="0" class="h-9" />
        </div>
        <div class="flex items-end gap-4 pb-1">
          <label class="flex items-center gap-2 text-xs" :class="{ 'opacity-50': !completionFeaturesEnabled }">
            <Switch
              :model-value="completionFeaturesEnabled && form.toolUseNeed"
              :disabled="!completionFeaturesEnabled"
              @update:model-value="(v) => (form.toolUseNeed = Boolean(v))"
            />
            {{ t('autopilot.diagnose.toolUse') }}
          </label>
          <label class="flex items-center gap-2 text-xs" :class="{ 'opacity-50': !completionFeaturesEnabled }">
            <Switch
              :model-value="completionFeaturesEnabled && form.reasoningNeed"
              :disabled="!completionFeaturesEnabled"
              @update:model-value="(v) => (form.reasoningNeed = Boolean(v))"
            />
            {{ t('autopilot.diagnose.reasoning') }}
          </label>
          <label class="flex items-center gap-2 text-xs" :class="{ 'opacity-50': !completionFeaturesEnabled }">
            <Switch
              :model-value="completionFeaturesEnabled && form.hasImage"
              :disabled="!completionFeaturesEnabled"
              @update:model-value="(v) => (form.hasImage = Boolean(v))"
            />
            {{ t('autopilot.diagnose.hasImage') }}
          </label>
        </div>
      </div>

      <div class="flex flex-wrap items-center gap-2">
        <span class="mr-1 text-xs text-muted-foreground">{{ t('autopilot.diagnose.quickModels') }}</span>
        <Button
          v-for="model in modelPresets"
          :key="model"
          size="sm"
          variant="secondary"
          :disabled="loading"
          @click="runDiagnose(model)"
        >
          {{ model }}
        </Button>
        <div class="flex-1" />
        <Button size="sm" :disabled="loading" @click="runDiagnose()">
          <Loader2 v-if="loading" class="size-4 animate-spin" />
          <Play v-else class="size-4" />
          {{ t('autopilot.diagnose.run') }}
        </Button>
      </div>
    </template>

    <!-- 请求体预演模式 -->
    <template v-else>
      <Alert class="mb-4">
        <div class="text-xs text-muted-foreground">
          {{ t('autopilot.diagnose.preview.hint') }}
        </div>
      </Alert>

      <div class="mb-3 grid grid-cols-1 gap-3 md:grid-cols-3">
        <div>
          <div class="mb-1 text-xs text-muted-foreground">{{ t('autopilot.diagnose.channelKind') }}</div>
          <Select v-model="previewForm.channelKind">
            <SelectTrigger class="h-9 w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem v-for="kind in channelKinds" :key="kind" :value="kind">
                {{ t(`autopilot.diagnose.kind.${kind}`) }}
              </SelectItem>
            </SelectContent>
          </Select>
        </div>
        <div>
          <div class="mb-1 text-xs text-muted-foreground">{{ t('autopilot.diagnose.model') }}</div>
          <Input v-model="previewForm.model" list="autopilot-diagnose-model-presets" class="h-9" />
        </div>
        <div>
          <div class="mb-1 text-xs text-muted-foreground">{{ operationLabel }}</div>
          <Input v-model="previewForm.operation" class="h-9" />
        </div>
      </div>

      <Textarea
        v-model="previewForm.bodyText"
        :placeholder="t('autopilot.diagnose.preview.bodyPlaceholder')"
        class="min-h-[220px] font-mono text-xs"
      />

      <div class="mt-3 flex justify-end">
        <Button size="sm" :disabled="loading" @click="runBodyPreview">
          <Loader2 v-if="loading" class="size-4 animate-spin" />
          <Play v-else class="size-4" />
          {{ t('autopilot.diagnose.run') }}
        </Button>
      </div>
    </template>

    <Alert v-if="error" variant="destructive" class="mt-4">
      <AlertTriangle class="size-4" />
      <div class="text-xs">{{ error }}</div>
    </Alert>

    <!-- 结果区 -->
    <template v-if="displayPlan">
      <div class="my-5 border-t border-border/60" />

      <!-- 提取的特征（仅请求体预演模式） -->
      <div v-if="previewMode === 'body' && extractedProfile" class="mb-4">
        <button
          class="flex items-center gap-1 text-xs font-medium text-muted-foreground hover:text-foreground"
          @click="extractedExpanded = !extractedExpanded"
        >
          <component :is="extractedExpanded ? ChevronDown : ChevronRight" class="size-3.5" />
          <ScanSearch class="size-3.5" />
          {{ t('autopilot.diagnose.preview.extracted') }}
        </button>
        <div v-if="extractedExpanded" class="mt-2 flex flex-wrap items-center gap-2">
          <Badge variant="outline">{{ t('autopilot.diagnose.taskClass') }}: {{ extractedProfile.TaskClass || '-' }}</Badge>
          <Badge variant="outline">{{ t('autopilot.diagnose.qualityNeed') }}: {{ extractedProfile.QualityNeed || '-' }}</Badge>
          <Badge variant="secondary">{{ t('autopilot.diagnose.estTokens') }}: {{ extractedProfile.EstTokens ?? 0 }}</Badge>
          <Badge v-if="extractedProfile.ToolUseNeed" variant="secondary">
            {{ t('autopilot.diagnose.toolUse') }}
          </Badge>
          <Badge v-if="extractedProfile.ReasoningNeed" variant="secondary">
            {{ t('autopilot.diagnose.reasoning') }}
          </Badge>
          <Badge v-if="extractedProfile.VisionNeed || extractedProfile.HasImage" variant="outline" class="border-amber-500/40 text-amber-700 dark:text-amber-300">
            {{ t('autopilot.diagnose.hasImage') }}
          </Badge>
          <Badge variant="outline">model: {{ extractedProfile.Model || '-' }}</Badge>
          <Badge variant="outline">kind: {{ extractedProfile.ChannelKind || '-' }}</Badge>
          <Badge variant="outline">operation: {{ extractedProfile.Operation || '-' }}</Badge>
        </div>
      </div>

      <Alert v-if="!plan" class="border-amber-500/50 bg-amber-500/10 text-amber-700 dark:text-amber-300">
        <AlertTriangle class="size-4" />
        <div class="text-xs">
          {{ responseMessage || t('autopilot.diagnose.noPlan') }}
        </div>
      </Alert>

      <template v-else>
        <div class="mb-4 flex flex-wrap items-center gap-2">
          <Badge variant="secondary">{{ t('autopilot.diagnose.mode') }}: {{ responseMode }}</Badge>
          <Badge variant="secondary">{{ t('autopilot.diagnose.taskClass') }}: {{ profile?.TaskClass || '-' }}</Badge>
          <Badge variant="outline">{{ t('autopilot.diagnose.qualityNeed') }}: {{ profile?.QualityNeed || '-' }}</Badge>
          <Badge variant="outline">{{ t('autopilot.diagnose.candidates') }}: {{ candidates.length }}</Badge>
          <Badge variant="outline" class="border-emerald-500/40 text-emerald-700 dark:text-emerald-300">
            {{ t('autopilot.diagnose.eligible') }}: {{ eligibleCount }}
          </Badge>
          <Badge v-if="plan.fallbackUsed" variant="outline" class="border-amber-500/40 bg-amber-500/10 text-amber-700 dark:text-amber-300">
            {{ t('autopilot.diagnose.failOpen') }}
          </Badge>
        </div>

        <!-- 推荐卡 -->
        <div class="mb-4 rounded-lg border border-primary/30 bg-primary/5 p-3">
          <div class="text-xs text-muted-foreground">{{ t('autopilot.diagnose.recommendation') }}</div>
          <div class="mt-1 flex flex-wrap items-center gap-2">
            <span class="font-bold" :title="plan.selectedChannelUid || '-'">{{ channelName(plan.selectedChannelUid) }}</span>
            <ArrowRight class="size-3.5 text-muted-foreground" />
            <span class="font-bold">{{ plan.selectedModel || profile?.Model || '-' }}</span>
            <Badge
              v-if="selectedCandidate?.mappingSource"
              variant="outline"
              :class="mappingSourceClass(selectedCandidate.mappingSource)"
            >
              {{ mappingSourceLabel(selectedCandidate.mappingSource) }}
            </Badge>
          </div>
          <div v-if="selectedCandidate?.mappingReason" class="mt-1 text-xs text-muted-foreground">
            {{ selectedCandidate.mappingReason }}
          </div>
        </div>

        <div v-if="candidates.length === 0" class="py-6 text-center text-sm text-muted-foreground">
          {{ t('autopilot.diagnose.noCandidates') }}
        </div>

        <!-- 候选表：(渠道,模型) 粒度，selected 行高亮 -->
        <div v-else class="overflow-hidden rounded-lg border border-border/50">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead class="w-[40px]"></TableHead>
                <TableHead>{{ t('autopilot.diagnose.col.channel') }}</TableHead>
                <TableHead>{{ t('autopilot.diagnose.col.actualModel') }}</TableHead>
                <TableHead>{{ t('autopilot.diagnose.col.mapping') }}</TableHead>
                <TableHead class="text-right">{{ t('autopilot.diagnose.col.score') }}</TableHead>
                <TableHead>{{ t('autopilot.diagnose.col.constraint') }}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              <TableRow
                v-for="candidate in candidates"
                :key="candidate.candidateKey || candidate.channelUid"
                :class="candidate.selected ? 'bg-primary/5' : 'text-muted-foreground'"
              >
                <TableCell>
                  <Star v-if="candidate.channelUid === plan.selectedChannelUid" class="size-3.5 text-primary" />
                  <Minus v-else class="size-3 text-muted-foreground/50" />
                </TableCell>
                <TableCell class="text-xs" :title="candidate.channelUid">
                  {{ channelName(candidate.channelUid) }}
                </TableCell>
                <TableCell class="text-xs">{{ candidate.mappedModel || profile?.Model || '-' }}</TableCell>
                <TableCell>
                  <Badge variant="outline" :class="mappingSourceClass(candidate.mappingSource)">
                    {{ mappingSourceLabel(candidate.mappingSource) }}
                  </Badge>
                </TableCell>
                <TableCell class="text-right font-mono text-xs">{{ formatScore(candidate.score) }}</TableCell>
                <TableCell class="text-xs">
                  <Badge v-if="candidate.selected" variant="outline" class="border-emerald-500/40 text-emerald-700 dark:text-emerald-300">
                    {{ t('autopilot.diagnose.passed') }}
                  </Badge>
                  <span v-else>{{ candidate.filterReasons?.join('; ') || '-' }}</span>
                </TableCell>
              </TableRow>
            </TableBody>
          </Table>
        </div>

        <div v-if="plan.sortReasons?.length" class="mt-3 flex flex-wrap items-center gap-2">
          <span class="text-xs text-muted-foreground">{{ t('autopilot.traceTable.sortReasons') }}:</span>
          <Badge v-for="reason in plan.sortReasons" :key="reason" variant="outline">
            {{ reason }}
          </Badge>
        </div>

        <!-- 调度器追踪（请求体预演模式） -->
        <div v-if="previewMode === 'body' && schedulerDiagnose" class="mt-4">
          <button
            class="flex items-center gap-1 text-xs font-medium text-muted-foreground hover:text-foreground"
            @click="schedulerExpanded = !schedulerExpanded"
          >
            <component :is="schedulerExpanded ? ChevronDown : ChevronRight" class="size-3.5" />
            <GitBranch class="size-3.5" />
            {{ t('autopilot.diagnose.preview.schedulerTrace') }}
            <Badge
              variant="outline"
              :class="schedulerDiagnose.ok
                ? 'border-emerald-500/40 text-emerald-700 dark:text-emerald-300'
                : 'border-rose-500/40 text-rose-700 dark:text-rose-300'"
            >
              {{ schedulerDiagnose.ok ? 'OK' : 'ERROR' }}
            </Badge>
          </button>

          <div v-if="schedulerExpanded" class="mt-2 space-y-3 rounded-lg border border-border/50 p-3">
            <!-- 阶段进度 -->
            <div>
              <div class="mb-1 text-xs text-muted-foreground">{{ t('autopilot.diagnose.preview.schedulerStages') }}</div>
              <div class="flex flex-wrap items-center gap-1">
                <template v-for="(stage, idx) in schedulerTrace.stages" :key="stage.name">
                  <Badge variant="secondary">{{ stage.name }}: {{ stage.count }}</Badge>
                  <ChevronRight v-if="idx < (schedulerTrace.stages?.length ?? 0) - 1" class="size-3 text-muted-foreground" />
                </template>
              </div>
            </div>

            <!-- 最终选择 -->
            <div v-if="schedulerDiagnose.selected">
              <div class="mb-1 text-xs text-muted-foreground">{{ t('autopilot.diagnose.preview.schedulerSelected') }}</div>
              <div class="flex flex-wrap items-center gap-2">
                <Badge variant="outline" class="border-emerald-500/40 text-emerald-700 dark:text-emerald-300">
                  #{{ schedulerDiagnose.selected.channelIndex }} {{ schedulerDiagnose.selected.channelName }}
                  ({{ schedulerDiagnose.selected.serviceType }})
                </Badge>
                <span v-if="schedulerDiagnose.reason" class="text-xs text-muted-foreground">— {{ schedulerDiagnose.reason }}</span>
              </div>
            </div>

            <!-- 被跳过候选 -->
            <div v-if="schedulerTrace.candidates?.length">
              <div class="mb-1 text-xs text-muted-foreground">
                {{ t('autopilot.diagnose.preview.skippedCandidates') }} ({{ schedulerTrace.candidates.length }})
              </div>
              <div class="overflow-hidden rounded-lg border border-border/50">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead class="w-[40px]">#</TableHead>
                      <TableHead>{{ t('autopilot.diagnose.col.channel') }}</TableHead>
                      <TableHead>Stage</TableHead>
                      <TableHead>Reason</TableHead>
                      <TableHead>Details</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    <TableRow v-for="c in schedulerTrace.candidates" :key="`${c.channelIndex}-${c.stage}`" class="text-xs">
                      <TableCell>{{ c.channelIndex }}</TableCell>
                      <TableCell>{{ c.channelName || '-' }}</TableCell>
                      <TableCell>
                        <Badge variant="outline">{{ c.stage }}</Badge>
                      </TableCell>
                      <TableCell>{{ c.reason || '-' }}</TableCell>
                      <TableCell class="text-muted-foreground">{{ c.details || '-' }}</TableCell>
                    </TableRow>
                  </TableBody>
                </Table>
              </div>
            </div>
          </div>
        </div>
      </template>
    </template>
  </div>
</template>
