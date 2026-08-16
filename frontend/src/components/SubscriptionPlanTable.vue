<template>
  <div class="subscription-plan-table">
    <v-table density="compact" hover>
      <thead>
        <tr>
          <th class="text-left">{{ t('subscription.field.originType') }}</th>
          <th class="text-left">{{ t('subscription.field.name') }}</th>
          <th class="text-left">{{ t('subscription.field.balance') }}</th>
          <th class="text-left">{{ t('subscription.billingTerms.title') }}</th>
          <th class="text-left">{{ t('subscription.field.version') }}</th>
          <th class="text-left">{{ t('subscription.field.linkedChannels') }}</th>
          <th class="text-left">{{ t('subscription.field.source') }}</th>
          <th class="text-left" style="width: 160px;">{{ t('subscription.field.autoRefreshSection') }}</th>
          <th class="text-right" style="width: 160px;">{{ t('app.actions.edit') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="item in subscriptions" :key="item.subscriptionUid">
          <!-- 来源类型 -->
          <td>
            <v-chip
              size="small"
              :color="getOriginTypeColor(item.originType)"
              variant="tonal"
            >
              {{ getOriginTypeLabel(item.originType) }}
            </v-chip>
          </td>

          <!-- 名称 -->
          <td>
            <div class="font-weight-medium">{{ item.displayName }}</div>
            <div v-if="item.provider" class="text-caption text-medium-emphasis">{{ item.provider }}</div>
            <div v-if="item.maxGroupMultiplier !== undefined && item.provisionedKeys?.length" class="text-caption text-medium-emphasis">
              {{ t('subscription.newApi.activeGroupSummary', { limit: item.maxGroupMultiplier, count: item.provisionedKeys.length }) }}
            </div>
          </td>

          <!-- 余额 -->
          <td>
            <span v-if="item.balance !== undefined && item.balance !== null">
              {{ formatBalance(item.balance, item.currency) }}
            </span>
            <span v-else class="text-medium-emphasis">-</span>
          </td>

          <!-- 充值到账（币种/金额） -->
          <td>{{ billingTermsPreview(item) }}</td>
          <td>{{ item.version }}</td>
          <td>
            <div v-if="item.linkedChannelUids && item.linkedChannelUids.length > 0" class="d-flex flex-wrap ga-1">
              <v-chip
                v-for="ch in item.linkedChannelUids.slice(0, 3)"
                :key="ch"
                size="x-small"
                variant="outlined"
                color="primary"
              >
                {{ ch }}
              </v-chip>
              <v-chip
                v-if="item.linkedChannelUids.length > 3"
                size="x-small"
                variant="outlined"
                color="grey"
              >
                +{{ item.linkedChannelUids.length - 3 }}
              </v-chip>
            </div>
            <span v-else class="text-caption text-medium-emphasis">{{ t('subscription.noChannels') }}</span>
          </td>

          <!-- 来源 -->
          <td>
            <v-chip size="x-small" variant="tonal" :color="item.source === 'auto_discovered' ? 'info' : 'grey'">
              {{ getSourceLabel(item.source) }}
            </v-chip>
          </td>

          <!-- 自动刷新状态 -->
          <td>
            <div v-if="item.autoRefreshEnabled && item.autoRefreshSupported" class="d-flex align-center ga-1">
              <v-icon size="14" :color="item.lastBalanceRefreshError ? 'error' : 'success'">
                {{ item.lastBalanceRefreshError ? 'mdi-alert-circle' : 'mdi-check-circle' }}
              </v-icon>
              <span v-if="item.lastBalanceRefreshAt" class="text-caption">
                {{ formatRefreshTime(item.lastBalanceRefreshAt) }}
              </span>
              <span v-else class="text-caption text-medium-emphasis">-</span>
            </div>
            <div v-else-if="item.autoRefreshEnabled && !item.autoRefreshSupported" class="text-caption text-medium-emphasis">
              <v-icon size="14" color="grey">mdi-information</v-icon>
              N/A
            </div>
            <span v-else class="text-caption text-medium-emphasis">-</span>
          </td>

          <!-- 操作 -->
          <td class="text-right">
            <v-tooltip v-if="item.autoRefreshEnabled && item.autoRefreshSupported" text="Refresh" location="top">
              <template #activator="{ props }">
                <v-btn v-bind="props" icon size="small" variant="text" color="primary" @click="$emit('refresh', item)">
                  <v-icon size="18">mdi-refresh</v-icon>
                </v-btn>
              </template>
            </v-tooltip>
            <v-tooltip :text="t('subscription.linkChannel')" location="top">
              <template #activator="{ props }">
                <v-btn v-bind="props" icon size="small" variant="text" color="primary" @click="$emit('link', item)">
                  <v-icon size="18">mdi-link</v-icon>
                </v-btn>
              </template>
            </v-tooltip>
            <v-btn icon size="small" variant="text" @click="$emit('edit', item)">
              <v-icon size="18">mdi-pencil</v-icon>
            </v-btn>
            <v-btn icon size="small" variant="text" color="error" @click="$emit('delete', item)">
              <v-icon size="18">mdi-delete</v-icon>
            </v-btn>
          </td>
        </tr>
      </tbody>
    </v-table>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from '@/i18n'
import type { SubscriptionItem } from '@/services/api-types'
import { billingTermsPreview } from '@/utils/subscriptionBilling'

defineProps<{
  subscriptions: SubscriptionItem[]
}>()

defineEmits<{
  edit: [item: SubscriptionItem]
  delete: [item: SubscriptionItem]
  refresh: [item: SubscriptionItem]
  link: [item: SubscriptionItem]
}>()

const { t } = useI18n()

function getOriginTypeColor(originType?: string): string {
  switch (originType) {
    case 'official_api': return 'success'
    case 'official_token_plan': return 'primary'
    case 'relay': return 'warning'
    case 'public_benefit': return 'info'
    default: return 'grey'
  }
}

function getOriginTypeLabel(originType?: string): string {
  if (!originType) return '-'
  const key = `subscription.originType.${originType}`
  return t(key)
}

function formatBalance(balance?: number, currency?: string): string {
  if (balance === undefined || balance === null) return '-'
  const code = currency || ''
  return `${code} ${balance.toFixed(2)}`
}

function getSourceLabel(source?: string): string {
  if (!source) return '-'
  const key = `subscription.source.${source}`
  return t(key)
}

function formatRefreshTime(isoStr?: string): string {
  if (!isoStr) return '-'
  try {
    const d = new Date(isoStr)
    return d.toLocaleString()
  } catch {
    return isoStr
  }
}
</script>
