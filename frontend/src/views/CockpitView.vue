<template>
  <div class="cockpit-view">
    <!-- Header -->
    <div class="d-flex align-center justify-space-between mb-4">
      <div class="d-flex align-center">
        <v-icon size="28" class="mr-2" color="primary">mdi-view-dashboard-outline</v-icon>
        <span class="text-h5 font-weight-bold">{{ t('cockpitOverview.title') }}</span>
      </div>
      <v-btn
        variant="tonal"
        prepend-icon="mdi-refresh"
        :loading="loading"
        @click="() => { fetchOverview(); fetchRecommendations(); fetchActiveTrials() }"
      >
        {{ t('app.actions.refresh') }}
      </v-btn>
    </div>

    <!-- Loading state -->
    <div v-if="loading && !overview" class="text-center py-12">
      <v-progress-circular indeterminate color="primary" size="48" />
    </div>

    <!-- Empty state -->
    <div v-else-if="!loading && !overview" class="text-center py-12 text-medium-emphasis">
      <v-icon size="64" class="mb-4" color="grey">mdi-view-dashboard-outline</v-icon>
      <div class="text-body-1">{{ t('cockpitOverview.empty') }}</div>
    </div>

    <!-- Overview content -->
    <template v-else-if="overview">
      <!-- Health summary -->
      <div class="section-label text-subtitle-2 font-weight-bold mb-2 d-flex align-center">
        <v-icon size="18" class="mr-1" color="success">mdi-heart-pulse</v-icon>
        {{ t('cockpitOverview.health') }}
      </div>
      <v-row dense class="mb-4">
        <v-col cols="6" sm="4" md="2">
          <v-card variant="tonal" rounded="lg" class="pa-3 text-center">
            <div class="text-h5 font-weight-bold">{{ overview.health.totalChannels }}</div>
            <div class="text-caption text-medium-emphasis">{{ t('cockpitOverview.channels') }}</div>
          </v-card>
        </v-col>
        <v-col cols="6" sm="4" md="2">
          <v-card variant="tonal" rounded="lg" class="pa-3 text-center">
            <div class="text-h5 font-weight-bold">{{ overview.health.totalEndpoints }}</div>
            <div class="text-caption text-medium-emphasis">{{ t('cockpitOverview.endpoints') }}</div>
          </v-card>
        </v-col>
        <v-col
          v-for="st in healthStateItems"
          :key="st.state"
          cols="6" sm="4" md="2"
        >
          <v-card variant="tonal" :color="st.color" rounded="lg" class="pa-3 text-center">
            <v-icon :color="st.color" size="24" class="mb-1">{{ st.icon }}</v-icon>
            <div class="text-h5 font-weight-bold">{{ st.count }}</div>
            <div class="text-caption text-medium-emphasis">{{ t(st.labelKey) }}</div>
          </v-card>
        </v-col>
      </v-row>

      <!-- Subscriptions summary -->
      <div class="section-label text-subtitle-2 font-weight-bold mb-2 d-flex align-center">
        <v-icon size="18" class="mr-1" color="primary">mdi-cash-multiple</v-icon>
        {{ t('cockpitOverview.subscriptions') }}
      </div>
      <v-row dense class="mb-4">
        <v-col cols="6" sm="4" md="2">
          <v-card variant="tonal" rounded="lg" class="pa-3 text-center">
            <div class="text-h5 font-weight-bold">{{ overview.subscriptions.total }}</div>
            <div class="text-caption text-medium-emphasis">{{ t('cockpitOverview.totalSubscriptions') }}</div>
          </v-card>
        </v-col>
        <v-col
          v-for="(amount, code) in overview.subscriptions.balanceByCode"
          :key="'bal-' + code"
          cols="6" sm="4" md="2"
        >
          <v-card variant="tonal" color="primary" rounded="lg" class="pa-3 text-center">
            <div class="text-h6 font-weight-bold">{{ code }} {{ amount.toFixed(2) }}</div>
            <div class="text-caption text-medium-emphasis">{{ t('cockpitOverview.balanceByCode') }}</div>
          </v-card>
        </v-col>
      </v-row>

      <!-- Billing mode chips -->
      <div v-if="Object.keys(overview.subscriptions.countByMode).length > 0" class="d-flex flex-wrap ga-2 mb-2">
        <v-chip
          v-for="(count, mode) in overview.subscriptions.countByMode"
          :key="'mode-' + mode"
          size="small"
          variant="tonal"
          color="secondary"
        >
          {{ getBillingModeLabel(String(mode)) }}: {{ count }}
        </v-chip>
      </div>
      <!-- Origin tier chips -->
      <div v-if="Object.keys(overview.subscriptions.countByTier).length > 0" class="d-flex flex-wrap ga-2 mb-4">
        <v-chip
          v-for="(count, tier) in overview.subscriptions.countByTier"
          :key="'tier-' + tier"
          size="small"
          variant="outlined"
          color="info"
        >
          {{ getOriginTierLabel(String(tier)) }}: {{ count }}
        </v-chip>
      </div>

      <!-- Local runtimes -->
      <div class="section-label text-subtitle-2 font-weight-bold mb-2 d-flex align-center">
        <v-icon size="18" class="mr-1" color="warning">mdi-server-network</v-icon>
        {{ t('cockpitOverview.localRuntimes') }}
      </div>
      <v-row dense class="mb-4">
        <v-col cols="6" sm="4" md="2">
          <v-card variant="tonal" rounded="lg" class="pa-3 text-center">
            <div class="text-h5 font-weight-bold">{{ overview.localRuntimes.total }}</div>
            <div class="text-caption text-medium-emphasis">{{ t('cockpitOverview.totalRuntimes') }}</div>
          </v-card>
        </v-col>
        <v-col cols="6" sm="4" md="2">
          <v-card variant="tonal" rounded="lg" class="pa-3 text-center">
            <div class="text-h5 font-weight-bold">{{ overview.localRuntimes.totalModels }}</div>
            <div class="text-caption text-medium-emphasis">{{ t('cockpitOverview.totalModels') }}</div>
          </v-card>
        </v-col>
        <v-col
          v-for="(count, status) in overview.localRuntimes.statusCounts"
          :key="'rt-' + status"
          cols="6" sm="4" md="2"
        >
          <v-card variant="tonal" :color="getRuntimeStatusColor(String(status))" rounded="lg" class="pa-3 text-center">
            <div class="text-h6 font-weight-bold">{{ count }}</div>
            <div class="text-caption text-medium-emphasis">{{ String(status) }}</div>
          </v-card>
        </v-col>
      </v-row>

      <!-- Manual intents -->
      <div class="section-label text-subtitle-2 font-weight-bold mb-2 d-flex align-center">
        <v-icon size="18" class="mr-1" color="info">mdi-account-switch</v-icon>
        {{ t('cockpitOverview.manualIntents') }}
      </div>
      <v-row dense class="mb-4">
        <v-col cols="6" sm="4" md="2">
          <v-card variant="tonal" color="info" rounded="lg" class="pa-3 text-center">
            <div class="text-h5 font-weight-bold">{{ overview.manualIntents.activeCount }}</div>
            <div class="text-caption text-medium-emphasis">{{ t('cockpitOverview.activeIntents') }}</div>
          </v-card>
        </v-col>
        <v-col cols="6" sm="4" md="2">
          <v-card variant="tonal" rounded="lg" class="pa-3 text-center">
            <div class="text-h5 font-weight-bold">{{ overview.manualIntents.totalCount }}</div>
            <div class="text-caption text-medium-emphasis">{{ t('cockpitOverview.totalIntents') }}</div>
          </v-card>
        </v-col>
      </v-row>

      <!-- Active trial intents -->
      <div class="section-label text-subtitle-2 font-weight-bold mb-2 d-flex align-center">
        <v-icon size="18" class="mr-1" color="deep-purple">mdi-flask-outline</v-icon>
        {{ t('cockpitOverview.activeTrials') }}
      </div>

      <div v-if="activeTrials.length === 0" class="text-body-2 text-medium-emphasis mb-4">
        {{ t('cockpitOverview.noActiveTrials') }}
      </div>

      <v-row v-else dense class="mb-4">
        <v-col
          v-for="trial in activeTrials"
          :key="trial.intentUid"
          cols="12" sm="6" md="4"
        >
          <v-card variant="tonal" color="deep-purple" rounded="lg" class="pa-3">
            <div class="d-flex align-center justify-space-between mb-1">
              <v-chip size="x-small" variant="tonal" color="primary">{{ t(`manualIntent.intentType.${trial.intentType}`) }}</v-chip>
              <v-chip size="x-small" variant="tonal" :color="getIntentStatusColor(trial.status)">{{ t(`manualIntent.status.${trial.status}`) }}</v-chip>
            </div>
            <div v-if="trial.model" class="text-body-2 mb-1">
              <span class="text-medium-emphasis">{{ t('manualIntent.field.model') }}:</span>
              <code class="text-caption">{{ trial.model }}</code>
            </div>
            <div v-if="trial.channelUid" class="text-body-2 mb-1">
              <span class="text-medium-emphasis">{{ t('manualIntent.field.channelUid') }}:</span>
              <code class="text-caption">{{ trial.channelUid }}</code>
            </div>
            <div class="text-caption text-medium-emphasis mb-1">
              {{ t('cockpitOverview.trialRemaining', { time: formatRemaining(trial.expiresAt) }) }}
            </div>
            <div class="text-caption mb-2">
              {{ t('cockpitOverview.trialStats', {
                hit: trial.trialResult.hitCount,
                rate: formatSuccessRate(trial.trialResult),
                fallback: trial.trialResult.fallbackCount ?? 0,
              }) }}
            </div>
            <v-btn
              size="x-small"
              variant="tonal"
              color="error"
              prepend-icon="mdi-stop-circle-outline"
              :loading="endingTrialUid === trial.intentUid"
              @click="endTrial(trial.intentUid)"
            >
              {{ t('manualIntent.actions.end') }}
            </v-btn>
          </v-card>
        </v-col>
      </v-row>

      <!-- Channel recommendations -->
      <div class="section-label text-subtitle-2 font-weight-bold mb-2 d-flex align-center">
        <v-icon size="18" class="mr-1" color="success">mdi-lightbulb-on-outline</v-icon>
        {{ t('cockpitOverview.recommendations') }}
      </div>

      <div v-if="recommendations.length === 0" class="text-body-2 text-medium-emphasis mb-4">
        {{ t('cockpitOverview.noRecommendations') }}
      </div>

      <v-row v-else dense class="mb-4">
        <v-col
          v-for="(rec, idx) in recommendations"
          :key="`${rec.proxyKeyMask}-${rec.domain}-${idx}`"
          cols="12" sm="6" md="4"
        >
          <v-card variant="tonal" color="success" rounded="lg" class="pa-3">
            <div class="d-flex align-center justify-space-between mb-1">
              <v-chip size="x-small" variant="tonal" color="primary">{{ rec.domain }}</v-chip>
              <span class="text-caption text-medium-emphasis">{{ t('cockpitOverview.usageCount', { count: rec.domainUsageCount }) }}</span>
            </div>
            <div class="text-body-2 mb-1">
              <span class="text-medium-emphasis">{{ t('cockpitOverview.currentChannel') }}:</span>
              <code class="text-caption">{{ rec.currentChannelUid }}</code>
              <span class="text-caption text-medium-emphasis"> ({{ rec.currentScore.toFixed(2) }})</span>
            </div>
            <div class="text-body-2 mb-1">
              <span class="text-medium-emphasis">{{ t('cockpitOverview.recommendedChannel') }}:</span>
              <code class="text-caption font-weight-bold">{{ rec.recommendedChannelUid }}</code>
              <span class="text-caption text-medium-emphasis"> ({{ rec.recommendedScore.toFixed(2) }})</span>
            </div>
            <div class="text-caption text-medium-emphasis">
              {{ t('cockpitOverview.scoreDelta', { delta: rec.scoreDelta.toFixed(2) }) }}
            </div>
          </v-card>
        </v-col>
      </v-row>

      <!-- To-do items -->
      <div class="section-label text-subtitle-2 font-weight-bold mb-2 d-flex align-center">
        <v-icon size="18" class="mr-1" color="warning">mdi-alert</v-icon>
        {{ t('cockpitOverview.todoList') }}
      </div>

      <div v-if="overview.todoItems.length === 0" class="text-body-2 text-medium-emphasis mb-4">
        {{ t('cockpitOverview.noTodoItems') }}
      </div>

      <v-table v-else density="compact" hover class="mb-4">
        <thead>
          <tr>
            <th class="text-left">Channel</th>
            <th class="text-left">Kind</th>
            <th class="text-left">Endpoint</th>
            <th class="text-left">Health</th>
            <th class="text-left">Action</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="item in overview.todoItems" :key="item.endpointUid">
            <td class="text-caption">{{ item.channelUid }}</td>
            <td>
              <v-chip size="x-small" variant="tonal" color="primary">{{ item.channelKind }}</v-chip>
            </td>
            <td class="text-caption" style="max-width: 200px; overflow: hidden; text-overflow: ellipsis;">
              {{ item.baseUrl }}
            </td>
            <td>
              <v-chip size="x-small" :color="getHealthColor(item.healthState)" variant="tonal">
                {{ item.healthState }}
              </v-chip>
            </td>
            <td class="text-caption">{{ item.suggestedAction }}</td>
          </tr>
        </tbody>
      </v-table>
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useI18n } from '@/i18n'
import { api } from '@/services/api'
import type { CockpitOverviewResponse, ChannelRecommendation, ManualRoutingIntent, TrialResult } from '@/services/api-types'

const { t } = useI18n()

const overview = ref<CockpitOverviewResponse | null>(null)
const loading = ref(true)
const recommendations = ref<ChannelRecommendation[]>([])
const activeTrials = ref<ManualRoutingIntent[]>([])
const endingTrialUid = ref<string>('')

interface HealthStateItem {
  state: string
  count: number
  color: string
  icon: string
  labelKey: string
}

const healthStateItems = computed<HealthStateItem[]>(() => {
  if (!overview.value) return []
  const sc = overview.value.health.stateCounts
  return [
    { state: 'healthy', count: sc.healthy ?? 0, color: 'success', icon: 'mdi-heart-pulse', labelKey: 'healthCenter.state.healthy' },
    { state: 'degraded', count: sc.degraded ?? 0, color: 'warning', icon: 'mdi-alert', labelKey: 'healthCenter.state.degraded' },
    { state: 'limited', count: sc.limited ?? 0, color: 'orange', icon: 'mdi-alert-circle-outline', labelKey: 'healthCenter.state.limited' },
    { state: 'misconfigured', count: sc.misconfigured ?? 0, color: 'deep-purple', icon: 'mdi-shield-alert', labelKey: 'healthCenter.state.misconfigured' },
    { state: 'dead', count: sc.dead ?? 0, color: 'error', icon: 'mdi-close-circle', labelKey: 'healthCenter.state.dead' },
    { state: 'unknown', count: sc.unknown ?? 0, color: 'grey', icon: 'mdi-information', labelKey: 'healthCenter.state.unknown' },
  ]
})

function getBillingModeLabel(mode: string): string {
  const key = `subscription.billingMode.${mode}`
  return t(key)
}

function getOriginTierLabel(tier: string): string {
  const key = `subscription.originTier.${tier}`
  return t(key)
}

function getRuntimeStatusColor(status: string): string {
  switch (status) {
    case 'healthy': return 'success'
    case 'slow': return 'warning'
    case 'error':
    case 'dead': return 'error'
    default: return 'grey'
  }
}

function getHealthColor(state: string): string {
  switch (state) {
    case 'healthy': return 'success'
    case 'degraded': return 'warning'
    case 'limited': return 'orange'
    case 'misconfigured': return 'deep-purple'
    case 'dead': return 'error'
    default: return 'grey'
  }
}

async function fetchOverview() {
  loading.value = true
  try {
    overview.value = await api.getCockpitOverview()
  } catch (e) {
    console.error('Failed to fetch cockpit overview:', e)
    overview.value = null
  } finally {
    loading.value = false
  }
}

async function fetchRecommendations() {
  try {
    const resp = await api.getRecommendations()
    recommendations.value = resp.recommendations ?? []
  } catch (e) {
    console.error('Failed to fetch channel recommendations:', e)
    recommendations.value = []
  }
}

async function fetchActiveTrials() {
  try {
    const resp = await api.getManualIntents({ all: false })
    activeTrials.value = resp.intents ?? []
  } catch (e) {
    console.error('Failed to fetch active trial intents:', e)
    activeTrials.value = []
  }
}

async function endTrial(uid: string) {
  endingTrialUid.value = uid
  try {
    await api.deleteManualIntent(uid)
    await fetchActiveTrials()
  } catch (e) {
    console.error('Failed to end trial intent:', e)
  } finally {
    endingTrialUid.value = ''
  }
}

function getIntentStatusColor(status: string): string {
  switch (status) {
    case 'active': return 'success'
    case 'expired': return 'grey'
    case 'exhausted': return 'warning'
    case 'disabled': return 'error'
    default: return 'grey'
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

onMounted(() => {
  fetchOverview()
  fetchRecommendations()
  fetchActiveTrials()
})
</script>
