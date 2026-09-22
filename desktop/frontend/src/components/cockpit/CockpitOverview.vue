<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  AlertCircle,
  AlertTriangle,
  Banknote,
  FlaskConical,
  HeartPulse,
  HelpCircle,
  Lightbulb,
  Loader2,
  RefreshCw,
  Server,
  ShieldAlert,
  StopCircle,
  UserCheck,
  XCircle,
} from 'lucide-vue-next'
import { Alert } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import ManualIntentForm from '@/components/intent/ManualIntentForm.vue'
import { useAdminApi } from '@/composables/useAdminApi'
import { useLanguage } from '@/composables/useLanguage'
import {
  AUTOPILOT_RECOMMENDATIONS_PATH,
  COCKPIT_OVERVIEW_PATH,
  MANUAL_INTENTS_PATH,
  manualIntentPath,
} from '@/services/admin-api'
import type {
  ChannelRecommendation,
  CockpitOverviewResponse,
  ManualRoutingIntent,
  RecommendationsResponse,
  TrialResult,
} from '@/services/admin-api'

const { t } = useLanguage()
const api = useAdminApi()

const overview = ref<CockpitOverviewResponse | null>(null)
const recommendations = ref<ChannelRecommendation[]>([])
const activeTrials = ref<ManualRoutingIntent[]>([])
const loading = ref(true)
const errorMessage = ref('')
const endingTrialUid = ref('')

interface HealthStateItem {
  state: string
  count: number
  colorClass: string
  icon: typeof HeartPulse
  labelKey: string
}

const healthStateItems = computed<HealthStateItem[]>(() => {
  if (!overview.value) return []
  const sc = overview.value.health.stateCounts || {}
  return [
    { state: 'healthy', count: sc.healthy ?? 0, colorClass: 'text-emerald-500 bg-emerald-500/10 border-emerald-500/30', icon: HeartPulse, labelKey: 'healthCenter.state.healthy' },
    { state: 'degraded', count: sc.degraded ?? 0, colorClass: 'text-amber-500 bg-amber-500/10 border-amber-500/30', icon: AlertTriangle, labelKey: 'healthCenter.state.degraded' },
    { state: 'limited', count: sc.limited ?? 0, colorClass: 'text-orange-500 bg-orange-500/10 border-orange-500/30', icon: AlertCircle, labelKey: 'healthCenter.state.limited' },
    { state: 'misconfigured', count: sc.misconfigured ?? 0, colorClass: 'text-purple-500 bg-purple-500/10 border-purple-500/30', icon: ShieldAlert, labelKey: 'healthCenter.state.misconfigured' },
    { state: 'dead', count: sc.dead ?? 0, colorClass: 'text-red-500 bg-red-500/10 border-red-500/30', icon: XCircle, labelKey: 'healthCenter.state.dead' },
    { state: 'unknown', count: sc.unknown ?? 0, colorClass: 'text-muted-foreground bg-muted/40 border-border/50', icon: HelpCircle, labelKey: 'healthCenter.state.unknown' },
  ]
})

function intentStatusVariant(status: string): 'default' | 'secondary' | 'destructive' | 'outline' {
  switch (status) {
    case 'active': return 'default'
    case 'exhausted': return 'secondary'
    case 'disabled': return 'destructive'
    default: return 'outline'
  }
}

function runtimeStatusClass(status: string): string {
  switch (status) {
    case 'healthy': return 'border-emerald-500/30 bg-emerald-500/5 text-emerald-600 dark:text-emerald-400'
    case 'slow': return 'border-amber-500/30 bg-amber-500/5 text-amber-600 dark:text-amber-400'
    case 'error':
    case 'dead': return 'border-red-500/30 bg-red-500/5 text-red-600 dark:text-red-400'
    default: return 'border-border/60 bg-card/40 text-foreground'
  }
}

function formatRemaining(expiresAt: string): string {
  const ms = new Date(expiresAt).getTime() - Date.now()
  if (Number.isNaN(ms) || ms <= 0) return t('cockpitOverview.trialExpired')
  const totalMinutes = Math.floor(ms / 60000)
  const hours = Math.floor(totalMinutes / 60)
  const minutes = totalMinutes % 60
  if (hours > 0) return `${hours}h ${minutes}m`
  return `${minutes}m`
}

function formatSuccessRate(result: TrialResult): string {
  if (!result.hitCount) return '0%'
  return `${Math.round((result.successCount / result.hitCount) * 100)}%`
}

async function fetchOverview() {
  try {
    overview.value = await api.get<CockpitOverviewResponse>(COCKPIT_OVERVIEW_PATH)
  } catch (err) {
    console.error('[Cockpit-Overview] 加载失败:', err)
    overview.value = null
    errorMessage.value = err instanceof Error ? err.message : String(err)
  }
}

async function fetchRecommendations() {
  try {
    const resp = await api.get<RecommendationsResponse>(AUTOPILOT_RECOMMENDATIONS_PATH)
    recommendations.value = resp.recommendations || []
  } catch (err) {
    console.error('[Cockpit-Overview] 渠道推荐加载失败:', err)
    recommendations.value = []
  }
}

async function fetchActiveTrials() {
  try {
    const resp = await api.get<{ intents: ManualRoutingIntent[]; total: number }>(`${MANUAL_INTENTS_PATH}?all=false`)
    activeTrials.value = resp.intents || []
  } catch (err) {
    console.error('[Cockpit-Overview] 活跃试用加载失败:', err)
    activeTrials.value = []
  }
}

async function fetchAll() {
  loading.value = true
  errorMessage.value = ''
  try {
    await Promise.all([fetchOverview(), fetchRecommendations(), fetchActiveTrials()])
  } finally {
    loading.value = false
  }
}

async function endTrial(uid: string) {
  endingTrialUid.value = uid
  try {
    await api.del(manualIntentPath(uid))
    await fetchActiveTrials()
  } catch (err) {
    console.error('[Cockpit-Overview] 结束试用失败:', err)
  } finally {
    endingTrialUid.value = ''
  }
}

// 新建试用意图对话框
const showTrialDialog = ref(false)

async function onTrialCreated() {
  showTrialDialog.value = false
  await fetchActiveTrials()
}

onMounted(fetchAll)
</script>

<template>
  <div class="mx-auto flex w-full max-w-[1680px] flex-col gap-4">
    <div class="flex items-center justify-between">
      <h3 class="text-lg font-bold">{{ t('cockpitOverview.title') }}</h3>
      <Button variant="outline" size="sm" :disabled="loading" @click="fetchAll">
        <Loader2 v-if="loading" class="size-4 animate-spin" />
        <RefreshCw v-else class="size-4" />
        {{ t('app.actions.refresh') }}
      </Button>
    </div>

    <Alert v-if="errorMessage" variant="destructive">
      <p class="text-sm">{{ errorMessage }}</p>
    </Alert>

    <div v-if="loading && !overview" class="flex items-center justify-center py-16 text-muted-foreground">
      <Loader2 class="size-6 animate-spin" />
    </div>

    <div v-else-if="!overview" class="flex flex-col items-center gap-2 py-16 text-center text-muted-foreground">
      <HeartPulse class="size-10" />
      <div class="text-sm">{{ t('cockpitOverview.empty') }}</div>
    </div>

    <template v-else>
      <!-- 健康概览 -->
      <Card>
        <CardHeader>
          <CardTitle class="flex items-center gap-2 text-sm">
            <HeartPulse class="size-4 text-emerald-500" />
            {{ t('cockpitOverview.health') }}
          </CardTitle>
        </CardHeader>
        <CardContent class="flex flex-col gap-3">
          <div class="grid grid-cols-2 gap-3 sm:grid-cols-4">
            <div class="rounded-lg border border-border/60 bg-card/40 px-4 py-3">
              <div class="text-2xl font-bold">{{ overview.health.totalChannels }}</div>
              <div class="text-xs text-muted-foreground">{{ t('cockpitOverview.channels') }}</div>
            </div>
            <div class="rounded-lg border border-border/60 bg-card/40 px-4 py-3">
              <div class="text-2xl font-bold">{{ overview.health.totalEndpoints }}</div>
              <div class="text-xs text-muted-foreground">{{ t('cockpitOverview.endpoints') }}</div>
            </div>
          </div>
          <div class="grid grid-cols-2 gap-2 sm:grid-cols-3 md:grid-cols-6">
            <div
              v-for="item in healthStateItems"
              :key="item.state"
              class="flex flex-col items-center gap-1 rounded-lg border px-3 py-3 text-center"
              :class="item.colorClass"
            >
              <component :is="item.icon" class="size-5" />
              <div class="text-xl font-bold">{{ item.count }}</div>
              <div class="text-[11px] text-muted-foreground">{{ t(item.labelKey) }}</div>
            </div>
          </div>
        </CardContent>
      </Card>

      <!-- 订阅余额 -->
      <Card>
        <CardHeader>
          <CardTitle class="flex items-center gap-2 text-sm">
            <Banknote class="size-4 text-primary" />
            {{ t('cockpitOverview.subscriptions') }}
          </CardTitle>
        </CardHeader>
        <CardContent class="flex flex-col gap-3">
          <div class="grid grid-cols-2 gap-3 sm:grid-cols-4">
            <div class="rounded-lg border border-border/60 bg-card/40 px-4 py-3">
              <div class="text-2xl font-bold">{{ overview.subscriptions.total }}</div>
              <div class="text-xs text-muted-foreground">{{ t('cockpitOverview.totalSubscriptions') }}</div>
            </div>
            <div
              v-for="(amount, code) in overview.subscriptions.balanceByCode"
              :key="`bal-${code}`"
              class="rounded-lg border border-primary/30 bg-primary/5 px-4 py-3"
            >
              <div class="text-2xl font-bold">{{ amount }}</div>
              <div class="text-xs text-muted-foreground">{{ code }}</div>
            </div>
          </div>
          <div v-if="Object.keys(overview.subscriptions.countByMode).length > 0" class="flex flex-wrap gap-2">
            <Badge
              v-for="(count, mode) in overview.subscriptions.countByMode"
              :key="`mode-${mode}`"
              variant="secondary"
            >
              {{ mode }}: {{ count }}
            </Badge>
          </div>
          <div v-if="Object.keys(overview.subscriptions.countByTier).length > 0" class="flex flex-wrap gap-2">
            <Badge
              v-for="(count, tier) in overview.subscriptions.countByTier"
              :key="`tier-${tier}`"
              variant="outline"
            >
              {{ tier }}: {{ count }}
            </Badge>
          </div>
        </CardContent>
      </Card>

      <!-- 本地 Runtime 概览 -->
      <Card>
        <CardHeader>
          <CardTitle class="flex items-center gap-2 text-sm">
            <Server class="size-4 text-amber-500" />
            {{ t('cockpitOverview.localRuntimes') }}
          </CardTitle>
        </CardHeader>
        <CardContent>
          <div class="grid grid-cols-2 gap-3 sm:grid-cols-4">
            <div class="rounded-lg border border-border/60 bg-card/40 px-4 py-3 text-center">
              <div class="text-2xl font-bold">{{ overview.localRuntimes.total }}</div>
              <div class="text-xs text-muted-foreground">{{ t('cockpitOverview.totalRuntimes') }}</div>
            </div>
            <div class="rounded-lg border border-border/60 bg-card/40 px-4 py-3 text-center">
              <div class="text-2xl font-bold">{{ overview.localRuntimes.totalModels }}</div>
              <div class="text-xs text-muted-foreground">{{ t('cockpitOverview.totalModels') }}</div>
            </div>
            <div
              v-for="(count, status) in overview.localRuntimes.statusCounts"
              :key="`runtime-${status}`"
              class="rounded-lg border px-4 py-3 text-center"
              :class="runtimeStatusClass(String(status))"
            >
              <div class="text-2xl font-bold">{{ count }}</div>
              <div class="text-xs text-muted-foreground">{{ status }}</div>
            </div>
          </div>
        </CardContent>
      </Card>

      <!-- 人工意图统计 -->
      <Card>
        <CardHeader>
          <CardTitle class="flex items-center gap-2 text-sm">
            <UserCheck class="size-4 text-blue-500" />
            {{ t('cockpitOverview.manualIntents') }}
          </CardTitle>
        </CardHeader>
        <CardContent>
          <div class="grid grid-cols-2 gap-3 sm:grid-cols-4">
            <div class="rounded-lg border border-blue-500/30 bg-blue-500/5 px-4 py-3">
              <div class="text-2xl font-bold">{{ overview.manualIntents.activeCount }}</div>
              <div class="text-xs text-muted-foreground">{{ t('cockpitOverview.activeIntents') }}</div>
            </div>
            <div class="rounded-lg border border-border/60 bg-card/40 px-4 py-3">
              <div class="text-2xl font-bold">{{ overview.manualIntents.totalCount }}</div>
              <div class="text-xs text-muted-foreground">{{ t('cockpitOverview.totalIntents') }}</div>
            </div>
          </div>
        </CardContent>
      </Card>

      <!-- 活跃试用意图列表 -->
      <Card>
        <CardHeader>
          <div class="flex items-center justify-between">
            <CardTitle class="flex items-center gap-2 text-sm">
              <FlaskConical class="size-4 text-purple-500" />
              {{ t('cockpitOverview.activeTrials') }}
            </CardTitle>
            <Button variant="outline" size="sm" @click="showTrialDialog = true">
              <FlaskConical class="size-4" />
              {{ t('manualIntent.actions.create') }}
            </Button>
          </div>
        </CardHeader>
        <CardContent>
          <div v-if="activeTrials.length === 0" class="py-4 text-center text-sm text-muted-foreground">
            {{ t('cockpitOverview.noActiveTrials') }}
          </div>
          <div v-else class="overflow-hidden rounded-lg border border-border/50">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{{ t('manualIntent.field.intentType') }}</TableHead>
                  <TableHead>{{ t('cockpitOverview.status') }}</TableHead>
                  <TableHead>{{ t('manualIntent.field.model') }}</TableHead>
                  <TableHead>{{ t('manualIntent.field.channelUid') }}</TableHead>
                  <TableHead>{{ t('cockpitOverview.remaining') }}</TableHead>
                  <TableHead>{{ t('cockpitOverview.trialStatsLabel') }}</TableHead>
                  <TableHead class="text-right">{{ t('manualIntent.actions.end') }}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                <TableRow v-for="trial in activeTrials" :key="trial.intentUid">
                  <TableCell>
                    <Badge variant="outline">{{ t(`manualIntent.intentType.${trial.intentType}`) }}</Badge>
                  </TableCell>
                  <TableCell>
                    <Badge :variant="intentStatusVariant(trial.status)">{{ t(`manualIntent.status.${trial.status}`) }}</Badge>
                  </TableCell>
                  <TableCell class="max-w-[160px] truncate text-xs">{{ trial.model || '-' }}</TableCell>
                  <TableCell class="max-w-[160px] truncate text-xs">{{ trial.channelUid || '-' }}</TableCell>
                  <TableCell class="text-xs">{{ formatRemaining(trial.expiresAt) }}</TableCell>
                  <TableCell class="text-xs">
                    {{ t('cockpitOverview.trialStats', {
                      hit: String(trial.trialResult.hitCount),
                      rate: formatSuccessRate(trial.trialResult),
                      fallback: String(trial.trialResult.fallbackCount ?? 0),
                    }) }}
                  </TableCell>
                  <TableCell class="text-right">
                    <Button
                      variant="destructive"
                      size="sm"
                      :disabled="endingTrialUid === trial.intentUid"
                      @click="endTrial(trial.intentUid)"
                    >
                      <Loader2 v-if="endingTrialUid === trial.intentUid" class="size-3.5 animate-spin" />
                      <StopCircle v-else class="size-3.5" />
                      {{ t('manualIntent.actions.end') }}
                    </Button>
                  </TableCell>
                </TableRow>
              </TableBody>
            </Table>
          </div>
        </CardContent>
      </Card>

      <!-- 渠道推荐 -->
      <Card>
        <CardHeader>
          <CardTitle class="flex items-center gap-2 text-sm">
            <Lightbulb class="size-4 text-emerald-500" />
            {{ t('cockpitOverview.recommendations') }}
          </CardTitle>
        </CardHeader>
        <CardContent>
          <div v-if="recommendations.length === 0" class="py-4 text-center text-sm text-muted-foreground">
            {{ t('cockpitOverview.noRecommendations') }}
          </div>
          <div v-else class="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
            <div
              v-for="(rec, idx) in recommendations"
              :key="`${rec.proxyKeyMask}-${rec.domain}-${idx}`"
              class="rounded-lg border border-emerald-500/30 bg-emerald-500/5 p-3"
            >
              <div class="mb-1 flex items-center justify-between">
                <Badge variant="secondary">{{ rec.domain }}</Badge>
                <span class="text-[11px] text-muted-foreground">{{ t('cockpitOverview.usageCount', { count: String(rec.domainUsageCount) }) }}</span>
              </div>
              <div class="mb-1 text-xs">
                <span class="text-muted-foreground">{{ t('cockpitOverview.currentChannel') }}:</span>
                <code class="ml-1">{{ rec.currentChannelUid }}</code>
                <span class="text-muted-foreground"> ({{ rec.currentScore.toFixed(2) }})</span>
              </div>
              <div class="mb-1 text-xs">
                <span class="text-muted-foreground">{{ t('cockpitOverview.recommendedChannel') }}:</span>
                <code class="ml-1 font-bold">{{ rec.recommendedChannelUid }}</code>
                <span class="text-muted-foreground"> ({{ rec.recommendedScore.toFixed(2) }})</span>
              </div>
              <div class="text-[11px] text-muted-foreground">
                {{ t('cockpitOverview.scoreDelta', { delta: rec.scoreDelta.toFixed(2) }) }}
              </div>
            </div>
          </div>
        </CardContent>
      </Card>

      <!-- 待办事项 -->
      <Card v-if="overview.todoItems && overview.todoItems.length > 0">
        <CardHeader>
          <CardTitle class="flex items-center gap-2 text-sm">
            <AlertTriangle class="size-4 text-amber-500" />
            {{ t('cockpitOverview.todoList') }}
          </CardTitle>
        </CardHeader>
        <CardContent>
          <div class="overflow-hidden rounded-lg border border-border/50">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Channel</TableHead>
                  <TableHead>Kind</TableHead>
                  <TableHead>Endpoint</TableHead>
                  <TableHead>Health</TableHead>
                  <TableHead>Action</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                <TableRow v-for="item in overview.todoItems" :key="item.endpointUid">
                  <TableCell class="text-xs">{{ item.channelUid }}</TableCell>
                  <TableCell><Badge variant="outline">{{ item.channelKind }}</Badge></TableCell>
                  <TableCell class="max-w-[220px] truncate text-xs">{{ item.baseUrl }}</TableCell>
                  <TableCell><Badge variant="secondary">{{ item.healthState }}</Badge></TableCell>
                  <TableCell class="text-xs">{{ item.suggestedAction }}</TableCell>
                </TableRow>
              </TableBody>
            </Table>
          </div>
        </CardContent>
      </Card>
    </template>

    <!-- 新建试用意图对话框 -->
    <Dialog v-model:open="showTrialDialog">
      <DialogContent class="max-h-[85vh] overflow-y-auto sm:max-w-[640px]">
        <DialogHeader>
          <DialogTitle>{{ t('manualIntent.actions.create') }}</DialogTitle>
        </DialogHeader>
        <ManualIntentForm @created="onTrialCreated" @close="showTrialDialog = false" />
      </DialogContent>
    </Dialog>
  </div>
</template>
