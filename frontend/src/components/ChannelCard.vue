<template>
  <v-card
    class="channel-card h-100"
    :style="serviceStyle"
    :data-pinned="channel.pinned"
    elevation="0"
    rounded="xl"
    hover
  >
    <!-- 渐变头部背景 -->
    <div class="card-header-gradient">
      <v-card-title class="d-flex align-center justify-space-between pa-4 pb-3 position-relative">
        <div class="d-flex align-center ga-3">
          <!-- 服务类型图标 -->
          <div class="service-icon-wrapper">
            <v-icon 
              :color="getServiceIconColor()"
              size="24"
            >
              {{ getServiceIcon() }}
            </v-icon>
          </div>
          <div class="d-flex align-center ga-2">
            <div>
              <!-- 渠道备注非空时悬停标题显示 -->
              <v-tooltip v-if="channel.remark?.trim()" :text="channel.remark" location="bottom" :open-delay="150" content-class="ccx-tooltip">
                <template #activator="{ props: tooltipProps }">
                  <div class="text-h6 font-weight-bold channel-title cursor-help" v-bind="tooltipProps">
                    {{ channel.name }}
                  </div>
                </template>
              </v-tooltip>
              <div v-else class="text-h6 font-weight-bold channel-title">
                {{ channel.name }}
              </div>
              <div class="text-caption text-high-emphasis opacity-80">
                {{ getServiceDisplayName() }}
              </div>
            </div>
            <!-- 官网图标按钮（紧贴标题右侧） -->
            <v-tooltip v-if="channel.website" :text="t('channelCard.openWebsite')" location="bottom" :open-delay="150" content-class="ccx-tooltip">
              <template #activator="{ props: tooltipProps }">
                <v-btn v-bind="tooltipProps" :href="channel.website" target="_blank" rel="noopener" size="small" variant="text" color="primary" icon>
                  <v-icon size="18">mdi-open-in-new</v-icon>
                </v-btn>
              </template>
            </v-tooltip>
          </div>
        </div>
        
        <div class="d-flex align-center ga-2">
          <!-- Pin 按钮 -->
          <v-btn
            size="small"
            :variant="channel.pinned ? 'tonal' : 'text'"
            :color="channel.pinned ? 'warning' : 'grey'"
            class="pin-btn"
            rounded="lg"
            @click="$emit('togglePin', channel.index)"
          >
            <v-icon size="16">
              {{ channel.pinned ? 'mdi-pin' : 'mdi-pin-outline' }}
            </v-icon>
          </v-btn>

          <v-chip
            :color="getServiceChipColor()"
            size="small"
            variant="tonal"
            density="comfortable"
            rounded="pill"
            class="service-chip"
          >
            <span class="font-weight-bold">{{ channel.serviceType.toUpperCase() }}</span>
          </v-chip>
          <!-- 健康状态 badge（§8.2） -->
          <ChannelHealthBadge :health="healthInfo ?? null" />
          <!-- 来源标签 chips（§8.2 标签系统） -->
          <v-chip
            v-for="tag in originTags"
            :key="tag.label"
            :color="tag.color"
            size="x-small"
            variant="tonal"
            density="comfortable"
            rounded="pill"
            class="origin-tag-chip"
          >
            <v-icon start size="12">{{ tag.icon }}</v-icon>
            {{ tag.label }}
          </v-chip>
          <!-- 用户自定义标签 -->
          <v-chip
            v-for="utag in channel.tags"
            :key="'user-' + utag"
            color="teal"
            size="x-small"
            variant="outlined"
            density="comfortable"
            rounded="pill"
            class="user-tag-chip"
          >
            <v-icon start size="12">mdi-tag</v-icon>
            {{ utag }}
          </v-chip>
          <!-- 自动托管徽标 -->
          <v-tooltip v-if="channel.autoManaged" :text="t('autopilot.badge.tooltip')" location="top" :open-delay="150" content-class="ccx-tooltip">
            <template #activator="{ props: autoProps }">
              <v-chip
                v-bind="autoProps"
                color="primary"
                size="x-small"
                variant="tonal"
                density="comfortable"
                rounded="pill"
                class="auto-managed-chip"
              >
                <v-icon start size="12">mdi-auto-fix</v-icon>
                {{ t('autopilot.badge.label') }}
              </v-chip>
            </template>
          </v-tooltip>
          <!-- Vision 能力按钮 -->
          <v-tooltip location="top" :text="channel.noVision ? t('channelCard.noVision') : t('channelCard.hasVision')" :open-delay="150" content-class="ccx-tooltip">
            <template #activator="{ props: tip }">
              <v-btn
                v-bind="tip"
                size="small"
                :variant="channel.noVision ? 'tonal' : 'text'"
                :color="channel.noVision ? 'warning' : undefined"
                rounded="lg"
                @click="$emit('toggleVision', channel.index)"
              >
                <v-icon size="16">
                  {{ channel.noVision ? 'mdi-eye-off' : 'mdi-eye' }}
                </v-icon>
              </v-btn>
            </template>
          </v-tooltip>
          <!-- 渠道状态芯片 -->
          <v-chip
            v-if="channel.status === 'disabled'"
            color="grey"
            size="small"
            variant="flat"
            density="comfortable"
            rounded="lg"
          >
            <v-icon start size="small">mdi-stop-circle</v-icon>
            {{ t('channelCard.disabled') }}
          </v-chip>
          <v-chip
            v-else-if="channel.status === 'partial'"
            color="warning"
            size="small"
            variant="flat"
            density="comfortable"
            rounded="lg"
          >
            <v-icon start size="small">mdi-alert-circle</v-icon>
            {{ t('status.partial') }}
          </v-chip>
          <v-chip
            v-else-if="channel.status === 'suspended'"
            color="warning"
            size="small"
            variant="flat"
            density="comfortable"
            rounded="lg"
          >
            <v-icon start size="small">mdi-pause-circle</v-icon>
            {{ t('channelCard.suspended') }}
          </v-chip>
        </div>
      </v-card-title>
    </div>

    <v-card-text class="px-4 py-2">
      <!-- 描述 -->
      <div v-if="channel.description" class="text-body-2 text-medium-emphasis mb-3">
        {{ channel.description }}
      </div>

      <!-- 基本信息 -->
      <div class="mb-4">
        <div class="d-flex align-center ga-2 mb-2">
          <v-icon size="16" color="medium-emphasis">mdi-web</v-icon>
          <span class="text-body-2 font-weight-medium">{{ t('addChannel.baseUrl') }}:</span>
          <div class="flex-1-1 text-truncate">
            <code class="text-caption bg-surface pa-1 rounded">{{ channel.baseUrl }}</code>
          </div>
        </div>

      </div>

      <!-- 状态和延迟（右对齐、间距更紧凑） -->
      <div class="d-flex align-center justify-end ga-4 mb-4">
        <div class="status-indicator">
          <v-tooltip :text="getStatusTooltip()" location="bottom" :open-delay="150" content-class="ccx-tooltip">
            <template #activator="{ props: tooltipProps }">
              <div class="status-badge cursor-help" v-bind="tooltipProps" :class="`status-${channel.status || 'unknown'}`">
                <v-icon 
                  :color="getStatusColor()"
                  size="16"
                  class="status-icon"
                >
                  {{ getStatusIcon() }}
                </v-icon>
                <span class="status-text">{{ getStatusText() }}</span>
              </div>
            </template>
          </v-tooltip>
        </div>
        <div v-if="channel.latency !== null" class="latency-indicator">
          <div class="latency-badge" :class="`latency-${getLatencyLevel()}`">
            <v-icon size="14" class="latency-icon">mdi-speedometer</v-icon>
            <span class="latency-text">{{ channel.latency }}ms</span>
          </div>
        </div>
      </div>

      <!-- API密钥管理 -->
      <v-expansion-panels variant="accordion" rounded="lg" class="mb-4">
        <v-expansion-panel>
          <v-expansion-panel-title>
            <div class="d-flex align-center justify-space-between w-100">
              <div class="d-flex align-center ga-2">
                <v-icon size="small">mdi-key-chain</v-icon>
                <span class="text-body-2 font-weight-medium">{{ t('channelCard.apiKeyManagement') }}</span>
              </div>
              <v-chip
                :color="channel.apiKeys.length ? 'secondary' : 'warning'"
                size="large"
                variant="tonal"
                density="comfortable"
                rounded="lg"
                class="mr-2 key-count-chip"
                :style="keyChipStyle"
              >
                <v-icon start size="18">mdi-key</v-icon>
                {{ channel.apiKeys.length }}
              </v-chip>
            </div>
          </v-expansion-panel-title>
          <v-expansion-panel-text>
            <div class="d-flex align-center justify-space-between mb-3">
              <span class="text-body-2 font-weight-medium">{{ t('channelCard.configuredKeys') }}</span>
              <v-btn
                size="small"
                color="primary"
                icon
                variant="elevated"
                rounded="lg"
                @click="$emit('addKey', channel.index)"
              >
                <v-icon>mdi-plus</v-icon>
              </v-btn>
            </div>
            
            <div v-if="channel.apiKeys.length" class="d-flex flex-column ga-2" style="max-height: 150px; overflow-y: auto;">
              <div
                v-for="(key, index) in channel.apiKeys"
                :key="index"
                class="d-flex align-center justify-space-between pa-2 bg-surface rounded"
              >
                <code class="text-caption flex-1-1 text-truncate mr-2">{{ maskApiKey(key) }}</code>
                <div class="d-flex align-center ga-1">
                  <!-- 暂停/恢复按钮 -->
                  <v-tooltip
                    v-if="isKeyPaused(key)"
                    :text="t('channelCard.resumeKey')"
                    location="top"
                    :open-delay="150"
                    content-class="ccx-tooltip"
                  >
                    <template #activator="{ props: tooltipProps }">
                      <v-btn
                        v-bind="tooltipProps"
                        size="x-small"
                        color="success"
                        icon
                        variant="text"
                        rounded="md"
                        @click="$emit('resumeKey', channel.index, key)"
                      >
                        <v-icon size="small">mdi-play</v-icon>
                      </v-btn>
                    </template>
                  </v-tooltip>
                  <v-tooltip
                    v-else
                    :text="t('channelCard.suspendKey')"
                    location="top"
                    :open-delay="150"
                    content-class="ccx-tooltip"
                  >
                    <template #activator="{ props: tooltipProps }">
                      <v-btn
                        v-bind="tooltipProps"
                        size="x-small"
                        color="warning"
                        icon
                        variant="text"
                        rounded="md"
                        @click="$emit('suspendKey', channel.index, key)"
                      >
                        <v-icon size="small">mdi-pause</v-icon>
                      </v-btn>
                    </template>
                  </v-tooltip>
                  <!-- 置顶按钮：仅最后一个 key 显示 -->
                  <v-tooltip v-if="index === channel.apiKeys.length - 1 && channel.apiKeys.length > 1" :text="t('channelCard.moveTop')" location="top" :open-delay="150" content-class="ccx-tooltip">
                    <template #activator="{ props: tooltipProps }">
                      <v-btn v-bind="tooltipProps" size="x-small" color="warning" icon variant="text" rounded="md" @click="$emit('moveKeyToTop', channel.index, key)">
                        <v-icon size="small">mdi-arrow-up-bold</v-icon>
                      </v-btn>
                    </template>
                  </v-tooltip>
                  <!-- 置底按钮：仅第一个 key 显示 -->
                  <v-tooltip v-if="index === 0 && channel.apiKeys.length > 1" :text="t('channelCard.moveBottom')" location="top" :open-delay="150" content-class="ccx-tooltip">
                    <template #activator="{ props: tooltipProps }">
                      <v-btn v-bind="tooltipProps" size="x-small" color="warning" icon variant="text" rounded="md" @click="$emit('moveKeyToBottom', channel.index, key)">
                        <v-icon size="small">mdi-arrow-down-bold</v-icon>
                      </v-btn>
                    </template>
                  </v-tooltip>
                  <v-tooltip :text="copiedKeyIndex === index ? t('channelCard.copied') : t('channelCard.copyKey')" location="top" :open-delay="150" content-class="ccx-tooltip">
                    <template #activator="{ props: tooltipProps }">
                      <v-btn
                        v-bind="tooltipProps"
                        size="x-small"
                        :color="copiedKeyIndex === index ? 'success' : 'primary'"
                        icon
                        variant="text"
                        rounded="md"
                        @click="copyApiKey(key, index)"
                      >
                        <v-icon size="small">{{ copiedKeyIndex === index ? 'mdi-check' : 'mdi-content-copy' }}</v-icon>
                      </v-btn>
                    </template>
                  </v-tooltip>
                  <v-btn
                    size="x-small"
                    color="error"
                    icon
                    variant="text"
                    rounded="md"
                    @click="$emit('removeKey', channel.index, getOriginalKey(key))"
                  >
                    <v-icon size="small">mdi-close</v-icon>
                  </v-btn>
                </div>
              </div>
            </div>
            
            <div v-else class="text-center py-4">
              <span class="text-body-2 text-medium-emphasis">{{ t('channelCard.noApiKeys') }}</span>
            </div>

            <!-- 被拉黑的 Key -->
            <div v-if="channel.disabledApiKeys?.length" class="mt-3">
              <div class="d-flex align-center ga-2 mb-2">
                <v-icon size="small" color="error">mdi-key-remove</v-icon>
                <span class="text-body-2 font-weight-medium text-error">{{ t('channelCard.disabledKeys') }}</span>
                <v-chip size="x-small" color="error" variant="tonal">{{ channel.disabledApiKeys.length }}</v-chip>
              </div>
              <div class="d-flex flex-column ga-2" style="max-height: 120px; overflow-y: auto;">
                <div
                  v-for="(dk, dkIndex) in channel.disabledApiKeys"
                  :key="'disabled-' + dkIndex"
                  class="d-flex align-center justify-space-between pa-2 rounded"
                  style="background: rgba(var(--v-theme-error), 0.06);"
                >
                  <div class="d-flex flex-column flex-1-1 mr-2" style="min-width: 0;">
                    <code class="text-caption text-truncate">{{ maskApiKey(dk.key) }}</code>
                    <div class="d-flex align-center ga-1 mt-1">
                      <v-chip size="x-small" :color="['insufficient_balance', 'insufficient_quota'].includes(dk.reason) ? 'warning' : 'error'" variant="tonal">
                        {{ t(getDisabledKeyReasonLabel(dk.reason)) }}
                      </v-chip>
                      <span class="text-caption text-medium-emphasis">{{ formatDisabledTime(dk.disabledAt) }}</span>
                    </div>
                  </div>
                  <v-tooltip :text="t('channelCard.restoreKey')" location="top" :open-delay="150" content-class="ccx-tooltip">
                    <template #activator="{ props: tooltipProps }">
                      <v-btn
                        v-bind="tooltipProps"
                        size="x-small"
                        color="success"
                        icon
                        variant="text"
                        rounded="md"
                        @click="$emit('restoreKey', channel.index, dk.key)"
                      >
                        <v-icon size="small">mdi-restore</v-icon>
                      </v-btn>
                    </template>
                  </v-tooltip>
                </div>
              </div>
            </div>
          </v-expansion-panel-text>
        </v-expansion-panel>
      </v-expansion-panels>

      <!-- 操作按钮 -->
      <div class="action-buttons d-flex flex-wrap ga-2 justify-end w-100">
        <v-btn
          size="small"
          color="info"
          variant="outlined"
          rounded="lg"
          class="action-btn"
          prepend-icon="mdi-pencil"
          @click="$emit('edit', channel)"
        >
          {{ t('orchestration.edit') }}
        </v-btn>
        
        <v-btn
          size="small"
          color="error"
          variant="text"
          rounded="lg"
          class="action-btn danger-action"
          prepend-icon="mdi-delete"
          @click="$emit('delete', channel.index)"
        >
          {{ t('orchestration.delete') }}
        </v-btn>
      </div>
    </v-card-text>
  </v-card>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import type { Channel } from '../services/api'
import type { ChannelHealthItem } from '../services/api-types'
import { useI18n } from '../i18n'
import { maskApiKey } from '../utils/apiKeyMask'
import ChannelHealthBadge from './ChannelHealthBadge.vue'

const disabledKeyReasonLabelMap = {
  insufficient_balance: 'channelCard.blacklistReason.insufficient_balance',
  insufficient_quota: 'channelCard.blacklistReason.insufficient_quota',
  unavailable: 'channelCard.blacklistReason.unavailable',
  rate_limited: 'channelCard.blacklistReason.rate_limited',
  invalid: 'channelCard.blacklistReason.invalid',
  authentication_error: 'channelCard.blacklistReason.authentication_error',
  permission_error: 'channelCard.blacklistReason.permission_error',
  unknown: 'channelCard.blacklistReason.unknown',
} as const

interface Props {
  channel: Channel
  healthInfo?: ChannelHealthItem | null
}

const props = defineProps<Props>()
const { t } = useI18n()

const getDisabledKeyReasonLabel = (reason?: string) => {
  return disabledKeyReasonLabelMap[reason as keyof typeof disabledKeyReasonLabelMap] || disabledKeyReasonLabelMap.unknown
}

const copiedKeyIndex = ref<number | null>(null)

defineEmits<{
  edit: [channel: Channel]
  delete: [channelId: number]
  addKey: [channelId: number]
  removeKey: [channelId: number, apiKey: string]
  restoreKey: [channelId: number, apiKey: string]
  suspendKey: [channelId: number, apiKey: string]
  resumeKey: [channelId: number, apiKey: string]
  moveKeyToTop: [channelId: number, apiKey: string]
  moveKeyToBottom: [channelId: number, apiKey: string]
  togglePin: [channelId: number]
  toggleVision: [channelId: number]
}>()

const isKeyPaused = (apiKey: string) => {
  const config = props.channel.apiKeyConfigs?.find(cfg => cfg.key === apiKey)
  return config?.enabled === false
}

// 获取服务类型对应的芯片颜色
const getServiceChipColor = () => {
  const colorMap: Record<string, string> = {
    openai: 'info',
    claude: 'success',
    gemini: 'accent'
  }
  return colorMap[props.channel.serviceType] || 'primary'
}

// 获取状态对应的颜色
const getStatusColor = () => {
  const colorMap: Record<string, string> = {
    'healthy': 'success',
    'partial': 'warning',
    'error': 'error',
    'unknown': 'warning'
  }
  return colorMap[props.channel.status || 'unknown']
}

// 获取状态图标
const getStatusIcon = () => {
  const iconMap: Record<string, string> = {
    'healthy': 'mdi-check-circle',
    'partial': 'mdi-alert-circle',
    'error': 'mdi-alert-circle',
    'unknown': 'mdi-help-circle'
  }
  return iconMap[props.channel.status || 'unknown']
}

// 获取状态文本
const getStatusText = () => {
  const status = props.channel.status || 'unknown'
  if (status === 'healthy') return t('channelCard.statusHealthy')
  if (status === 'partial') return t('status.partial')
  if (status === 'error') return t('channelCard.statusError')
  return t('channelCard.notChecked')
}

// 状态解释文案（悬浮提示）
const getStatusTooltip = () => {
  const status = props.channel.status || 'unknown'
  if (status === 'healthy') return t('channelCard.tooltipHealthy')
  if (status === 'partial') return t('status.partial')
  if (status === 'error') return t('channelCard.tooltipError')
  return t('channelCard.tooltipUnknown')
}

// 获取原始密钥（用于删除操作），现在直接传递原始密钥
const getOriginalKey = (originalKey: string) => {
  return originalKey
}

// 格式化拉黑时间
const formatDisabledTime = (isoStr: string): string => {
  try {
    const d = new Date(isoStr)
    return d.toLocaleDateString() + ' ' + d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  } catch {
    return isoStr
  }
}

// 复制API密钥到剪贴板
const copyApiKey = async (key: string, index: number) => {
  try {
    await navigator.clipboard.writeText(key)
    copiedKeyIndex.value = index

    // 2秒后重置复制状态
    setTimeout(() => {
      copiedKeyIndex.value = null
    }, 2000)
  } catch (err) {
    console.error(t('channelCard.copyKey'), err)
    // 降级方案：使用传统的复制方法
    const textArea = document.createElement('textarea')
    textArea.value = key
    textArea.style.position = 'fixed'
    textArea.style.left = '-999999px'
    textArea.style.top = '-999999px'
    document.body.appendChild(textArea)
    textArea.focus()
    textArea.select()

    try {
      document.execCommand('copy')
      copiedKeyIndex.value = index

      setTimeout(() => {
        copiedKeyIndex.value = null
      }, 2000)
    } catch (err) {
      console.error(t('channelCard.copyKey'), err)
    } finally {
      textArea.remove()
    }
  }
}

// 获取服务类型图标
const getServiceIcon = () => {
  const iconMap: Record<string, string> = {
    'openai': 'mdi-robot',
    'claude': 'mdi-message-processing',
    'gemini': 'mdi-diamond-stone'
  }
  return iconMap[props.channel.serviceType] || 'mdi-api'
}

// 获取服务类型图标颜色
const getServiceIconColor = () => {
  const colorMap: Record<string, string> = {
    'openai': 'primary',
    'claude': 'orange',
    'gemini': 'purple'
  }
  return colorMap[props.channel.serviceType] || 'grey'
}

// 获取服务类型显示名称
const getServiceDisplayName = () => {
  const nameMap: Record<string, string> = {
    'openai': 'OpenAI API',
    'claude': 'Claude API',
    'gemini': 'Gemini API'
  }
  return nameMap[props.channel.serviceType] || 'Custom API'
}

// 获取延迟等级
const getLatencyLevel = () => {
  if (!props.channel.latency) return 'unknown'
  
  if (props.channel.latency < 200) return 'excellent'
  if (props.channel.latency < 500) return 'good'
  if (props.channel.latency < 1000) return 'fair'
  return 'poor'
}

// Chip 动态文本颜色，避免在浅色背景上出现近黑文本
const keyChipStyle = computed(() => {
  const hasKeys = props.channel.apiKeys.length > 0
  return {
    color: hasKeys ? 'rgb(var(--v-theme-on-secondary))' : 'rgb(var(--v-theme-on-warning))',
    fontSize: '0.95rem'
  }
})

// Origin / pool tags derived from health info (§8.2 标签系统).
// originTier and poolTag may be absent in current API; tags render only when present.
interface OriginTag { label: string; color: string; icon: string }

const originTags = computed<OriginTag[]>(() => {
  const tags: OriginTag[] = []
  const h = props.healthInfo
  if (!h) return tags

  // Origin tier tag
  if (h.originTier && h.originTier !== 'unknown') {
    const tierMap: Record<string, { key: string; color: string; icon: string }> = {
      first:  { key: 'channelHealth.originOfficial', color: 'blue',   icon: 'mdi-shield-check' },
      second: { key: 'channelHealth.originRelay',    color: 'indigo', icon: 'mdi-account-group' },
      third:  { key: 'channelHealth.originCommunity', color: 'green',  icon: 'mdi-hand-heart' },
    }
    const cfg = tierMap[h.originTier]
    if (cfg) {
      tags.push({ label: t(cfg.key), color: cfg.color, icon: cfg.icon })
    }
  }

  // Pool tag
  if (h.poolTag) {
    const poolMap: Record<string, { key: string; color: string; icon: string }> = {
      free: { key: 'channelHealth.poolFree', color: 'green',  icon: 'mdi-gift' },
      temp: { key: 'channelHealth.poolTemp', color: 'orange', icon: 'mdi-clock-alert' },
    }
    const cfg = poolMap[h.poolTag]
    if (cfg) {
      tags.push({ label: t(cfg.key), color: cfg.color, icon: cfg.icon })
    }
  }

  return tags
})

// 根据服务类型设置卡片强调色（明暗模式自动随主题变量变更）
const serviceStyle = computed(() => {
  const map: Record<string, string> = {
    openai: 'var(--v-theme-info)',
    claude: 'var(--v-theme-success)',
    gemini: 'var(--v-theme-accent)'
  }
  const value = map[props.channel.serviceType] || 'var(--v-theme-primary)'
  return {
    '--card-accent-rgb': value
  } as Record<string, string>
})
</script>

<style scoped>
/* --- BASE STYLES (LIGHT MODE) --- */
.channel-card {
  transition: all 0.4s cubic-bezier(0.4, 0, 0.2, 1);
  position: relative;
  overflow: hidden;
  /* 类型底色：在 surface 上叠加轻度同色系着色 */
  background: linear-gradient(
    0deg,
    rgba(var(--card-accent-rgb, var(--v-theme-primary)), 0.06),
    rgba(var(--card-accent-rgb, var(--v-theme-primary)), 0.06)
  ), rgb(var(--v-theme-surface));
  border: 1px solid rgba(var(--card-accent-rgb, var(--v-theme-primary)), 0.28);
  box-shadow: 
    0 4px 16px rgba(0, 0, 0, 0.05),
    0 1px 4px rgba(0, 0, 0, 0.02);
  border-radius: 16px;
}

/* 左侧彩色强调条，突出渠道类型颜色 */
.channel-card::before {
  content: '';
  position: absolute;
  left: 0;
  top: 0;
  bottom: 0;
  width: 6px;
  background: linear-gradient(
    to bottom,
    rgba(var(--card-accent-rgb, var(--v-theme-primary)), 0.9),
    rgba(var(--card-accent-rgb, var(--v-theme-primary)), 0.5)
  );
}

.channel-card:not(:hover) {
  /* default state */
}

.channel-card:hover {
  /* 不做 translate/scale：位移会改变卡片自身 hitbox，
     指针停在下边界时会陷入 hover/非 hover 振荡，表现为边框抖动。
     悬停反馈仅用阴影加深 + 边框提亮，与暗色主题保持一致。 */
  box-shadow:
    0 16px 32px rgba(0, 0, 0, 0.08),
    0 6px 18px rgba(0, 0, 0, 0.05);
  border-color: rgba(var(--card-accent-rgb, var(--v-theme-primary)), 0.45);
}

.card-header-gradient {
  background: linear-gradient(135deg,
    rgba(var(--card-accent-rgb, var(--v-theme-primary)), 0.20) 0%,
    rgba(var(--card-accent-rgb, var(--v-theme-primary)), 0.10) 50%,
    rgba(var(--v-theme-accent), 0.12) 100%);
  position: relative;
  border-top-left-radius: inherit;
  border-top-right-radius: inherit;
}

.service-icon-wrapper {
  width: 48px;
  height: 48px;
  border-radius: 12px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: linear-gradient(135deg,
    rgba(var(--card-accent-rgb, var(--v-theme-primary)), 0.18) 0%,
    rgba(var(--card-accent-rgb, var(--v-theme-primary)), 0.10) 100%);
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.08);
  border: 1px solid rgba(var(--card-accent-rgb, var(--v-theme-primary)), 0.25);
  transition: all 0.3s ease;
}

.channel-card:hover .service-icon-wrapper {
  transform: scale(1.1);
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.12);
}

.service-chip {
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.06);
  border: none;
}

.origin-tag-chip {
  font-weight: 600;
  letter-spacing: 0;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.05);
}

.auto-managed-chip {
  font-weight: 600;
  letter-spacing: 0;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.05);
  cursor: help;
}

/* --- INDICATORS (LIGHT) --- */
.status-badge, .latency-badge {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 4px 8px;
  border-radius: 8px;
  font-size: 0.75rem;
  font-weight: 500;
}

.status-badge {
  background-color: rgba(0, 0, 0, 0.05);
}
.status-badge.status-healthy { color: rgb(var(--v-theme-success)); background-color: rgba(var(--v-theme-success), 0.12); }
.status-badge.status-partial { color: rgb(var(--v-theme-warning)); background-color: rgba(var(--v-theme-warning), 0.12); }
.status-badge.status-error { color: rgb(var(--v-theme-error)); background-color: rgba(var(--v-theme-error), 0.12); }
.status-badge.status-unknown { color: rgb(var(--v-theme-secondary)); background-color: rgba(var(--v-theme-secondary), 0.12); }

.latency-badge {
  font-weight: 600;
}
.latency-badge.latency-excellent { color: #2e7d32; background: rgba(76, 175, 80, 0.1); }
.latency-badge.latency-good { color: #f57c00; background: rgba(255, 193, 7, 0.1); }
.latency-badge.latency-fair { color: #e65100; background: rgba(255, 152, 0, 0.1); }
.latency-badge.latency-poor { color: #c62828; background: rgba(244, 67, 54, 0.1); }

/* --- PIN BUTTON (LIGHT) --- */
.pin-btn {
  min-width: 32px !important; width: 32px; height: 32px;
  border-radius: 12px !important;
  transition: all 0.3s cubic-bezier(0.4, 0, 0.2, 1);
}

.pin-btn:hover {
  transform: scale(1.1);
}

.key-count-chip {
  font-weight: 700;
}

/* --- KEYFRAMES --- */
@keyframes shimmer {
  0% { transform: translateX(-100%); }
  100% { transform: translateX(100%); }
}

@keyframes slideInUp {
  from { opacity: 0; transform: translateY(30px); }
  to { opacity: 1; transform: translateY(0); }
}

.channel-card {
  animation: slideInUp 0.6s ease-out;
}

/* 
██████╗ ██╗  ██╗██████╗  ██╗  ██╗
██╔══██╗██║  ██║██╔══██╗██║ ██╔╝
██║  ██║███████║██████╔╝█████╔╝ 
██║  ██║██╔══██║██╔══██╗██╔═██╗ 
██████╔╝██║  ██║██║  ██║██║  ██╗
╚═════╝ ╚═╝  ╚═╝╚═╝  ╚═╝╚═╝  ╚═╝
*/
/* Prefer Vuetify theme class over media query to honor manual toggles */
.v-theme--dark .channel-card {
  /* 暗色下加深类型底色透明度，保证可见 */
  background: linear-gradient(
    0deg,
    rgba(var(--card-accent-rgb, var(--v-theme-primary)), 0.12),
    rgba(var(--card-accent-rgb, var(--v-theme-primary)), 0.12)
  ), rgb(var(--v-theme-surface));
  border: 1px solid rgba(var(--card-accent-rgb, var(--v-theme-primary)), 0.45);
  box-shadow:
    0 4px 24px rgba(0, 0, 0, 0.28),
    0 1px 8px rgba(0, 0, 0, 0.18);
}

.v-theme--dark .channel-card:not(.current-channel):hover {
  border-color: rgba(var(--card-accent-rgb, var(--v-theme-primary)), 0.65);
  box-shadow:
    0 20px 40px rgba(0, 0, 0, 0.36),
    0 8px 24px rgba(0, 0, 0, 0.24);
}

.v-theme--dark .card-header-gradient {
  background: linear-gradient(135deg,
    rgba(var(--card-accent-rgb, var(--v-theme-primary)), 0.28) 0%,
    rgba(var(--card-accent-rgb, var(--v-theme-primary)), 0.16) 50%,
    rgba(156, 39, 176, 0.18) 100%);
}

.v-theme--dark .service-icon-wrapper {
  background: linear-gradient(135deg,
    rgba(var(--card-accent-rgb, var(--v-theme-primary)), 0.25) 0%,
    rgba(var(--card-accent-rgb, var(--v-theme-primary)), 0.15) 100%);
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.24);
  border: 1px solid rgba(var(--card-accent-rgb, var(--v-theme-primary)), 0.35);
}

.v-theme--dark .channel-card::before {
  background: linear-gradient(
    to bottom,
    rgba(var(--card-accent-rgb, var(--v-theme-primary)), 0.95),
    rgba(var(--card-accent-rgb, var(--v-theme-primary)), 0.6)
  );
}

.v-theme--dark .channel-card:hover .service-icon-wrapper {
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.32);
  border-color: rgba(255, 255, 255, 0.2);
}

.v-theme--dark .service-chip {
  border: none;
}

/* --- INDICATORS (DARK) --- */
.v-theme--dark .status-badge {
  background-color: rgba(255, 255, 255, 0.1);
}
.v-theme--dark .status-badge.status-healthy { color: #b6e3be; background-color: rgba(52, 211, 153, 0.2); }
.v-theme--dark .status-badge.status-partial { color: #fcd34d; background-color: rgba(245, 158, 11, 0.2); }
.v-theme--dark .status-badge.status-error { color: #f4b4b4; background-color: rgba(248, 113, 113, 0.22); }
.v-theme--dark .status-badge.status-unknown { color: #cbd5e1; background-color: rgba(148, 163, 184, 0.2); }

.v-theme--dark .latency-badge.latency-excellent { color: #b6e3be; background: rgba(52, 211, 153, 0.25); }
.v-theme--dark .latency-badge.latency-good { color: #fde68a; background: rgba(251, 191, 36, 0.22); }
.v-theme--dark .latency-badge.latency-fair { color: #fcd49b; background: rgba(251, 146, 60, 0.25); }
.v-theme--dark .latency-badge.latency-poor { color: #f4b4b4; background: rgba(248, 113, 113, 0.28); }

/* --- MOBILE RESPONSIVE --- */
@media (max-width: 720px) {
  .card-header-gradient .v-card-title {
    flex-direction: column;
    align-items: flex-start !important;
    gap: 12px;
  }

  .card-header-gradient .v-card-title > div:first-child {
    width: 100%;
  }

  .card-header-gradient .v-card-title > div:last-child {
    width: 100%;
    justify-content: flex-start !important;
    flex-wrap: wrap;
  }

  .service-icon-wrapper {
    width: 40px;
    height: 40px;
  }

  .service-icon-wrapper .v-icon {
    font-size: 20px !important;
  }

  .channel-title {
    font-size: 1rem !important;
    max-width: 16ch;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .action-buttons {
    justify-content: flex-start !important;
  }

  .action-btn {
    flex: 1 1 auto;
    min-width: 0;
  }
}
</style>
