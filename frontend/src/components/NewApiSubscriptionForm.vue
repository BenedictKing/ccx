<template>
  <div class="newapi-subscription-form d-flex flex-column ga-4">
    <!-- Step 1: 验证（订阅中心两步流程；添加渠道的自动接入模式无步骤结构） -->
    <v-form @submit.prevent="autoProvision ? handleAutoSubmit() : handleVerify()">
      <div v-if="!autoProvision" class="text-subtitle-2 mb-2 text-medium-emphasis">
        {{ t('subscription.newApi.step1Title') }}
      </div>
      <v-text-field
        v-model="verifyForm.baseUrl"
        :label="t('subscription.newApi.baseUrl')"
        placeholder="https://your-newapi-instance.com"
        variant="outlined"
        density="compact"
        class="mb-2"
        :disabled="verified"
        required
      >
        <!-- 借 details 插槽渲染：紧贴输入框下沿，与其余字段的空 details 占位同高，不打乱表单节奏；
             details 容器是 space-between 的 flex（左侧固定渲染 0 宽 messages），w-100 占满整行才不会右贴 -->
        <template #details>
          <div
            v-if="recognizedBaseUrl"
            class="recognized-base-url d-flex align-center ga-1 w-100 text-caption text-medium-emphasis"
          >
            <v-icon size="14" color="success">mdi-arrow-right</v-icon>
            <span>{{ t('autopilot.quickAdd.recognizedBaseUrl', { url: recognizedBaseUrl }) }}</span>
          </div>
        </template>
      </v-text-field>
      <v-text-field
        v-model="verifyForm.accessToken"
        :label="t('subscription.newApi.accessToken')"
        variant="outlined"
        density="compact"
        type="password"
        class="mb-2"
        :disabled="verified"
        required
      />
      <v-text-field
        v-model="verifyForm.userId"
        :label="t('subscription.newApi.userId')"
        variant="outlined"
        density="compact"
        class="mb-2"
        :disabled="verified"
        required
      />
      <v-select
        v-model="verifyForm.authTokenMode"
        :label="t('subscription.newApi.authTokenMode')"
        :items="authTokenModeOptions"
        variant="outlined"
        density="compact"
        class="mb-2"
        :disabled="verified"
      />
      <v-text-field
        v-model="verifyForm.proxyUrl"
        :label="t('subscription.newApi.proxyUrl')"
        :hint="t('subscription.newApi.proxyUrlHint')"
        persistent-hint
        variant="outlined"
        density="compact"
        class="mb-2"
        :disabled="verified"
      />
      <v-switch
        v-model="verifyForm.proxyPreferDirect"
        :label="t('subscription.newApi.proxyPreferDirect')"
        :hint="t('subscription.newApi.proxyPreferDirectHint')"
        persistent-hint
        color="primary"
        density="compact"
        class="mb-2"
        :disabled="verified || !verifyForm.proxyUrl?.trim()"
      />
      <!-- 显示名称不收用户输入：验证后自动取上游账号用户名 -->

      <!-- 自动接入模式：单按钮直达（验证 → 自动建渠道）；订阅中心保留 验证/重新验证 两步 -->
      <v-btn
        v-if="autoProvision"
        color="primary"
        type="submit"
        :loading="verifying || provisioning"
        :disabled="!canVerify"
        block
      >
        {{ t('subscription.newApi.verifyAndProvision') }}
      </v-btn>
      <v-btn
        v-else-if="!verified"
        color="primary"
        type="submit"
        :loading="verifying"
        :disabled="!canVerify"
        block
      >
        {{ t('subscription.newApi.verify') }}
      </v-btn>
      <v-btn
        v-else
        variant="tonal"
        block
        @click="resetVerification"
      >
        {{ t('subscription.newApi.reVerify') }}
      </v-btn>
    </v-form>

    <!-- 验证结果展示（仅订阅中心两步流程） -->
    <v-card v-if="!autoProvision && verified && verifyResult" variant="outlined" class="pa-3">
      <div class="text-subtitle-2 mb-2">{{ t('subscription.newApi.accountPreview') }}</div>
      <div class="d-flex flex-column ga-1 text-body-2">
        <div>{{ t('subscription.newApi.username') }}: {{ verifyResult.username }}</div>
        <div>{{ t('subscription.newApi.quota') }}: {{ verifyResult.quota }}</div>
        <div>{{ t('subscription.newApi.usedQuota') }}: {{ verifyResult.usedQuota }}</div>
        <div>
          {{ t('subscription.newApi.availableModels') }}: {{ verifyResult.availableModels.length }}
        </div>
        <div v-if="groupItems.length">
          {{ t('subscription.newApi.groups') }}:
          <v-chip
            v-for="g in groupItems"
            :key="g.name"
            size="small"
            class="mr-1 mt-1"
            :color="g.modelCount === 0 ? 'error' : g.ratio <= maxGroupMultiplier ? 'success' : 'warning'"
            variant="tonal"
          >
            {{ g.name }} × {{ g.ratio
            }}<template v-if="g.modelCount !== undefined">
              · {{ t('subscription.newApi.groupModelCount', { count: g.modelCount }) }}</template
            >
          </v-chip>
        </div>
      </div>
    </v-card>

    <!-- Step 2: 接入（仅订阅中心两步流程；自动接入模式由验证成功后自动完成） -->
    <v-form v-if="!autoProvision && verified" @submit.prevent="handleProvision">
      <v-divider class="my-2" />
      <div class="text-subtitle-2 mb-2 text-medium-emphasis">
        {{ t('subscription.newApi.step2Title') }}
      </div>
      <v-text-field
        v-model="provisionForm.subscriptionUid"
        :label="t('subscription.field.uid')"
        variant="outlined"
        density="compact"
        class="mb-2"
        required
      />
      <v-select
        v-model="provisionForm.channelKind"
        :label="t('subscription.newApi.channelKind')"
        :items="channelKindOptions"
        variant="outlined"
        density="compact"
        class="mb-2"
        required
      />
      <v-text-field
        v-model="provisionForm.channelName"
        :label="t('subscription.newApi.channelName')"
        variant="outlined"
        density="compact"
        class="mb-2"
      />
      <v-text-field
        v-model.number="maxGroupMultiplier"
        :label="t('subscription.newApi.maxGroupMultiplier')"
        type="number"
        min="0"
        step="0.1"
        variant="outlined"
        density="compact"
        class="mb-1"
        required
      />
      <div class="text-caption text-medium-emphasis mb-2">
        {{ t('subscription.newApi.maxGroupMultiplierHint', { limit: maxGroupMultiplier }) }}
      </div>
      <v-alert v-if="blockedGroupCount > 0" color="warning" variant="tonal" density="compact" class="mb-2">
        {{ t('subscription.newApi.excludedGroups', { count: blockedGroupCount, limit: maxGroupMultiplier }) }}
      </v-alert>
      <v-alert v-if="emptySkippedGroupNames.length > 0" color="info" variant="tonal" density="compact" class="mb-2">
        {{
          t('subscription.newApi.emptyGroupsSkipped', { count: emptySkippedGroupNames.length, groups: emptySkippedGroupNames.join('、') })
        }}
      </v-alert>
      <v-alert v-if="verifyResult?.groupFetchError" color="error" variant="tonal" density="compact" class="mb-2">
        {{ t('subscription.newApi.groupFetchError') }} {{ verifyResult.groupFetchError }}
      </v-alert>
      <v-alert v-if="verified && eligibleGroupItems.length === 0" color="error" variant="tonal" density="compact" class="mb-2">
        {{ t('subscription.newApi.noEligibleGroups', { limit: maxGroupMultiplier }) }}
      </v-alert>
      <v-alert v-if="eligibleGroupItems.length" color="success" variant="tonal" density="compact" class="mb-2">
        {{ t('subscription.newApi.eligibleGroups', { count: eligibleGroupItems.length }) }}
        <v-chip
          v-for="group in eligibleGroupItems"
          :key="group.name"
          size="x-small"
          class="ml-1"
          variant="outlined"
        >
          {{ group.name }} × {{ group.ratio }}
        </v-chip>
      </v-alert>
      <v-textarea
        v-model="provisionForm.notes"
        :label="t('subscription.field.notes')"
        variant="outlined"
        density="compact"
        rows="2"
        class="mb-2"
      />

      <v-btn
        color="primary"
        type="submit"
        :loading="provisioning"
        :disabled="!canProvision"
        block
      >
        {{ t('subscription.newApi.provision') }}
      </v-btn>
    </v-form>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from '@/i18n'
import { api } from '@/services/api'
import type {
  NewApiVerifyRequest,
  NewApiVerifyResponse,
  NewApiProvisionRequest,
  NewApiProvisionResponse,
} from '@/services/api-types'
import {
  DEFAULT_NEWAPI_MAX_GROUP_MULTIPLIER,
  eligibleNewApiGroups,
  isValidNewApiGroupMultiplier
} from '@/utils/newApiGroups'
import { parseQuickInput } from '@/utils/quickInputParser'

const { t } = useI18n()

interface Props {
  /**
   * 自动接入模式（添加渠道场景）：单按钮「验证并接入」直达建渠道，
   * 无验证预览与第二步配置；订阅 ID 自动生成、渠道类型取 defaultChannelKind
   */
  autoProvision?: boolean
  /** 自动接入模式使用的渠道类型（跟随添加渠道对话框当前 tab） */
  defaultChannelKind?: string
}

const props = withDefaults(defineProps<Props>(), {
  autoProvision: false,
  defaultChannelKind: 'messages'
})

const emit = defineEmits<{
  created: [result: NewApiProvisionResponse]
  error: [message: string]
}>()

const verifying = ref(false)
const provisioning = ref(false)
const verified = ref(false)
const verifyResult = ref<NewApiVerifyResponse | null>(null)
const maxGroupMultiplier = ref(DEFAULT_NEWAPI_MAX_GROUP_MULTIPLIER)

const verifyForm = ref<NewApiVerifyRequest>({
  baseUrl: '',
  accessToken: '',
  userId: '',
  authTokenMode: 'bearer',
  proxyUrl: '',
  proxyPreferDirect: false,
})

const provisionForm = ref<NewApiProvisionRequest>({
  subscriptionUid: '',
  displayName: '',
  baseUrl: '',
  accessToken: '',
  channelKind: 'messages',
  userId: '',
  authTokenMode: 'bearer',
  channelName: '',
  notes: '',
  proxyUrl: '',
  proxyPreferDirect: false,
})

const authTokenModeOptions = computed(() => [
  { title: 'Bearer', value: 'bearer' },
  { title: 'Raw', value: 'raw' },
])

const channelKindOptions = computed(() => [
  { title: 'messages', value: 'messages' },
  { title: 'chat', value: 'chat' },
  { title: 'responses', value: 'responses' },
  { title: 'gemini', value: 'gemini' },
  { title: 'images', value: 'images' },
  { title: 'vectors', value: 'vectors' },
])

const groupItems = computed(() => {
  if (!verifyResult.value) return []
  const counts = verifyResult.value.groupModelCounts
  return Object.entries(verifyResult.value.groups || {})
    .map(([name, ratio]) => ({ name, ratio, modelCount: counts?.[name] }))
    .sort((left, right) => left.ratio - right.ratio || left.name.localeCompare(right.name))
})

const maxGroupMultiplierValid = computed(() => isValidNewApiGroupMultiplier(maxGroupMultiplier.value))
// 倍率合格集合（不看模型数）：用于区分「倍率超限」与「分组 0 模型」两类排除原因
const ratioEligibleGroupItems = computed(() =>
  eligibleNewApiGroups(verifyResult.value?.groups || {}, maxGroupMultiplier.value)
)
const eligibleGroupItems = computed(() =>
  eligibleNewApiGroups(verifyResult.value?.groups || {}, maxGroupMultiplier.value, verifyResult.value?.groupModelCounts)
)
const blockedGroupCount = computed(() => groupItems.value.length - ratioEligibleGroupItems.value.length)
// 倍率合格但可用模型数为 0 而被跳过的分组：接入时不建 key，站点补上模型后同步兜底会自动补建
const emptySkippedGroupNames = computed(() => {
  if (ratioEligibleGroupItems.value.length === eligibleGroupItems.value.length) return []
  const eligible = new Set(eligibleGroupItems.value.map(g => g.name))
  return ratioEligibleGroupItems.value.filter(g => !eligible.has(g.name)).map(g => g.name)
})

const canVerify = computed(() => !!verifyForm.value.baseUrl.trim() && !!verifyForm.value.accessToken.trim() && !!(verifyForm.value.userId ?? '').trim())

// 与标准模式同一套识别：粘贴面板页地址（如 .../keys）或端点 URL 时剥到站点根；
// 识别不出（空/非法输入）时回退原始输入，保持原有提交行为
const recognizedBaseUrl = computed(() => parseQuickInput(verifyForm.value.baseUrl.trim()).detectedBaseUrl)
const submitBaseUrl = computed(() => recognizedBaseUrl.value || verifyForm.value.baseUrl.trim())
const canProvision = computed(
  () =>
    !!provisionForm.value.subscriptionUid.trim() &&
    !!provisionForm.value.channelKind &&
    maxGroupMultiplierValid.value &&
    eligibleGroupItems.value.length > 0
)

async function handleVerify() {
  if (!canVerify.value) return
  verifying.value = true
  try {
    const result = await api.verifyNewApiSubscription({
      baseUrl: submitBaseUrl.value,
      accessToken: verifyForm.value.accessToken,
      userId: verifyForm.value.userId?.trim() || undefined,
      authTokenMode: verifyForm.value.authTokenMode || undefined,
      proxyUrl: verifyForm.value.proxyUrl?.trim() || undefined,
      proxyPreferDirect: verifyForm.value.proxyPreferDirect || undefined,
    })
    verifyResult.value = result
    verified.value = true

    // 预填第 2 步表单（显示名称自动取上游账号用户名，不收用户输入）
    provisionForm.value.baseUrl = submitBaseUrl.value
    provisionForm.value.accessToken = verifyForm.value.accessToken
    provisionForm.value.userId = verifyForm.value.userId?.trim() || undefined
    provisionForm.value.authTokenMode = verifyForm.value.authTokenMode || undefined
    provisionForm.value.displayName = result.username || verifyForm.value.userId?.trim() || 'new-api'
    provisionForm.value.proxyUrl = verifyForm.value.proxyUrl?.trim() || undefined
    provisionForm.value.proxyPreferDirect = verifyForm.value.proxyPreferDirect
  } catch (e) {
    const message = e instanceof Error ? e.message : 'Unknown error'
    emit('error', message)
  } finally {
    verifying.value = false
  }
}

function resetVerification() {
  verified.value = false
  verifyResult.value = null
}

async function handleProvision() {
  if (!canProvision.value) return
  provisioning.value = true
  try {
    const result = await api.provisionNewApiSubscription({
      subscriptionUid: provisionForm.value.subscriptionUid.trim(),
      displayName: provisionForm.value.displayName || provisionForm.value.subscriptionUid,
      baseUrl: provisionForm.value.baseUrl,
      accessToken: provisionForm.value.accessToken,
      channelKind: provisionForm.value.channelKind,
      userId: provisionForm.value.userId || undefined,
      authTokenMode: provisionForm.value.authTokenMode || undefined,
      channelName: provisionForm.value.channelName || undefined,
      provisionAllEligibleGroups: true,
      maxGroupMultiplier: maxGroupMultiplier.value,
      notes: provisionForm.value.notes || undefined,
      proxyUrl: provisionForm.value.proxyUrl?.trim() || undefined,
      proxyPreferDirect: provisionForm.value.proxyPreferDirect || undefined,
    })
    emit('created', result)
  } catch (e) {
    const message = e instanceof Error ? e.message : 'Unknown error'
    emit('error', message)
  } finally {
    provisioning.value = false
  }
}

// ---- 自动接入模式（添加渠道场景） ----

/** 订阅 ID 自动生成：8 位随机后缀避开与既有订阅冲突（后端 409 兜底） */
function generateAutoSubscriptionUid(): string {
  const chars = 'abcdefghijklmnopqrstuvwxyz0123456789'
  let suffix = ''
  for (let i = 0; i < 8; i++) suffix += chars.charAt(Math.floor(Math.random() * chars.length))
  return `newapi-${suffix}`
}

/** 验证成功后自动接入：默认倍率上限 + 全部合格分组，接入失败解锁表单供整段重试 */
async function autoProvisionAfterVerify() {
  const result = verifyResult.value
  if (!result) return

  // 分组未知或无合格分组时后端会硬性拦截，提前以明确文案失败，避免无效建 key 请求
  if (result.groupFetchError || eligibleGroupItems.value.length === 0) {
    resetVerification()
    if (result.groupFetchError) {
      emit('error', `${t('subscription.newApi.groupFetchError')} ${result.groupFetchError}`)
    } else if (ratioEligibleGroupItems.value.length > 0 && emptySkippedGroupNames.value.length > 0) {
      // 倍率合格但全部 0 可用模型：不是倍率问题，需告知真实原因
      emit('error', t('subscription.newApi.allEligibleGroupsEmpty', { groups: emptySkippedGroupNames.value.join('、') }))
    } else {
      emit('error', t('subscription.newApi.noEligibleGroups', { limit: maxGroupMultiplier.value }))
    }
    return
  }

  provisioning.value = true
  try {
    const provisioned = await api.provisionNewApiSubscription({
      subscriptionUid: generateAutoSubscriptionUid(),
      displayName: provisionForm.value.displayName,
      baseUrl: provisionForm.value.baseUrl,
      accessToken: provisionForm.value.accessToken,
      channelKind: props.defaultChannelKind,
      userId: provisionForm.value.userId || undefined,
      authTokenMode: provisionForm.value.authTokenMode || undefined,
      provisionAllEligibleGroups: true,
      maxGroupMultiplier: maxGroupMultiplier.value,
      proxyUrl: provisionForm.value.proxyUrl?.trim() || undefined,
      proxyPreferDirect: provisionForm.value.proxyPreferDirect || undefined,
    })
    emit('created', provisioned)
  } catch (e) {
    const message = e instanceof Error ? e.message : 'Unknown error'
    resetVerification()
    emit('error', message)
  } finally {
    provisioning.value = false
  }
}

/** 自动接入模式的单按钮主操作：验证 → 自动接入 */
async function handleAutoSubmit() {
  if (!canVerify.value || verifying.value || provisioning.value) return
  await handleVerify()
  if (!verified.value) return
  await autoProvisionAfterVerify()
}

/** 当前步骤的主操作（供承载对话框的 ⌘/Ctrl+Enter 快捷键接线）：自动模式直达接入，否则未验证→验证、已验证→接入 */
function requestPrimaryAction() {
  if (props.autoProvision) {
    void handleAutoSubmit()
    return
  }
  if (verified.value) {
    if (canProvision.value && !provisioning.value) void handleProvision()
  } else if (canVerify.value && !verifying.value) {
    void handleVerify()
  }
}

defineExpose({ requestPrimaryAction })
</script>

<style scoped>
.recognized-base-url {
  overflow-wrap: anywhere;
}
</style>
