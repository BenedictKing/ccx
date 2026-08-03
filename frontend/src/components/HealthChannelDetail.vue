<template>
  <div class="health-detail pa-4">
    <div v-if="loading" class="text-center py-6">
      <v-progress-circular indeterminate color="primary" />
    </div>

    <div v-else-if="endpoints.length === 0" class="text-center py-4 text-medium-emphasis">
      {{ t('healthCenter.noChannels') }}
    </div>

    <div v-else>
      <v-table density="compact" hover>
        <thead>
          <tr>
            <th>{{ t('healthCenter.col.status') }}</th>
            <th>{{ t('healthCenter.detail.baseUrl') }}</th>
            <th>{{ t('healthCenter.detail.keyHash') }}</th>
            <th>{{ t('healthCenter.detail.confidence') }}</th>
            <th>{{ t('healthCenter.detail.qualityTier') }}</th>
            <th>{{ t('healthCenter.detail.stabilityTier') }}</th>
            <th>{{ t('healthCenter.detail.speedTier') }}</th>
            <th>{{ t('healthCenter.detail.successRate15m') }}</th>
            <th>{{ t('healthCenter.detail.p95') }}</th>
            <th>{{ t('healthCenter.detail.firstByteP95') }}</th>
            <th>{{ t('healthCenter.detail.consecutiveFail') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="ep in endpoints" :key="ep.endpointUid">
            <td>
              <v-chip
                size="small"
                :color="stateColor(ep.healthState)"
                variant="tonal"
              >
                <v-icon size="14" start>{{ stateIcon(ep.healthState) }}</v-icon>
                {{ ep.healthState }}
              </v-chip>
            </td>
            <td class="text-caption" style="max-width: 220px; word-break: break-all;">{{ ep.baseUrl }}</td>
            <td class="font-monospace text-caption">{{ ep.keyMask || ep.keyHash }}</td>
            <td>{{ formatPercent(ep.healthConfidence) }}</td>
            <td>
              <v-chip v-if="ep.qualityTier" size="x-small" variant="outlined">{{ ep.qualityTier }}</v-chip>
            </td>
            <td>
              <v-chip v-if="ep.stabilityTier" size="x-small" variant="outlined">{{ ep.stabilityTier }}</v-chip>
            </td>
            <td>
              <v-chip v-if="ep.speedTier" size="x-small" variant="outlined">{{ ep.speedTier }}</v-chip>
            </td>
            <td :class="rateColor(ep.successRate15m)">{{ formatPercent(ep.successRate15m) }}</td>
            <td>{{ ep.p95LatencyMs ? ep.p95LatencyMs.toFixed(0) + 'ms' : '-' }}</td>
            <td>
              {{ ep.p95FirstByteLatencyMs ? ep.p95FirstByteLatencyMs.toFixed(0) + 'ms' : '-' }}
              <span v-if="ep.firstByteSampleCount" class="text-caption text-medium-emphasis">
                (n={{ ep.firstByteSampleCount }})
              </span>
            </td>
            <td>
              <span v-if="ep.consecutiveFail > 0" class="text-error font-weight-bold">{{ ep.consecutiveFail }}</span>
              <span v-else>-</span>
            </td>
          </tr>
        </tbody>
      </v-table>

      <!-- evidence / suggestedAction per endpoint (collapsed) -->
      <v-expansion-panels v-if="hasEvidence" variant="accordion" class="mt-3">
        <v-expansion-panel v-for="ep in endpointWithEvidence" :key="'evidence-' + ep.endpointUid">
          <v-expansion-panel-title class="text-caption">
            <v-icon size="16" class="mr-2" :color="stateColor(ep.healthState)">{{ stateIcon(ep.healthState) }}</v-icon>
            {{ ep.keyMask || ep.keyHash }} -- {{ ep.healthState }}
            <v-chip v-if="ep.tokenPlanUsageSupported" size="x-small" color="primary" variant="tonal" class="ml-2">Token Plan</v-chip>
          </v-expansion-panel-title>
          <v-expansion-panel-text>
            <div v-if="ep.miniMaxTokenPlanUsage?.models.length && !ep.miniMaxTokenPlanUsageError" class="mb-3">
              <div class="text-caption font-weight-bold mb-1">{{ t('healthCenter.detail.tokenPlanUsage') }}</div>
              <UsageQuotaRows :items="minimaxQuotaItems(ep)" />
              <div class="text-caption text-medium-emphasis mt-1">
                {{ t('healthCenter.detail.updatedAt') }}: {{ formatDateTime(ep.miniMaxTokenPlanUsage.fetchedAt) }}
              </div>
            </div>
            <div v-else-if="ep.miniMaxTokenPlanUsageError" class="text-caption text-error mb-3">
              {{ t('healthCenter.detail.tokenPlanUsageError') }}: {{ ep.miniMaxTokenPlanUsageError }}
            </div>
            <div v-if="ep.healthEvidence" class="mb-2">
              <div class="text-caption font-weight-bold mb-1">{{ t('healthCenter.detail.evidence') }}</div>
              <div class="text-body-2" style="white-space: pre-wrap;">{{ ep.healthEvidence }}</div>
            </div>
            <div v-if="ep.suggestedAction">
              <div class="text-caption font-weight-bold mb-1">{{ t('healthCenter.detail.suggestedAction') }}</div>
              <v-chip size="small" color="info" variant="tonal">{{ ep.suggestedAction }}</v-chip>
            </div>
            <div v-if="ep.lastSuccessAt" class="mt-2 text-caption text-medium-emphasis">
              {{ t('healthCenter.detail.lastSuccess') }}: {{ ep.lastSuccessAt }}
            </div>
          </v-expansion-panel-text>
        </v-expansion-panel>
      </v-expansion-panels>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { useI18n } from '@/i18n'
import { api } from '@/services/api'
import type { HealthState, EndpointDetailItem } from '@/services/api-types'
import { buildMinimaxQuotaItems } from '@/utils/minimaxEndpointUsage'
import UsageQuotaRows from './UsageQuotaRows.vue'

const props = defineProps<{ channelUid: string }>()
const { t } = useI18n()

const endpoints = ref<EndpointDetailItem[]>([])
const loading = ref(true)

const loadEndpoints = async () => {
  const resp = await api.getHealthCenterChannelEndpoints(props.channelUid)
  endpoints.value = resp.endpoints
}

onMounted(async () => {
  try {
    await loadEndpoints()
    const tokenPlanEndpoints = endpoints.value.filter(ep => ep.tokenPlanUsageSupported)
    if (tokenPlanEndpoints.length > 0) {
      await Promise.allSettled(tokenPlanEndpoints.map(ep => api.refreshEndpointTokenPlanUsage(ep.endpointUid)))
      await loadEndpoints()
    }
  } finally {
    loading.value = false
  }
})

const hasEvidence = computed(() =>
  endpoints.value.some(ep => ep.healthEvidence || ep.suggestedAction || ep.tokenPlanUsageSupported)
)

const endpointWithEvidence = computed(() =>
  endpoints.value.filter(ep => ep.healthEvidence || ep.suggestedAction || ep.tokenPlanUsageSupported)
)

function stateColor(state: HealthState): string {
  const map: Record<HealthState, string> = {
    healthy: 'success',
    degraded: 'warning',
    limited: 'orange',
    misconfigured: 'deep-purple',
    dead: 'error',
    unknown: 'grey',
  }
  return map[state] ?? 'grey'
}

function stateIcon(state: HealthState): string {
  const map: Record<HealthState, string> = {
    healthy: 'mdi-heart-pulse',
    degraded: 'mdi-alert',
    limited: 'mdi-alert-circle-outline',
    misconfigured: 'mdi-shield-alert',
    dead: 'mdi-close-circle',
    unknown: 'mdi-information',
  }
  return map[state] ?? 'mdi-information'
}

function formatPercent(value?: number): string {
  if (value == null) return '-'
  return (value * 100).toFixed(1) + '%'
}

function rateColor(rate?: number): string {
  if (rate == null) return ''
  if (rate >= 0.95) return 'text-success'
  if (rate >= 0.8) return 'text-warning'
  return 'text-error'
}

// 与编辑渠道共用同一映射，保证全局 MiniMax 用量展示一致。
const minimaxQuotaItems = (ep: EndpointDetailItem) =>
  ep.miniMaxTokenPlanUsage ? buildMinimaxQuotaItems(ep.miniMaxTokenPlanUsage, t) : []

function formatDateTime(value: string): string {
  return new Date(value).toLocaleString()
}
</script>
