<template>
  <v-dialog v-model="visible" max-width="480" persistent>
    <v-card v-if="state === 'confirming'">
      <v-card-title class="d-flex align-center pa-4">
        <v-icon size="24" class="mr-3" color="warning">mdi-alert</v-icon>
        <span>{{ t('app.version.updateAvailable') }}</span>
      </v-card-title>
      <v-card-text class="pa-4 pt-0">
        <div class="d-flex align-center justify-center my-4">
          <span class="text-h5 font-weight-bold">{{ versionInfo.currentVersion }}</span>
          <v-icon size="28" class="mx-3" color="warning">mdi-arrow-right-bold</v-icon>
          <span class="text-h5 font-weight-bold" color="primary">{{ versionInfo.latestVersion }}</span>
        </div>
        <div v-if="publishedAt" class="text-center text-body-2 text-medium-emphasis mb-4">
          {{ t('app.version.publishedAt') }}: {{ publishedAt }}
        </div>
        <v-btn
          v-if="releaseUrl"
          variant="text"
          block
          :href="releaseUrl"
          target="_blank"
          rel="noopener noreferrer"
          prepend-icon="mdi-open-in-new"
          class="text-none"
        >
          {{ t('app.version.releaseNotes') }}
        </v-btn>
      </v-card-text>
      <v-card-actions class="pa-4 pt-0">
        <v-spacer />
        <v-btn variant="text" @click="cancel">
          {{ t('app.version.later') }}
        </v-btn>
        <v-btn color="primary" variant="elevated" @click="confirm" :loading="loading">
          {{ t('app.version.updateNow') }}
        </v-btn>
      </v-card-actions>
    </v-card>

    <v-card v-else-if="state === 'downloading'">
      <v-card-title class="d-flex align-center pa-4">
        <v-progress-circular indeterminate :size="20" :width="2" class="mr-3" />
        <span>{{ progressText }}</span>
      </v-card-title>
      <v-card-text class="pa-4 pt-0">
        <v-progress-linear :model-value="progress" color="primary" height="6" rounded />
      </v-card-text>
    </v-card>

    <v-card v-else-if="state === 'success'">
      <v-card-title class="d-flex align-center pa-4">
        <v-icon size="24" class="mr-3" color="success">mdi-check-circle</v-icon>
        <span>{{ t('app.version.updateSuccess') }}</span>
      </v-card-title>
      <v-card-text class="pa-4 pt-0">
        <div class="text-center my-4">
          <span class="text-h5 font-weight-bold" color="success">{{ versionInfo.latestVersion || versionInfo.currentVersion }}</span>
        </div>
        <v-btn
          v-if="releaseUrl"
          variant="text"
          block
          :href="releaseUrl"
          target="_blank"
          rel="noopener noreferrer"
          prepend-icon="mdi-open-in-new"
          class="text-none"
        >
          {{ t('app.version.releaseNotes') }}
        </v-btn>
      </v-card-text>
      <v-card-actions class="pa-4 pt-0">
        <v-spacer />
        <v-btn color="primary" variant="elevated" @click="close">
          {{ t('app.version.close') }}
        </v-btn>
      </v-card-actions>
    </v-card>

    <v-card v-else-if="state === 'error'">
      <v-card-title class="d-flex align-center pa-4">
        <v-icon size="24" class="mr-3" color="error">mdi-alert-circle</v-icon>
        <span>{{ t('app.version.updateFailed') }}</span>
      </v-card-title>
      <v-card-text class="pa-4 pt-0">
        <v-alert type="error" variant="tonal" class="mb-0">
          {{ errorMessage }}
        </v-alert>
      </v-card-text>
      <v-card-actions class="pa-4 pt-0">
        <v-spacer />
        <v-btn color="primary" variant="elevated" @click="close">
          {{ t('app.version.close') }}
        </v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useI18n } from '../i18n'
import { versionService } from '../services/version'
import { useSystemStore } from '../stores/system'
import type { VersionInfo } from '../services/version'

const props = defineProps<{
  modelValue: boolean
  versionInfo: VersionInfo
}>()

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  'update-success': []
}>()

const { t } = useI18n()
const systemStore = useSystemStore()

const loading = ref(false)
const state = ref<'confirming' | 'downloading' | 'success' | 'error'>('confirming')
const errorMessage = ref('')
const progressText = ref('')
const progress = ref(0)
const pollTimer = ref<ReturnType<typeof setInterval> | null>(null)

const releaseUrl = computed(() => props.versionInfo.releaseUrl || '')

function formatPublishedDate(isoString: string | null): string {
  if (!isoString) return ''
  try {
    const d = new Date(isoString)
    return d.toLocaleDateString()
  } catch {
    return isoString
  }
}
const publishedAt = computed(() => formatPublishedDate(
  props.versionInfo.latestVersion ? (props.versionInfo.publishedAt ?? null) : null
))

const visible = computed({
  get: () => props.modelValue,
  set: (v: boolean) => emit('update:modelValue', v),
})

watch(() => props.modelValue, (v) => {
  if (v) reset()
})

function reset() {
  state.value = 'confirming'
  loading.value = false
  errorMessage.value = ''
  progressText.value = ''
  progress.value = 0
  if (pollTimer.value) {
    clearInterval(pollTimer.value)
    pollTimer.value = null
  }
}

function cancel() {
  visible.value = false
}

function close() {
  if (pollTimer.value) {
    clearInterval(pollTimer.value)
    pollTimer.value = null
  }
  visible.value = false
}

async function confirm() {
  loading.value = true
  state.value = 'downloading'
  progressText.value = t('app.version.downloading')
  progress.value = 0

  const result = await versionService.triggerUpdate()
  if (result.status === 'error') {
    state.value = 'error'
    errorMessage.value = result.message
    loading.value = false
    return
  }

  // Poll /api/version/status for progress, fall back to /health for completion
  let attempts = 0
  const maxAttempts = 120 // 120 * 2s = 240s max
  pollTimer.value = setInterval(async () => {
    attempts++

    try {
      // Check update status from backend
      const statusResult = await versionService.checkUpdateStatus()

      if (statusResult.status === 'failed') {
        clearInterval(pollTimer.value!)
        pollTimer.value = null
        state.value = 'error'
        errorMessage.value = statusResult.error || t('app.version.updateFailed')
        loading.value = false
        return
      }

      if (statusResult.status === 'idle') {
        // Already at latest: treat as success
        clearInterval(pollTimer.value!)
        pollTimer.value = null
        state.value = 'success'
        loading.value = false
        return
      }

      progress.value = statusResult.progress ?? 0

      if (statusResult.status === 'downloading') {
        progressText.value = t('app.version.downloading')
      } else if (statusResult.status === 'verifying') {
        progressText.value = t('app.version.verifying')
      }

      // Also poll /health to detect when the server comes back with new version
      try {
        const { fetchHealth } = await import('../services/api')
        const health = await fetchHealth()
        const newVersion = health.version?.version || ''
        if (newVersion && newVersion !== props.versionInfo.currentVersion) {
          clearInterval(pollTimer.value!)
          pollTimer.value = null
          state.value = 'success'
          loading.value = false
          systemStore.setCurrentVersion(newVersion)
          systemStore.setVersionInfo({
            ...systemStore.versionInfo,
            currentVersion: newVersion,
            status: 'latest',
          })
          emit('update-success')
          return
        }
      } catch {
        // Server is restarting, keep polling
      }
    } catch {
      // Status endpoint may be unavailable during restart
    }

    if (attempts >= maxAttempts) {
      clearInterval(pollTimer.value!)
      pollTimer.value = null
      state.value = 'error'
      errorMessage.value = '更新超时：服务未在预期时间内完成重启'
      loading.value = false
    }
  }, 2000)
}
</script>
