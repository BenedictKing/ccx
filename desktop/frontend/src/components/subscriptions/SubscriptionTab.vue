<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { Check, CheckCircle2, Copy, Loader2, ShieldCheck, Trash2, X } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { useAdminApi } from '@/composables/useAdminApi'
import { useChannelPresets } from '@/composables/useChannelPresets'
import { useCopilotOAuth } from '@/composables/useCopilotOAuth'
import { buildBaseConfigs, maskKey, useCopilotAccounts } from '@/composables/useCopilotAccounts'
import { useLanguage } from '@/composables/useLanguage'
import { GetProviderKeyAssets } from '@bindings/github.com/BenedictKing/ccx/desktop/desktopservice'
import type { Channel, ChannelsResponse } from '@/services/admin-api'
import type { ProviderKeyAsset } from '@/types'

type CopilotTarget = 'messages' | 'chat' | 'responses' | 'gemini'
type SubscriptionProvider = 'github-copilot'
type CopilotKnownAccount = {
  target: CopilotTarget
  key: string
  name?: string
}

const { t } = useLanguage()
const adminApi = useAdminApi()
const { creating, error, createChannel } = useChannelPresets()
const { verifyAccount, addAccount, removeAccount } = useCopilotAccounts()

const copilotApiKeys = ref<string[]>([])
const copilotProxyUrl = ref('')
const selectedSubscription = ref<SubscriptionProvider>('github-copilot')
const selectedCopilotTarget = ref<CopilotTarget>('responses')
const copilotCreateError = ref('')
const accountActionError = ref('')
const existingCopilotChannels = ref<Record<CopilotTarget, Channel | null>>({
  messages: null,
  chat: null,
  responses: null,
  gemini: null,
})
const savedCopilotAsset = ref<ProviderKeyAsset | null>(null)
const checkingCopilotChannel = ref(false)
const addingCopilotChannel = ref(false)
const channelStatusLoaded = ref(false)
const verifyingAccount = ref(false)
const removingKey = ref('')
const pendingRemoveKey = ref('')
const savedTokenFailed = ref(false)

const {
  copilotOAuthLoading,
  copilotPolling,
  copilotOAuthError,
  copilotOAuthSuccess,
  copilotUserCode,
  copilotUserCodeCopied,
  clearCopilotPollTimer,
  copyCopilotUserCode,
  startCopilotOAuth,
  openCopilotAuthorization,
} = useCopilotOAuth(copilotApiKeys, t, () => copilotProxyUrl.value)

const latestAuthorizedCopilotToken = computed(() => copilotApiKeys.value[copilotApiKeys.value.length - 1] || '')
const savedCopilotToken = computed(() => savedCopilotAsset.value?.apiKey || '')
const availableCopilotToken = computed(() => latestAuthorizedCopilotToken.value || savedCopilotToken.value)
const selectedCopilotChannel = computed(() => existingCopilotChannels.value[selectedCopilotTarget.value])
const hasCopilotChannel = computed(() => Boolean(selectedCopilotChannel.value))
const hasSavedCopilotAuthorization = computed(() => Boolean(savedCopilotToken.value))
const copilotAccounts = computed(() => (selectedCopilotChannel.value ? buildBaseConfigs(selectedCopilotChannel.value) : []))
const accountCount = computed(() => copilotAccounts.value.length)
const copilotAccountCounts = computed<Record<CopilotTarget, number>>(() => ({
  messages: existingCopilotChannels.value.messages ? buildBaseConfigs(existingCopilotChannels.value.messages).length : 0,
  chat: existingCopilotChannels.value.chat ? buildBaseConfigs(existingCopilotChannels.value.chat).length : 0,
  responses: existingCopilotChannels.value.responses ? buildBaseConfigs(existingCopilotChannels.value.responses).length : 0,
  gemini: existingCopilotChannels.value.gemini ? buildBaseConfigs(existingCopilotChannels.value.gemini).length : 0,
}))
const copilotTotalAccountCount = computed(() =>
  Object.values(copilotAccountCounts.value).reduce((total, count) => total + count, 0),
)
const copilotKnownAccounts = computed<CopilotKnownAccount[]>(() => {
  const accounts: CopilotKnownAccount[] = []
  for (const target of copilotTargetOptions.value) {
    const channel = existingCopilotChannels.value[target.value]
    if (!channel) continue
    accounts.push(...buildBaseConfigs(channel).map(account => ({
      target: target.value,
      key: account.key,
      name: account.name,
    })))
  }
  return accounts
})
const reusableCopilotAccount = computed<CopilotKnownAccount | null>(() => {
  const selectedKeys = new Set(copilotAccounts.value.map(account => account.key))
  const selectedNames = new Set(copilotAccounts.value.map(account => account.name).filter(Boolean))
  const accountFromOtherChannel = copilotKnownAccounts.value.find(account => (
    account.target !== selectedCopilotTarget.value
    && account.key
    && (!selectedKeys.has(account.key) || (account.name && !selectedNames.has(account.name)))
  ))
  if (accountFromOtherChannel) return accountFromOtherChannel
  const saved = availableCopilotToken.value
  if (saved && !selectedKeys.has(saved)) {
    return { target: selectedCopilotTarget.value, key: saved }
  }
  return null
})
const copilotBusy = computed(() =>
  copilotOAuthLoading.value || copilotPolling.value || creating.value || addingCopilotChannel.value || verifyingAccount.value || checkingCopilotChannel.value,
)
const copilotTargetOptions = computed<Array<{ value: CopilotTarget; label: string; description: string }>>(() => [
  { value: 'messages', label: t('subscription.targetClaude'), description: t('subscription.targetClaudeDesc') },
  { value: 'chat', label: t('subscription.targetChat'), description: t('subscription.targetChatDesc') },
  { value: 'responses', label: t('subscription.targetCodex'), description: t('subscription.targetCodexDesc') },
  { value: 'gemini', label: t('subscription.targetGemini'), description: t('subscription.targetGeminiDesc') },
])
const selectedCopilotTargetOption = computed(() =>
  copilotTargetOptions.value.find(item => item.value === selectedCopilotTarget.value) || copilotTargetOptions.value[2],
)
const copilotPrimaryActionLabel = computed(() =>
  reusableCopilotAccount.value ? t('subscription.addExistingAccount') :
  hasCopilotChannel.value ? t('subscription.authorizeAndAddAccount') : t('subscription.authorizeCopilot'),
)

async function refreshSavedCopilotAsset() {
  try {
    const assets = await GetProviderKeyAssets() as ProviderKeyAsset[]
    const asset = assets.find(item => item.provider === 'github-copilot' && item.apiKey) || null
    savedCopilotAsset.value = asset
    if (asset?.proxyUrl && !copilotProxyUrl.value.trim()) {
      copilotProxyUrl.value = asset.proxyUrl
    }
  } catch {
    savedCopilotAsset.value = null
  }
}

async function refreshCopilotChannelStatus() {
  checkingCopilotChannel.value = true
  try {
    const entries = await Promise.all(
      copilotTargetOptions.value.map(async ({ value }) => {
        const data = await adminApi.get<ChannelsResponse>(`/api/${value}/channels`)
        const channel = data.channels.find(item => item.name === 'desktop-github-copilot')
          || data.channels.find(item => item.serviceType === 'copilot')
          || null
        return [value, channel] as const
      }),
    )
    existingCopilotChannels.value = Object.fromEntries(entries) as Record<CopilotTarget, Channel | null>
    channelStatusLoaded.value = true
    const channelWithProxy = entries.map(([, channel]) => channel).find(channel => channel?.proxyUrl)
    if (channelWithProxy?.proxyUrl && !copilotProxyUrl.value.trim()) {
      copilotProxyUrl.value = channelWithProxy.proxyUrl
    }
  } catch {
    // 加载失败时清空渠道数据，避免使用陈旧的 channel.index。
    existingCopilotChannels.value = {
      messages: null,
      chat: null,
      responses: null,
      gemini: null,
    }
    channelStatusLoaded.value = false
  } finally {
    checkingCopilotChannel.value = false
  }
}

async function startCopilotAuthorization() {
  copilotApiKeys.value = []
  copilotCreateError.value = ''
  accountActionError.value = ''
  await startCopilotOAuth()
}

function handleCopilotPrimaryAction() {
  if (reusableCopilotAccount.value && channelStatusLoaded.value && !copilotBusy.value && !checkingCopilotChannel.value) {
    savedTokenFailed.value = false
    void processNewToken(reusableCopilotAccount.value.key)
    return
  }
  // 已有保存的授权 token 且当前 target 无渠道时，先尝试直接复用。
  // 必须等渠道状态检查完成且加载成功，避免误判为无渠道而覆盖已有配置。
  // savedTokenFailed 标记上一次复用失败（token 过期等），避免无限重试同一失效 token。
  const saved = savedCopilotToken.value
  if (saved && channelStatusLoaded.value && !hasCopilotChannel.value && !copilotBusy.value && !checkingCopilotChannel.value && !savedTokenFailed.value) {
    savedTokenFailed.value = true
    copilotOAuthError.value = ''
    void processNewToken(saved).then((ok) => {
      // 复用成功：清除失败标记，使切换 target 时仍可复用同一个有效保存 token。
      if (ok) savedTokenFailed.value = false
    })
    return
  }
  savedTokenFailed.value = false
  void startCopilotAuthorization()
}

function cancelCopilotAuthorization() {
  clearCopilotPollTimer()
  copilotPolling.value = false
  copilotOAuthLoading.value = false
}

// 授权拿到新 token 后：反查 GitHub 用户名 -> 必要时建渠道 -> 合并进渠道 key 池。
// 返回 true 表示成功加入，false 表示失败（错误已写入 accountActionError）。
async function processNewToken(token: string): Promise<boolean> {
  if (!token || verifyingAccount.value || addingCopilotChannel.value) return false
  const target = selectedCopilotTarget.value
  accountActionError.value = ''
  copilotCreateError.value = ''
  verifyingAccount.value = true
  try {
    const login = await verifyAccount(token, copilotProxyUrl.value)
    if (!login) throw new Error(t('subscription.verifyFailed'))
    verifyingAccount.value = false
    addingCopilotChannel.value = true
    let channel = existingCopilotChannels.value[target]
    if (!channel) {
      await createChannel({
        provider: 'github-copilot',
        target,
        baseUrl: 'https://api.githubcopilot.com',
        apiKey: token,
        name: 'desktop-github-copilot',
        proxyUrl: copilotProxyUrl.value.trim(),
      }, { reloadPresets: false })
      await refreshCopilotChannelStatus()
      channel = existingCopilotChannels.value[target]
    }
    if (!channel) throw new Error(t('subscription.channelResolveFailed'))
    // 已有渠道时不传 proxyUrl，避免用页面陈旧值覆盖渠道当前代理配置。
    // 新建渠道时 proxyUrl 已在 createChannel 调用中设置。
    await addAccount(target, channel, token, login)
    await syncExistingCopilotAccountToken(target, token, login)
    await refreshSavedCopilotAsset()
    await refreshCopilotChannelStatus()
    return true
  } catch (err) {
    accountActionError.value = err instanceof Error ? err.message : String(err)
    return false
  } finally {
    verifyingAccount.value = false
    addingCopilotChannel.value = false
  }
}

async function syncExistingCopilotAccountToken(sourceTarget: CopilotTarget, token: string, login: string) {
  const updates = copilotTargetOptions.value
    .filter(({ value }) => value !== sourceTarget)
    .map(({ value }) => {
      const channel = existingCopilotChannels.value[value]
      if (!channel) return null
      const hasSameLogin = buildBaseConfigs(channel).some(account => account.name === login)
      return hasSameLogin ? { target: value, channel } : null
    })
    .filter((item): item is { target: CopilotTarget; channel: Channel } => Boolean(item))

  await Promise.all(updates.map(({ target, channel }) => addAccount(target, channel, token, login)))
}

// 加入失败后用已授权 token 重试，避免重新走 OAuth。
function retryAddAccount() {
  const token = latestAuthorizedCopilotToken.value
  if (token && !copilotBusy.value) void processNewToken(token)
}

async function removeAccountConfirmed(key: string) {
  const target = selectedCopilotTarget.value
  const channel = existingCopilotChannels.value[target]
  if (!channel || removingKey.value) return
  accountActionError.value = ''
  pendingRemoveKey.value = ''
  removingKey.value = key
  try {
    await removeAccount(target, channel, key)
    await refreshCopilotChannelStatus()
  } catch (err) {
    accountActionError.value = err instanceof Error ? err.message : String(err)
  } finally {
    removingKey.value = ''
  }
}

watch(latestAuthorizedCopilotToken, (token) => {
  if (token) void processNewToken(token)
})

onMounted(() => {
  void refreshSavedCopilotAsset()
  void refreshCopilotChannelStatus()
})
</script>

<template>
  <div class="flex h-full min-h-0 flex-col gap-5">
    <div class="bg-glass dark:bg-glass-dark border border-border rounded-2xl p-5 shrink-0">
      <div class="flex items-start justify-between gap-4">
        <div>
          <div class="flex items-center gap-2 text-primary mb-2">
            <ShieldCheck class="w-4 h-4" />
            <span class="text-xs font-bold uppercase tracking-[0.2em]">{{ t('subscription.headerEyebrow') }}</span>
          </div>
          <h3 class="text-xl font-bold text-foreground">{{ t('subscription.title') }}</h3>
          <p class="text-sm text-muted-foreground mt-1 max-w-2xl">
            {{ t('subscription.description') }}
          </p>
        </div>
      </div>
    </div>

    <div class="grid grid-cols-1 gap-4 md:min-h-0 md:flex-1 md:overflow-hidden md:grid-cols-[280px_1fr]">
      <div class="space-y-1.5 md:min-h-0 md:overflow-y-auto md:overscroll-contain md:pr-1">
        <button
          type="button"
          :class="[
            'w-full rounded-xl border p-3 text-left transition-colors duration-200',
            selectedSubscription === 'github-copilot'
              ? 'border-border bg-secondary/60 dark:border-white/10 dark:bg-white/[0.04]'
              : 'border-border bg-card/40 hover:bg-card/70 dark:hover:bg-white/[0.03]',
          ]"
          @click="selectedSubscription = 'github-copilot'"
        >
          <div class="min-w-0">
            <div class="flex items-center justify-between gap-2">
              <span class="font-semibold text-foreground">GitHub Copilot</span>
              <span
                v-if="copilotOAuthSuccess || hasSavedCopilotAuthorization || copilotTotalAccountCount > 0"
                class="rounded border border-emerald-500/20 bg-emerald-500/10 px-1.5 py-0.5 text-[10px] text-emerald-700 dark:text-emerald-400"
              >
                {{ t('subscription.authorized') }}
              </span>
            </div>
            <p class="mt-1 truncate text-xs text-muted-foreground">{{ t('subscription.copilotDescription') }}</p>
            <p v-if="copilotTotalAccountCount > 0" class="mt-1 text-[11px] text-emerald-700 dark:text-emerald-400">
              {{ t('subscription.accountCountShort', { count: String(copilotTotalAccountCount) }) }}
            </p>
          </div>
        </button>
      </div>

      <section class="bg-glass dark:bg-glass-dark border border-border rounded-2xl p-5 space-y-5 md:min-h-0 md:overflow-y-auto md:overscroll-contain">
        <div class="space-y-3">
          <div class="min-w-0">
            <div class="flex flex-wrap items-center gap-2">
              <h4 class="text-base font-semibold text-foreground">GitHub Copilot</h4>
              <span class="rounded-full border border-blue-500/20 bg-blue-500/10 px-2 py-0.5 text-[10px] text-blue-700 dark:text-blue-300">
                {{ selectedCopilotTargetOption.label }}
              </span>
              <span
                v-if="copilotOAuthSuccess || hasSavedCopilotAuthorization || accountCount > 0"
                class="rounded border border-emerald-500/20 bg-emerald-500/10 px-1.5 py-0.5 text-[10px] text-emerald-700 dark:text-emerald-400"
              >
                {{ t('subscription.authorized') }}
              </span>
            </div>
            <p class="mt-1 text-sm text-muted-foreground">{{ t('subscription.copilotDescription') }}</p>
            <p class="mt-1 text-xs text-muted-foreground">{{ selectedCopilotTargetOption.description }}</p>
          </div>
        </div>

        <div class="space-y-2">
          <Label class="text-xs text-muted-foreground">{{ t('subscription.targetLabel') }}</Label>
          <div class="grid grid-cols-1 gap-2 sm:grid-cols-2">
            <button
              v-for="target in copilotTargetOptions"
              :key="target.value"
              type="button"
              :class="[
                'rounded-lg border px-3 py-2 text-left transition-colors',
                selectedCopilotTarget === target.value
                  ? 'border-primary/50 bg-primary/10 text-primary'
                  : 'border-border bg-background/70 text-foreground hover:bg-secondary/60',
              ]"
              @click="selectedCopilotTarget = target.value"
            >
              <div class="flex items-center justify-between gap-2">
                <span class="text-sm font-semibold">{{ target.label }}</span>
                <span
                  v-if="existingCopilotChannels[target.value]"
                  class="rounded border border-emerald-500/20 bg-emerald-500/10 px-1.5 py-0.5 text-[10px] text-emerald-700 dark:text-emerald-400"
                >
                  {{ t('subscription.channelExists') }}
                </span>
              </div>
              <p class="mt-1 text-xs text-muted-foreground">{{ target.description }}</p>
              <p v-if="copilotAccountCounts[target.value] > 0" class="mt-1 text-[11px] text-emerald-700 dark:text-emerald-400">
                {{ t('subscription.accountCountShort', { count: String(copilotAccountCounts[target.value]) }) }}
              </p>
            </button>
          </div>
        </div>

        <div class="space-y-1.5">
          <div class="space-y-1.5">
            <Label class="text-xs text-muted-foreground">{{ t('channelEditor.transport.proxyUrl.label') }}</Label>
            <Input
              v-model="copilotProxyUrl"
              class="font-mono text-xs"
              :placeholder="t('channelEditor.transport.proxyUrl.placeholder')"
            />
            <p class="text-xs text-muted-foreground">{{ t('channelEditor.transport.proxyUrl.hint') }}</p>
          </div>
        </div>

        <div class="space-y-2">
          <Label class="text-xs text-muted-foreground">
            {{ t('subscription.accountsTitle', { target: selectedCopilotTargetOption.label, count: String(accountCount) }) }}
          </Label>
          <div v-if="accountCount > 0" class="space-y-2">
            <div
              v-for="account in copilotAccounts"
              :key="account.key"
              class="flex items-center justify-between gap-2 rounded-lg border border-border bg-background/70 px-3 py-2"
            >
              <div class="min-w-0">
                <p class="truncate text-sm font-medium text-foreground">
                  {{ account.name || t('subscription.accountUnnamed') }}
                </p>
                <p class="truncate font-mono text-xs text-muted-foreground">{{ maskKey(account.key) }}</p>
              </div>
              <div class="flex shrink-0 items-center gap-1">
                <template v-if="pendingRemoveKey === account.key">
                  <button
                    type="button"
                    class="inline-flex h-7 items-center gap-1 rounded border border-destructive/40 px-2 text-xs text-destructive transition-colors hover:bg-destructive/10 disabled:opacity-50"
                    :disabled="Boolean(removingKey)"
                    @click="removeAccountConfirmed(account.key)"
                  >
                    <Loader2 v-if="removingKey === account.key" class="h-3.5 w-3.5 animate-spin" />
                    <Check v-else class="h-3.5 w-3.5" />
                    {{ t('subscription.accountRemoveConfirm') }}
                  </button>
                  <button
                    type="button"
                    class="inline-flex h-7 w-7 items-center justify-center rounded border border-border text-muted-foreground transition-colors hover:text-foreground"
                    :aria-label="t('common.cancel')"
                    @click="pendingRemoveKey = ''"
                  >
                    <X class="h-3.5 w-3.5" />
                  </button>
                </template>
                <button
                  v-else
                  type="button"
                  class="inline-flex h-7 w-7 items-center justify-center rounded border border-border text-muted-foreground transition-colors hover:border-destructive/40 hover:text-destructive disabled:opacity-50"
                  :title="t('subscription.accountRemove')"
                  :aria-label="t('subscription.accountRemove')"
                  :disabled="Boolean(removingKey) || copilotBusy"
                  @click="pendingRemoveKey = account.key"
                >
                  <Trash2 class="h-3.5 w-3.5" />
                </button>
              </div>
            </div>
          </div>
          <div v-else class="rounded-lg border border-dashed border-border bg-background/50 px-3 py-4 text-sm text-muted-foreground">
            {{ t('subscription.accountsEmpty', { target: selectedCopilotTargetOption.label }) }}
          </div>
        </div>

        <div v-if="copilotUserCode" class="flex flex-wrap items-center gap-2 text-sm">
          <span class="text-muted-foreground">{{ t('copilotOAuth.userCode') }}</span>
          <code class="rounded bg-muted px-2 py-0.5 font-mono text-xs">{{ copilotUserCode }}</code>
          <button
            type="button"
            class="inline-flex h-6 w-6 items-center justify-center rounded border border-border text-muted-foreground transition-colors hover:text-foreground"
            :title="copilotUserCodeCopied ? t('common.copied') : t('common.copy')"
            :aria-label="copilotUserCodeCopied ? t('common.copied') : t('common.copy')"
            @click="copyCopilotUserCode"
          >
            <CheckCircle2 v-if="copilotUserCodeCopied" class="h-3.5 w-3.5 text-emerald-700 dark:text-emerald-400" />
            <Copy v-else class="h-3.5 w-3.5" />
          </button>
          <button type="button" class="text-xs text-primary underline" @click="openCopilotAuthorization">
            {{ t('copilotOAuth.openAuthorize') }}
          </button>
        </div>

        <template v-if="copilotOAuthError">
          <p class="text-xs text-destructive">{{ copilotOAuthError }}</p>
        </template>
        <template v-else-if="accountActionError || copilotCreateError || error">
          <p class="text-xs text-destructive">{{ accountActionError || copilotCreateError || error }}</p>
        </template>
        <template v-else-if="verifyingAccount">
          <p class="text-xs text-muted-foreground">{{ t('subscription.verifying') }}</p>
        </template>
        <template v-else-if="checkingCopilotChannel">
          <p class="text-xs text-muted-foreground">{{ t('subscription.checkingChannel') }}</p>
        </template>
        <template v-else-if="addingCopilotChannel">
          <p class="text-xs text-muted-foreground">{{ t('subscription.addingCopilotAccount') }}</p>
        </template>
        <template v-else-if="accountCount > 0">
          <p class="text-xs text-emerald-600">
            {{ t('subscription.accountsSummary', { target: selectedCopilotTargetOption.label, count: String(accountCount) }) }}
          </p>
        </template>
        <template v-else-if="availableCopilotToken">
          <p class="text-xs text-emerald-600">
            {{ t('subscription.copilotAuthorizationSavedOnly', { target: selectedCopilotTargetOption.label }) }}
          </p>
        </template>

        <div class="flex flex-wrap items-center gap-2">
          <Button :disabled="copilotBusy" @click="handleCopilotPrimaryAction">
            <Loader2 v-if="copilotBusy" class="mr-1.5 h-3.5 w-3.5 animate-spin" />
            {{ copilotPrimaryActionLabel }}
          </Button>
          <button
            v-if="copilotPolling || copilotOAuthLoading"
            type="button"
            class="text-xs text-muted-foreground underline"
            @click="cancelCopilotAuthorization"
          >
            {{ t('copilotOAuth.cancel') }}
          </button>
          <button
            v-else-if="accountActionError && latestAuthorizedCopilotToken && !copilotBusy"
            type="button"
            class="text-xs text-primary underline"
            @click="retryAddAccount"
          >
            {{ t('subscription.retryAddAccount') }}
          </button>
        </div>
      </section>
    </div>
  </div>
</template>
