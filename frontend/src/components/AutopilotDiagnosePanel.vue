<template>
  <v-card variant="outlined" rounded="lg">
    <v-card-title class="d-flex align-center text-subtitle-1 font-weight-bold pb-0">
      <v-icon size="20" class="mr-2" color="primary">mdi-radar</v-icon>
      {{ t('autopilot.diagnose.title') }}
    </v-card-title>

    <v-card-text>
      <v-alert type="info" variant="tonal" density="compact" class="mb-4">
        {{ t('autopilot.diagnose.hint') }}
      </v-alert>

      <v-row dense>
        <v-col cols="12" md="3">
          <v-select
            v-model="form.channelKind"
            :items="channelKindItems"
            item-title="label"
            item-value="value"
            :label="t('autopilot.diagnose.channelKind')"
            variant="outlined"
            density="compact"
            hide-details
          />
        </v-col>
        <v-col cols="12" md="4">
          <v-combobox
            v-model="form.model"
            :items="modelPresets"
            :label="t('autopilot.diagnose.model')"
            variant="outlined"
            density="compact"
            hide-details
          />
        </v-col>
        <v-col cols="6" md="2">
          <v-select
            v-model="form.agentRole"
            :items="agentRoleItems"
            item-title="label"
            item-value="value"
            :label="t('autopilot.diagnose.agentRole')"
            variant="outlined"
            density="compact"
            hide-details
          />
        </v-col>
        <v-col cols="6" md="3">
          <v-text-field
            v-model.number="form.estTokens"
            type="number"
            min="0"
            :label="t('autopilot.diagnose.estTokens')"
            variant="outlined"
            density="compact"
            hide-details
          />
        </v-col>
      </v-row>

      <div class="d-flex flex-wrap ga-4 mt-3">
        <v-switch
          v-model="form.toolUseNeed"
          :label="t('autopilot.diagnose.toolUse')"
          color="primary"
          density="compact"
          hide-details
          :disabled="!completionFeaturesEnabled"
        />
        <v-switch
          v-model="form.reasoningNeed"
          :label="t('autopilot.diagnose.reasoning')"
          color="primary"
          density="compact"
          hide-details
          :disabled="!completionFeaturesEnabled"
        />
        <v-switch
          v-model="form.hasImage"
          :label="t('autopilot.diagnose.hasImage')"
          color="primary"
          density="compact"
          hide-details
          :disabled="!completionFeaturesEnabled"
        />
      </div>

      <div class="d-flex flex-wrap align-center ga-2 mt-4">
        <span class="text-caption text-medium-emphasis mr-1">
          {{ t('autopilot.diagnose.quickModels') }}
        </span>
        <v-btn
          v-for="model in modelPresets"
          :key="model"
          size="small"
          variant="tonal"
          :disabled="loading"
          @click="runDiagnose(model)"
        >
          {{ model }}
        </v-btn>
        <v-spacer />
        <v-btn
          color="primary"
          variant="flat"
          prepend-icon="mdi-play"
          :loading="loading"
          @click="runDiagnose()"
        >
          {{ t('autopilot.diagnose.run') }}
        </v-btn>
      </div>

      <v-alert v-if="error" type="error" variant="tonal" density="compact" class="mt-4">
        {{ error }}
      </v-alert>

      <template v-if="response">
        <v-divider class="my-5" />

        <v-alert
          v-if="!plan"
          type="warning"
          variant="tonal"
          density="compact"
        >
          {{ response.message || t('autopilot.diagnose.noPlan') }}
        </v-alert>

        <template v-else>
          <div class="d-flex flex-wrap align-center ga-2 mb-4">
            <v-chip size="small" color="info" variant="tonal">
              {{ t('autopilot.diagnose.mode') }}: {{ response.mode }}
            </v-chip>
            <v-chip size="small" color="secondary" variant="tonal">
              {{ t('autopilot.diagnose.taskClass') }}: {{ profile?.TaskClass || '-' }}
            </v-chip>
            <v-chip size="small" variant="outlined">
              {{ t('autopilot.diagnose.qualityNeed') }}: {{ profile?.QualityNeed || '-' }}
            </v-chip>
            <v-chip size="small" variant="outlined">
              {{ t('autopilot.diagnose.candidates') }}: {{ candidates.length }}
            </v-chip>
            <v-chip size="small" color="success" variant="tonal">
              {{ t('autopilot.diagnose.eligible') }}: {{ eligibleCount }}
            </v-chip>
            <v-chip v-if="plan.fallbackUsed" size="small" color="warning" variant="flat">
              {{ t('autopilot.diagnose.failOpen') }}
            </v-chip>
          </div>

          <v-card variant="tonal" color="primary" rounded="lg" class="pa-3 mb-4">
            <div class="text-caption text-medium-emphasis">
              {{ t('autopilot.diagnose.recommendation') }}
            </div>
            <div class="d-flex flex-wrap align-center ga-2 mt-1">
              <v-tooltip :text="plan.selectedChannelUid || '-'" location="top" :open-delay="150" content-class="ccx-tooltip">
                <template #activator="{ props: tooltipProps }">
                  <span v-bind="tooltipProps" class="font-weight-bold">{{ channelName(plan.selectedChannelUid) }}</span>
                </template>
              </v-tooltip>
              <v-icon size="16">mdi-arrow-right</v-icon>
              <span class="font-weight-bold">{{ plan.selectedModel || profile?.Model || '-' }}</span>
              <v-chip
                v-if="selectedCandidate?.mappingSource"
                size="x-small"
                :color="mappingColor(selectedCandidate.mappingSource)"
                variant="flat"
              >
                {{ mappingSourceLabel(selectedCandidate.mappingSource) }}
              </v-chip>
            </div>
            <div v-if="selectedCandidate?.mappingReason" class="text-caption text-medium-emphasis mt-1">
              {{ selectedCandidate.mappingReason }}
            </div>
          </v-card>

          <div v-if="candidates.length === 0" class="text-center py-6 text-medium-emphasis">
            {{ t('autopilot.diagnose.noCandidates') }}
          </div>

          <v-table v-else hover density="compact">
            <thead>
              <tr>
                <th>{{ t('autopilot.diagnose.col.chosen') }}</th>
                <th>{{ t('autopilot.diagnose.col.channel') }}</th>
                <th>{{ t('autopilot.diagnose.col.actualModel') }}</th>
                <th>{{ t('autopilot.diagnose.col.mapping') }}</th>
                <th class="text-right">{{ t('autopilot.diagnose.col.score') }}</th>
                <th>{{ t('autopilot.diagnose.col.constraint') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="candidate in candidates"
                :key="candidate.candidateKey || candidate.channelUid"
                :class="{ 'text-medium-emphasis': !candidate.selected }"
              >
                <td>
                  <v-icon
                    v-if="candidate.channelUid === plan.selectedChannelUid"
                    size="18"
                    color="primary"
                  >mdi-star</v-icon>
                  <v-icon v-else size="16" color="grey">mdi-minus</v-icon>
                </td>
                <td class="text-caption">
                  <v-tooltip :text="candidate.channelUid" location="top" :open-delay="150" content-class="ccx-tooltip">
                    <template #activator="{ props: tooltipProps }">
                      <span v-bind="tooltipProps">{{ channelName(candidate.channelUid) }}</span>
                    </template>
                  </v-tooltip>
                </td>
                <td class="text-caption">
                  {{ candidate.mappedModel || profile?.Model || '-' }}
                </td>
                <td>
                  <v-chip
                    size="x-small"
                    :color="mappingColor(candidate.mappingSource)"
                    variant="tonal"
                  >
                    {{ mappingSourceLabel(candidate.mappingSource) }}
                  </v-chip>
                </td>
                <td class="text-caption text-right">{{ formatScore(candidate.score) }}</td>
                <td class="text-caption">
                  <v-chip v-if="candidate.selected" size="x-small" color="success" variant="tonal">
                    {{ t('autopilot.diagnose.passed') }}
                  </v-chip>
                  <span v-else>{{ candidate.filterReasons?.join('; ') || '-' }}</span>
                </td>
              </tr>
            </tbody>
          </v-table>

          <div v-if="plan.sortReasons?.length" class="d-flex flex-wrap align-center ga-2 mt-3">
            <span class="text-caption text-medium-emphasis">
              {{ t('autopilot.traceTable.sortReasons') }}:
            </span>
            <v-chip
              v-for="reason in plan.sortReasons"
              :key="reason"
              size="x-small"
              variant="outlined"
            >
              {{ reason }}
            </v-chip>
          </div>
        </template>
      </template>
    </v-card-text>
  </v-card>
</template>

<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { useI18n } from '@/i18n'
import { api } from '@/services/api'
import {
  diagnoseSmartRouting,
  type SmartRoutingDiagnoseChannelKind,
  type SmartRoutingDiagnoseRequest,
  type SmartRoutingDiagnoseResponse,
} from '@/services/autopilot-api'

interface DiagnoseForm {
  model: string | null
  channelKind: SmartRoutingDiagnoseChannelKind
  agentRole: 'main' | 'subagent' | ''
  estTokens: number
  toolUseNeed: boolean
  reasoningNeed: boolean
  hasImage: boolean
}

const { t } = useI18n()
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
const form = reactive<DiagnoseForm>({
  model: modelPresets[0],
  channelKind: 'messages',
  agentRole: 'main',
  estTokens: 20_000,
  toolUseNeed: true,
  reasoningNeed: true,
  hasImage: false,
})
const loading = ref(false)
const error = ref('')
const response = ref<SmartRoutingDiagnoseResponse | null>(null)
const channelNamesByUid = ref(new Map<string, string>())
const completionFeaturesEnabled = computed(() => (
  form.channelKind !== 'images' && form.channelKind !== 'vectors'
))

const channelKindItems = computed(() => (
  ['messages', 'chat', 'responses', 'gemini', 'images', 'vectors'] as SmartRoutingDiagnoseChannelKind[]
).map(value => ({
  value,
  label: t(`autopilot.diagnose.kind.${value}`),
})))

const agentRoleItems = computed(() => [
  { value: '', label: t('autopilot.diagnose.role.auto') },
  { value: 'main', label: t('autopilot.diagnose.role.main') },
  { value: 'subagent', label: t('autopilot.diagnose.role.subagent') },
])

const plan = computed(() => response.value?.plan ?? null)
const profile = computed(() => plan.value?.requestProfile)
const candidates = computed(() => plan.value?.candidates ?? [])
const eligibleCount = computed(() => candidates.value.filter(candidate => candidate.selected).length)
const selectedCandidate = computed(() => candidates.value.find(
  candidate => candidate.channelUid === plan.value?.selectedChannelUid
))

function operationFor(kind: SmartRoutingDiagnoseChannelKind): string {
  if (kind === 'images') return 'image_generation'
  if (kind === 'vectors') return 'embedding'
  return 'completion'
}

async function loadChannelNames() {
  const results = await Promise.all([
    api.getChannels(),
    api.getChatChannels(),
    api.getResponsesChannels(),
    api.getGeminiChannels(),
    api.getImagesChannels(),
    api.getVectorsChannels(),
  ])
  const names = new Map<string, string>()

  for (const result of results) {
    for (const channel of result.channels) {
      if (channel.channelUid) names.set(channel.channelUid, channel.name)
    }
  }

  channelNamesByUid.value = names
}

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
    response.value = await diagnoseSmartRouting(request)
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : t('autopilot.diagnose.failed')
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

function mappingColor(source?: string): string {
  if (source === 'auto_resolve_preview') return 'warning'
  if (source === 'explicit_mapping') return 'info'
  return 'grey'
}
</script>
