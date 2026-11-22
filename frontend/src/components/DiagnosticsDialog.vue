<template>
  <v-dialog v-model="open" max-width="900" scrollable>
    <v-card>
      <v-card-title class="d-flex align-center ga-2">
        <span>{{ t('diagnostics.title') }}</span>
        <v-spacer />
        <v-btn size="small" variant="text" :disabled="!report || loading" @click="exportReport">{{
          t('diagnostics.export')
        }}</v-btn>
        <v-btn
          icon="mdi-refresh"
          variant="text"
          size="small"
          :aria-label="t('keyOps.refresh')"
          :disabled="loading"
          @click="load"
        />
        <v-btn icon="mdi-close" variant="text" size="small" :aria-label="t('keyOps.close')" @click="open = false" />
      </v-card-title>
      <v-card-text>
        <v-progress-linear v-if="loading" indeterminate class="mb-3" />
        <v-alert v-if="error" type="error" variant="tonal" class="mb-3">{{ error }}</v-alert>
        <template v-if="report">
          <p class="text-body-2 diagnostics-version">
            {{ report.version.version }} · {{ report.version.gitCommit }} · {{ report.version.buildTime }}
          </p>
          <p class="text-caption text-medium-emphasis">
            {{
              t('diagnostics.configured', {
                status: t(report.persistenceConfigured ? 'diagnostics.enabled' : 'diagnostics.disabled')
              })
            }}
          </p>
          <v-alert
            v-if="report.persistenceConfigured && !report.metrics.persistent"
            type="warning"
            variant="tonal"
            density="compact"
            class="mb-3"
            >{{ t('diagnostics.initFailed') }}</v-alert
          >
          <div v-for="section in sections" :key="section.key" class="diagnostics-section">
            <div class="d-flex align-center ga-2 mb-2">
              <strong>{{ t(`diagnostics.section.${section.key}`) }}</strong>
              <v-chip size="x-small" :color="section.status.persistent ? 'success' : 'warning'" variant="tonal">{{
                t(section.status.persistent ? 'keyOps.persistent' : 'keyOps.memoryOnlyShort')
              }}</v-chip>
            </div>
            <v-alert v-if="section.status.queryFailed" type="error" variant="tonal" density="compact" class="mb-2">{{
              t('diagnostics.queryFailed')
            }}</v-alert>
            <dl class="diagnostics-values">
              <dt>{{ t('diagnostics.retention') }}</dt>
              <dd>{{ t('diagnostics.days', { days: section.status.retentionDays }) }}</dd>
              <dt>{{ t('diagnostics.records') }}</dt>
              <dd>{{ section.status.queryFailed ? '--' : section.status.recordCount }}</dd>
              <dt>{{ t('diagnostics.oldest') }}</dt>
              <dd>{{ date(section.status.oldestRecordAt) }}</dd>
              <dt>{{ t('diagnostics.buffered') }}</dt>
              <dd>{{ section.status.bufferedWrites }}</dd>
              <dt>{{ t('diagnostics.lastWrite') }}</dt>
              <dd>{{ date(section.status.lastWriteAt) }}</dd>
              <dt>{{ t('diagnostics.writeFailures') }}</dt>
              <dd :class="{ 'text-error': section.status.writeFailures }">{{ section.status.writeFailures }}</dd>
              <dt>{{ t('diagnostics.dropped') }}</dt>
              <dd :class="{ 'text-error': section.status.droppedWrites }">{{ section.status.droppedWrites }}</dd>
              <dt>{{ t('diagnostics.lastCleanup') }}</dt>
              <dd>
                {{ date(section.status.lastCleanupAt)
                }}<span v-if="section.status.lastCleanupAt">
                  · {{ t(section.status.lastCleanupOK ? 'keyOps.outcome.success' : 'keyOps.outcome.failed') }}</span
                >
              </dd>
              <dt>{{ t('diagnostics.cleanupFailures') }}</dt>
              <dd :class="{ 'text-error': section.status.cleanupFailures }">{{ section.status.cleanupFailures }}</dd>
            </dl>
          </div>
          <p class="text-caption text-medium-emphasis mt-3">{{ t('diagnostics.counterScope') }}</p>
          <p class="text-caption text-medium-emphasis">{{ t('diagnostics.exportScope') }}</p>
        </template>
      </v-card-text>
    </v-card>
  </v-dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api, type DiagnosticReport } from '../services/api'
import { useI18n } from '../i18n'

const props = defineProps<{ modelValue: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [boolean] }>()
const { t } = useI18n()
const open = computed({ get: () => props.modelValue, set: value => emit('update:modelValue', value) })
const report = ref<DiagnosticReport>()
const error = ref('')
const loading = ref(false)
let generation = 0
const sections = computed(() =>
  report.value
    ? (['metrics', 'requestHistory', 'keyOperations'] as const).map(key => ({ key, status: report.value![key] }))
    : []
)
const date = (value?: string) => (value ? new Date(value).toLocaleString() : '--')

async function load() {
  const current = ++generation
  error.value = ''
  report.value = undefined
  loading.value = true
  try {
    const result = await api.getDiagnostics()
    if (current === generation) report.value = result
  } catch (cause) {
    if (current === generation) error.value = cause instanceof Error ? cause.message : t('diagnostics.loadFailed')
  } finally {
    if (current === generation) loading.value = false
  }
}

function exportReport() {
  if (!report.value) return
  const url = URL.createObjectURL(new Blob([JSON.stringify(report.value, null, 2)], { type: 'application/json' }))
  const link = document.createElement('a')
  link.href = url
  link.download = `ccx-diagnostics-${report.value.generatedAt.slice(0, 10)}.json`
  document.body.appendChild(link)
  link.click()
  link.remove()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}
watch(
  () => props.modelValue,
  value => {
    generation++
    loading.value = false
    if (value) void load()
  },
  { immediate: true }
)
</script>

<style scoped>
.diagnostics-version {
  overflow-wrap: anywhere;
}
.diagnostics-section {
  padding: 14px 0;
  border-bottom: 1px solid rgba(var(--v-theme-on-surface), 0.12);
}
.diagnostics-values {
  display: grid;
  grid-template-columns: minmax(100px, 1fr) minmax(0, 2fr);
  gap: 6px 16px;
  font-size: 12px;
}
.diagnostics-values dt {
  color: rgba(var(--v-theme-on-surface), 0.65);
}
.diagnostics-values dd {
  margin: 0;
  text-align: right;
  overflow-wrap: anywhere;
}
</style>
