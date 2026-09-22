<script setup lang="ts">
// 调度诊断对话框：dry-run 复现指定请求场景下的渠道选择过程。
// 移植自 web 端 SchedulerDiagnoseDialog.vue，按桌面端 shadcn 风格重写。
import { computed, ref, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Alert } from '@/components/ui/alert'
import { Checkbox } from '@/components/ui/checkbox'
import { Label } from '@/components/ui/label'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Loader2, Network } from 'lucide-vue-next'
import { useLanguage } from '@/composables/useLanguage'
import { useAdminApi } from '@/composables/useAdminApi'
import { schedulerDiagnosePath } from '@/services/admin-api'
import type { ChannelKind } from '@/services/admin-api'
import type { ManagedChannelType } from '@/utils/channel-type-api'

// SchedulerDiagnose 请求/响应类型（admin-api.ts 未收录，此处局部定义，与后端 handler 对齐）
interface SchedulerDiagnoseContextRequirement {
  inputTokens?: number
  outputTokens?: number
  requiredTokens?: number
  explicitOutputMax?: boolean
  skipWindowValidation?: boolean
}

interface SchedulerDiagnoseRequest {
  userId?: string
  model?: string
  routePrefix?: string
  channelName?: string
  failedChannels?: number[]
  hasImageContent?: boolean
  agentRole?: string
  contextRequirement?: SchedulerDiagnoseContextRequirement
}

interface SchedulerDiagnoseResponse {
  ok: boolean
  kind: ChannelKind
  reason?: string
  summary?: string
  error?: string
  selected?: {
    channelIndex: number
    channelName: string
    serviceType?: string
  }
  trace?: {
    kind: ChannelKind
    model?: string
    routePrefix?: string
    stages?: Array<{ name: string; count: number }>
    candidates?: Array<{
      channelIndex: number
      channelName: string
      stage: string
      reason: string
      details?: string
    }>
    selected?: {
      channelIndex: number
      channelName: string
      reason: string
    }
  }
}

interface Props {
  open: boolean
  channelType: ManagedChannelType
}

const props = defineProps<Props>()
const emit = defineEmits<{ (e: 'close'): void }>()

const { t } = useLanguage()
const adminApi = useAdminApi()

const model = ref('')
const userId = ref('')
const routePrefix = ref('')
const channelName = ref('')
const failedChannelsText = ref('')
const agentRole = ref('')
const hasImageContent = ref(false)
const explicitOutputMax = ref(false)
const skipWindowValidation = ref(false)
const inputTokens = ref('')
const outputTokens = ref('')
const requiredTokens = ref('')
const isRunning = ref(false)
const result = ref<SchedulerDiagnoseResponse | null>(null)

const hasTraceDetails = computed(() => Boolean(
  result.value?.summary
  || result.value?.trace?.stages?.length
  || result.value?.trace?.candidates?.length
))

const agentRoleOptions = computed(() => [
  { label: t('schedulerDiagnose.agentRoleDefault'), value: '' },
  { label: t('schedulerDiagnose.agentRoleMain'), value: 'main' },
  { label: t('schedulerDiagnose.agentRoleSubagent'), value: 'subagent' },
])

// 打开时清空上次结果，保持表单输入便于连续调试
watch(() => props.open, (open) => {
  if (!open) return
  result.value = null
})

function parseOptionalNumber(value: string): number | undefined {
  const trimmed = value.trim()
  if (!trimmed) return undefined
  const parsed = Number.parseInt(trimmed, 10)
  return Number.isFinite(parsed) && parsed > 0 ? parsed : undefined
}

function parseFailedChannels(): number[] {
  return failedChannelsText.value
    .split(/[\s,，]+/)
    .map(part => Number.parseInt(part, 10))
    .filter(index => Number.isInteger(index) && index >= 0)
}

function buildPayload(): SchedulerDiagnoseRequest {
  const input = parseOptionalNumber(inputTokens.value)
  const output = parseOptionalNumber(outputTokens.value)
  const required = parseOptionalNumber(requiredTokens.value)
  const hasContext = input !== undefined || output !== undefined || required !== undefined
    || explicitOutputMax.value || skipWindowValidation.value

  return {
    model: model.value.trim() || undefined,
    userId: userId.value.trim() || undefined,
    routePrefix: routePrefix.value.trim() || undefined,
    channelName: channelName.value.trim() || undefined,
    failedChannels: parseFailedChannels(),
    agentRole: agentRole.value || undefined,
    hasImageContent: hasImageContent.value,
    contextRequirement: hasContext
      ? {
          inputTokens: input,
          outputTokens: output,
          requiredTokens: required,
          explicitOutputMax: explicitOutputMax.value,
          skipWindowValidation: skipWindowValidation.value,
        }
      : undefined,
  }
}

async function runDiagnose() {
  isRunning.value = true
  try {
    result.value = await adminApi.post<SchedulerDiagnoseResponse>(
      schedulerDiagnosePath(props.channelType),
      buildPayload(),
    )
  } catch (err) {
    result.value = {
      ok: false,
      kind: props.channelType,
      error: err instanceof Error ? err.message : String(err),
    }
  } finally {
    isRunning.value = false
  }
}

function clearResult() {
  result.value = null
}

function handleClose(open: boolean) {
  if (!open) emit('close')
}
</script>

<template>
  <Dialog :open="open" @update:open="handleClose">
    <DialogContent class="max-h-[85vh] overflow-y-auto sm:max-w-[820px]">
      <DialogHeader>
        <DialogTitle class="flex items-center gap-2">
          <Network class="h-4 w-4 text-primary" />
          {{ t('schedulerDiagnose.title') }}
          <Badge variant="outline" class="font-mono text-[10px] uppercase">{{ channelType }}</Badge>
        </DialogTitle>
      </DialogHeader>

      <!-- 表单 -->
      <div class="grid grid-cols-1 gap-2.5 sm:grid-cols-2">
        <div class="space-y-1">
          <Label class="text-[10px] font-bold uppercase tracking-wider text-muted-foreground">{{ t('schedulerDiagnose.model') }}</Label>
          <Input v-model="model" class="h-8 font-mono text-xs" />
        </div>
        <div class="space-y-1">
          <Label class="text-[10px] font-bold uppercase tracking-wider text-muted-foreground">{{ t('schedulerDiagnose.userId') }}</Label>
          <Input v-model="userId" class="h-8 font-mono text-xs" />
        </div>
        <div class="space-y-1">
          <Label class="text-[10px] font-bold uppercase tracking-wider text-muted-foreground">{{ t('schedulerDiagnose.routePrefix') }}</Label>
          <Input v-model="routePrefix" class="h-8 font-mono text-xs" />
        </div>
        <div class="space-y-1">
          <Label class="text-[10px] font-bold uppercase tracking-wider text-muted-foreground">{{ t('schedulerDiagnose.channelName') }}</Label>
          <Input v-model="channelName" class="h-8 text-xs" />
        </div>
        <div class="space-y-1">
          <Label class="text-[10px] font-bold uppercase tracking-wider text-muted-foreground">{{ t('schedulerDiagnose.failedChannels') }}</Label>
          <Input v-model="failedChannelsText" class="h-8 font-mono text-xs" placeholder="0, 2" />
        </div>
        <div class="space-y-1">
          <Label class="text-[10px] font-bold uppercase tracking-wider text-muted-foreground">{{ t('schedulerDiagnose.agentRole') }}</Label>
          <Select v-model="agentRole">
            <SelectTrigger class="h-8 text-xs">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem v-for="opt in agentRoleOptions" :key="opt.value" :value="opt.value" class="text-xs">
                {{ opt.label }}
              </SelectItem>
            </SelectContent>
          </Select>
        </div>
        <div class="space-y-1">
          <Label class="text-[10px] font-bold uppercase tracking-wider text-muted-foreground">{{ t('schedulerDiagnose.inputTokens') }}</Label>
          <Input v-model="inputTokens" type="number" min="0" class="h-8 font-mono text-xs" />
        </div>
        <div class="space-y-1">
          <Label class="text-[10px] font-bold uppercase tracking-wider text-muted-foreground">{{ t('schedulerDiagnose.outputTokens') }}</Label>
          <Input v-model="outputTokens" type="number" min="0" class="h-8 font-mono text-xs" />
        </div>
        <div class="space-y-1">
          <Label class="text-[10px] font-bold uppercase tracking-wider text-muted-foreground">{{ t('schedulerDiagnose.requiredTokens') }}</Label>
          <Input v-model="requiredTokens" type="number" min="0" class="h-8 font-mono text-xs" />
        </div>
      </div>

      <div class="flex flex-wrap items-center gap-x-4 gap-y-2">
        <label class="flex cursor-pointer items-center gap-1.5 text-xs text-foreground">
          <Checkbox :checked="hasImageContent" @update:checked="hasImageContent = Boolean($event)" />
          {{ t('schedulerDiagnose.hasImageContent') }}
        </label>
        <label class="flex cursor-pointer items-center gap-1.5 text-xs text-foreground">
          <Checkbox :checked="explicitOutputMax" @update:checked="explicitOutputMax = Boolean($event)" />
          {{ t('schedulerDiagnose.explicitOutputMax') }}
        </label>
        <label class="flex cursor-pointer items-center gap-1.5 text-xs text-foreground">
          <Checkbox :checked="skipWindowValidation" @update:checked="skipWindowValidation = Boolean($event)" />
          {{ t('schedulerDiagnose.skipWindowValidation') }}
        </label>
      </div>

      <div class="flex items-center gap-2">
        <Button size="sm" :disabled="isRunning" @click="runDiagnose">
          <Loader2 v-if="isRunning" class="h-3.5 w-3.5 animate-spin" />
          <Network v-else class="h-3.5 w-3.5" />
          {{ t('schedulerDiagnose.run') }}
        </Button>
        <Button size="sm" variant="outline" :disabled="isRunning" @click="clearResult">
          {{ t('schedulerDiagnose.clear') }}
        </Button>
      </div>

      <Alert v-if="result?.ok === false" variant="destructive">
        <p class="text-sm">{{ result.error }}</p>
      </Alert>

      <!-- 结果区 -->
      <div v-if="result && hasTraceDetails" class="space-y-3 text-sm">
        <div v-if="result.ok" class="flex flex-wrap items-center gap-2">
          <Badge class="border-emerald-500/25 bg-emerald-500/10 font-normal text-emerald-700 dark:text-emerald-300">
            {{ t('schedulerDiagnose.selected') }} {{ result.selected?.channelIndex }}:{{ result.selected?.channelName }}
          </Badge>
          <Badge v-if="result.reason" variant="outline" class="font-normal text-muted-foreground">
            {{ result.reason }}
          </Badge>
        </div>

        <div v-if="result.summary" class="space-y-1">
          <div class="text-[10px] font-bold uppercase tracking-[0.18em] text-muted-foreground">{{ t('schedulerDiagnose.summary') }}</div>
          <pre class="max-h-48 overflow-auto rounded border border-border bg-muted/40 p-2.5 font-mono text-xs whitespace-pre-wrap break-all text-foreground">{{ result.summary }}</pre>
        </div>

        <div v-if="result.trace?.stages?.length" class="space-y-1">
          <div class="text-[10px] font-bold uppercase tracking-[0.18em] text-muted-foreground">{{ t('schedulerDiagnose.stages') }}</div>
          <div class="flex flex-wrap gap-1.5">
            <Badge
              v-for="stage in result.trace.stages"
              :key="stage.name"
              variant="outline"
              class="font-mono font-normal"
            >
              {{ stage.name }}: {{ stage.count }}
            </Badge>
          </div>
        </div>

        <div v-if="result.trace?.candidates?.length" class="space-y-1">
          <div class="text-[10px] font-bold uppercase tracking-[0.18em] text-muted-foreground">{{ t('schedulerDiagnose.candidates') }}</div>
          <div class="overflow-hidden rounded border border-border">
            <Table>
              <TableHeader>
                <TableRow class="hover:bg-transparent">
                  <TableHead>{{ t('schedulerDiagnose.channel') }}</TableHead>
                  <TableHead>{{ t('schedulerDiagnose.stage') }}</TableHead>
                  <TableHead>{{ t('schedulerDiagnose.reason') }}</TableHead>
                  <TableHead>{{ t('schedulerDiagnose.details') }}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                <TableRow
                  v-for="candidate in result.trace.candidates"
                  :key="`${candidate.stage}:${candidate.channelIndex}:${candidate.reason}`"
                >
                  <TableCell class="font-mono text-xs">{{ candidate.channelIndex }}:{{ candidate.channelName }}</TableCell>
                  <TableCell class="text-xs">{{ candidate.stage }}</TableCell>
                  <TableCell class="text-xs">{{ candidate.reason }}</TableCell>
                  <TableCell class="text-xs text-muted-foreground">{{ candidate.details }}</TableCell>
                </TableRow>
              </TableBody>
            </Table>
          </div>
        </div>
      </div>
    </DialogContent>
  </Dialog>
</template>
