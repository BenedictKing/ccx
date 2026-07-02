<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Alert } from '@/components/ui/alert'
import { Check, RefreshCw, Save, X, ExternalLink } from 'lucide-vue-next'
import { useEnvFile } from '@/composables/useEnvFile'
import { useLanguage } from '@/composables/useLanguage'
import { detectEnvNewline, getEnvFieldValue, parseEnvFile, serializeEnvFile, type EnvEntry } from '@/lib/env-file'

type FieldType = 'text' | 'password' | 'number' | 'select'

type EnvField = {
  key: string
  label: string
  type: FieldType
  defaultValue: string
  description?: string
  placeholder?: string
  options?: Array<{ label: string; value: string }>
  min?: number
  max?: number
  step?: number | string
  required?: boolean
  disallow?: string[]
}

type EnvGroup = {
  title: string
  description: string
  fields: EnvField[]
}

const booleanOptions = [
  { label: 'true', value: 'true' },
  { label: 'false', value: 'false' },
]

const { t } = useLanguage()

const defaultAppUILanguage = () => {
  if (typeof navigator === 'undefined') return 'en'
  const languages = [...(navigator.languages || []), navigator.language].filter(Boolean)
  return languages.some(language => language.toLowerCase().startsWith('zh')) ? 'zh-CN' : 'en'
}

const envGroups = computed<EnvGroup[]>(() => [
  {
    title: t('env.groupAccess'),
    description: t('env.groupAccessDesc'),
    fields: [
      { key: 'PROXY_ACCESS_KEY', label: t('env.fieldProxyAccessKey'), type: 'password' as const, defaultValue: '', required: true, disallow: ['your-proxy-access-key'], placeholder: t('env.placeholderProxyAccessKey') },
      { key: 'ADMIN_ACCESS_KEY', label: t('env.fieldAdminAccessKey'), type: 'password' as const, defaultValue: '', placeholder: t('env.placeholderAdminAccessKey'), description: t('env.descAdminAccessKey') },
    ],
  },
  {
    title: t('env.groupServer'),
    description: t('env.groupServerDesc'),
    fields: [
      { key: 'PORT', label: t('env.fieldPort'), type: 'number' as const, defaultValue: '3688', min: 1, max: 65535, description: t('env.descPort') },
      { key: 'ENV', label: t('env.fieldEnv'), type: 'select' as const, defaultValue: 'production', options: [{ label: 'production', value: 'production' }, { label: 'development', value: 'development' }], description: t('env.descEnv') },
      { key: 'ENABLE_HTTPS', label: t('env.fieldEnableHttps'), type: 'select' as const, defaultValue: 'false', options: booleanOptions, description: t('env.descEnableHttps') },
      { key: 'TLS_AUTO_CERT', label: t('env.fieldTlsAutoCert'), type: 'select' as const, defaultValue: 'true', options: booleanOptions, description: t('env.descTlsAutoCert') },
      { key: 'TLS_CERT_FILE', label: t('env.fieldTlsCertFile'), type: 'text' as const, defaultValue: '', placeholder: t('env.placeholderTlsCertFile'), description: t('env.descTlsCertFile') },
      { key: 'TLS_KEY_FILE', label: t('env.fieldTlsKeyFile'), type: 'text' as const, defaultValue: '', placeholder: t('env.placeholderTlsKeyFile'), description: t('env.descTlsKeyFile') },
    ],
  },
  {
    title: t('env.groupWebUI'),
    description: t('env.groupWebUIDesc'),
    fields: [
      { key: 'ENABLE_WEB_UI', label: t('env.fieldEnableWebUI'), type: 'select' as const, defaultValue: 'true', options: booleanOptions, description: t('env.descEnableWebUI') },
      { key: 'APP_UI_LANGUAGE', label: t('env.fieldAppUILanguage'), type: 'select' as const, defaultValue: defaultAppUILanguage(), options: [{ label: 'English', value: 'en' }, { label: 'Bahasa Indonesia', value: 'id' }, { label: '简体中文', value: 'zh-CN' }] },
    ],
  },
  {
    title: t('env.groupLogs'),
    description: t('env.groupLogsDesc'),
    fields: [
      { key: 'LOG_LEVEL', label: t('env.fieldLogLevel'), type: 'select' as const, defaultValue: 'info', options: [{ label: 'error', value: 'error' }, { label: 'warn', value: 'warn' }, { label: 'info', value: 'info' }, { label: 'debug', value: 'debug' }] },
      { key: 'ENABLE_REQUEST_LOGS', label: t('env.fieldEnableRequestLogs'), type: 'select' as const, defaultValue: 'false', options: booleanOptions },
      { key: 'ENABLE_RESPONSE_LOGS', label: t('env.fieldEnableResponseLogs'), type: 'select' as const, defaultValue: 'false', options: booleanOptions, description: t('env.descEnableResponseLogs') },
      { key: 'QUIET_POLLING_LOGS', label: t('env.fieldQuietPollingLogs'), type: 'select' as const, defaultValue: 'true', options: booleanOptions },
      { key: 'RAW_LOG_OUTPUT', label: t('env.fieldRawLogOutput'), type: 'select' as const, defaultValue: 'false', options: booleanOptions },
      { key: 'SSE_DEBUG_LEVEL', label: t('env.fieldSseDebugLevel'), type: 'select' as const, defaultValue: 'off', options: [{ label: 'off', value: 'off' }, { label: 'summary', value: 'summary' }, { label: 'full', value: 'full' }] },
      { key: 'REWRITE_RESPONSE_MODEL', label: t('env.fieldRewriteResponseModel'), type: 'select' as const, defaultValue: 'false', options: booleanOptions },
    ],
  },
  {
    title: t('env.groupPerformance'),
    description: t('env.groupPerformanceDesc'),
    fields: [
      { key: 'SERVER_READ_TIMEOUT', label: t('env.fieldServerReadTimeout'), type: 'number' as const, defaultValue: '60000', min: 10000, max: 300000 },
      { key: 'MAX_REQUEST_BODY_SIZE_MB', label: t('env.fieldMaxRequestBodySize'), type: 'number' as const, defaultValue: '50', min: 1 },
    ],
  },
  {
    title: t('env.groupCors'),
    description: t('env.groupCorsDesc'),
    fields: [
      { key: 'ENABLE_CORS', label: t('env.fieldEnableCors'), type: 'select' as const, defaultValue: 'false', options: booleanOptions },
      { key: 'CORS_ORIGIN', label: t('env.fieldCorsOrigin'), type: 'text' as const, defaultValue: '*', placeholder: '*' },
    ],
  },
])

const supportedKeys = envGroups.value.flatMap((group) => group.fields.map((field) => field.key))
const allFields = envGroups.value.flatMap((group) => group.fields)

const { envFile, envLoading, envSaving, envMessage, envError, editors, openingEditor, loadEnvFile, saveEnvFile, loadEditors, openInEditor } = useEnvFile()

const saveState = ref<'idle' | 'saved' | 'failed'>('idle')
let saveResetTimer: ReturnType<typeof setTimeout> | null = null

const resetSaveState = () => {
  if (saveResetTimer) {
    clearTimeout(saveResetTimer)
    saveResetTimer = null
  }
  saveState.value = 'idle'
  clearMessages()
}

const clearMessages = () => {
  envMessage.value = ''
  envError.value = ''
}

const entries = ref<EnvEntry[]>([])
const newline = ref('\n')
const showSecret = reactive<Record<string, boolean>>({})
const copiedKey = ref('')
let copiedTimer: ReturnType<typeof setTimeout> | null = null
const copyToClipboard = async (key: string) => {
  try {
    await navigator.clipboard.writeText(form[key])
    copiedKey.value = key
    if (copiedTimer) clearTimeout(copiedTimer)
    copiedTimer = setTimeout(() => { copiedKey.value = '' }, 1500)
  } catch { /* ignore */ }
}
const form = reactive<Record<string, string>>(Object.fromEntries(allFields.map((field) => [field.key, field.defaultValue])))

const fieldErrors = computed(() => {
  const errors: Record<string, string> = {}
  for (const field of allFields) {
    const value = String(form[field.key] ?? '').trim()
    if (field.required && !value) {
      errors[field.key] = t('env.fieldRequired', { field: field.label })
      continue
    }
    if (field.disallow?.includes(value)) {
      errors[field.key] = t('env.fieldDisallow', { field: field.label })
      continue
    }
    if (field.type === 'number') {
      const numberValue = Number(value)
      if (!Number.isFinite(numberValue)) {
        errors[field.key] = t('env.fieldNumber', { field: field.label })
        continue
      }
      if (field.step !== '0.01' && !Number.isInteger(numberValue)) {
        errors[field.key] = t('env.fieldInteger', { field: field.label })
        continue
      }
      if (field.min !== undefined && numberValue < field.min) {
        errors[field.key] = t('env.fieldMin', { field: field.label, min: String(field.min) })
        continue
      }
      if (field.max !== undefined && numberValue > field.max) {
        errors[field.key] = t('env.fieldMax', { field: field.label, max: String(field.max) })
      }
    }
  }
  return errors
})

const validationError = computed(() => Object.values(fieldErrors.value)[0] || '')

const alertMessage = computed(() => {
  if (validationError.value) return validationError.value
  if (saveState.value === 'saved') return t('env.saveSuccess')
  if (saveState.value === 'failed' && envError.value) return envError.value
  if (envError.value) return envError.value
  if (envMessage.value) return envMessage.value
  return ''
})

const alertVariant = computed(() => {
  if (validationError.value || saveState.value === 'failed' || envError.value) return 'destructive'
  if (saveState.value === 'saved' || envMessage.value) return 'success'
  return 'default'
})

const alertIcon = computed(() => {
  if (validationError.value || saveState.value === 'failed' || envError.value) return X
  if (saveState.value === 'saved' || envMessage.value) return Check
  return null
})

const saveButtonState = computed(() => {
  const states = {
    saved: {
      variant: 'success' as const,
      icon: Check,
      text: t('env.saved'),
      disabled: false,
      spinning: false,
    },
    failed: {
      variant: 'error' as const,
      icon: X,
      text: t('env.failed'),
      disabled: false,
      spinning: false,
    },
    idle: {
      variant: 'default' as const,
      icon: Save,
      text: t('env.save'),
      disabled: false,
      spinning: false,
    },
  }

  if (envSaving.value) {
    return {
      variant: 'default' as const,
      icon: RefreshCw,
      text: t('env.saving'),
      disabled: true,
      spinning: true,
    }
  }

  return states[saveState.value]
})

const isSaveDisabled = computed(() => {
  return envLoading.value || envSaving.value || Boolean(validationError.value)
})

const load = async () => {
  if (envSaving.value) {
    return
  }

  if (saveResetTimer) {
    clearTimeout(saveResetTimer)
    saveResetTimer = null
  }

  resetSaveState()

  try {
    await loadEnvFile()
    const content = envFile.value.content || ''
    newline.value = detectEnvNewline(content)
    entries.value = parseEnvFile(content)
    for (const field of allFields) {
      form[field.key] = getEnvFieldValue(entries.value, field.key, field.defaultValue)
    }
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err)
    envError.value = t('env.loadFailed', { error: msg })
  }
}

const save = async () => {
  if (validationError.value || envSaving.value) return

  if (saveResetTimer) {
    clearTimeout(saveResetTimer)
    saveResetTimer = null
  }

  clearMessages()

  const serialized = serializeEnvFile(entries.value, form, supportedKeys, newline.value)
  await saveEnvFile(serialized)

  if (envError.value) {
    saveState.value = 'failed'
  } else {
    saveState.value = 'saved'
    entries.value = parseEnvFile(envFile.value.content || serialized)
  }

  saveResetTimer = setTimeout(resetSaveState, 5000)
}

const inputType = (field: EnvField) => {
  if (field.type === 'password') return showSecret[field.key] ? 'text' : 'password'
  if (field.type === 'number') return 'number'
  return 'text'
}

onMounted(() => {
  load()
  loadEditors()
})

onUnmounted(() => {
  if (saveResetTimer) {
    clearTimeout(saveResetTimer)
    saveResetTimer = null
  }
  if (copiedTimer) {
    clearTimeout(copiedTimer)
    copiedTimer = null
  }
})
</script>

<template>
  <Card>
    <CardHeader class="pb-3">
      <div class="flex items-start justify-between gap-3">
        <div>
          <CardTitle class="text-base">{{ t('env.title') }}</CardTitle>
          <p class="text-xs text-muted-foreground mt-1 break-all">
            {{ envFile.path || t('env.pathDetecting') }}
          </p>
        </div>
        <div class="flex gap-2">
          <Button size="sm" variant="ghost" :disabled="envLoading || envSaving" @click="load">
            <RefreshCw class="w-4 h-4 mr-1.5" />
            {{ t('env.refresh') }}
          </Button>

          <div class="relative" v-if="editors.length > 0">
            <Button size="sm" variant="outline" :disabled="openingEditor || envSaving" @click="editors.length === 1 ? openInEditor(editors[0].path) : null" :class="editors.length > 1 ? 'pr-8' : ''">
              <ExternalLink class="w-4 h-4 mr-1.5" />
              <span v-if="openingEditor">{{ t('env.openingEditor') }}</span>
              <span v-else-if="editors.length === 1">{{ t('env.openWithEditor', { editor: editors[0].name }) }}</span>
              <span v-else>{{ t('env.openInEditor') }}</span>
            </Button>
            <select
              v-if="editors.length > 1"
              class="absolute inset-0 w-full opacity-0 cursor-pointer"
              :disabled="openingEditor || envSaving"
              @change="($event.target as HTMLSelectElement).value && openInEditor(($event.target as HTMLSelectElement).value); ($event.target as HTMLSelectElement).selectedIndex = 0"
            >
              <option value="" disabled selected>{{ t('env.selectEditor') }}</option>
              <option v-for="ed in editors" :key="ed.id" :value="ed.path">{{ ed.name }}</option>
            </select>
          </div>

          <Button size="sm" :variant="saveButtonState.variant" :disabled="isSaveDisabled" @click="save">
            <component :is="saveButtonState.icon" class="w-4 h-4 mr-1.5" :class="{ 'animate-spin': saveButtonState.spinning }" />
            {{ saveButtonState.text }}
          </Button>
        </div>
      </div>

      <Alert v-if="alertMessage" :variant="alertVariant" class="mt-3">
        <div class="flex items-center gap-2">
          <component :is="alertIcon" class="w-4 h-4" />
          <p class="text-sm font-medium">{{ alertMessage }}</p>
        </div>
      </Alert>
    </CardHeader>

    <CardContent class="space-y-6">
      <section v-for="group in envGroups" :key="group.title" class="space-y-3">
        <div>
          <h3 class="text-sm font-semibold">{{ group.title }}</h3>
          <p class="text-xs text-muted-foreground mt-1">{{ group.description }}</p>
        </div>

        <div class="grid grid-cols-1 lg:grid-cols-2 gap-4">
          <div v-for="field in group.fields" :key="field.key" class="space-y-1.5">
            <Label class="text-xs text-muted-foreground">{{ field.key }}</Label>

            <div v-if="field.type === 'password'" class="flex gap-2">
              <Input v-model="form[field.key]" :type="inputType(field)" :placeholder="field.placeholder" />
              <Button type="button" variant="secondary" size="sm" @click="showSecret[field.key] = !showSecret[field.key]">
                {{ showSecret[field.key] ? t('env.hide') : t('env.show') }}
              </Button>
              <Button v-if="form[field.key]" type="button" variant="outline" size="sm" @click="copyToClipboard(field.key)">
                {{ copiedKey === field.key ? t('env.copied') : t('env.copy') }}
              </Button>
            </div>

            <select
              v-else-if="field.type === 'select'"
              v-model="form[field.key]"
              class="w-full h-9 rounded-md border border-input bg-background px-3 py-1 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
            >
              <option v-for="option in field.options" :key="option.value" :value="option.value">
                {{ option.label }}
              </option>
            </select>

            <Input
              v-else
              v-model="form[field.key]"
              :type="inputType(field)"
              :min="field.min"
              :max="field.max"
              :step="field.step"
              :placeholder="field.placeholder"
            />

            <p v-if="field.description" class="text-xs text-muted-foreground">{{ field.description }}</p>
            <p v-if="fieldErrors[field.key]" class="text-xs text-destructive-foreground">{{ fieldErrors[field.key] }}</p>
          </div>
        </div>
      </section>

    </CardContent>
  </Card>
</template>
