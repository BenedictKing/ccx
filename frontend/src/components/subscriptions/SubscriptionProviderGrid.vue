<template>
  <div class="subscription-provider-grid">
    <div class="d-flex flex-wrap ga-4">
      <v-card
        class="provider-card pa-4 cursor-pointer"
        variant="outlined"
        :class="{ 'provider-card--active': selectedProvider === 'github-copilot' }"
        @click="selectProvider('github-copilot')"
      >
        <div class="d-flex align-center ga-3">
          <v-icon size="32" color="primary">mdi-github</v-icon>
          <div>
            <div class="text-subtitle-1 font-weight-bold">GitHub Copilot</div>
            <div class="text-caption text-medium-emphasis">{{ t('subscription.copilotDescription') }}</div>
          </div>
        </div>
      </v-card>

      <v-card
        v-for="sponsor in sponsorCards"
        :key="`sponsor-${sponsor.providerId}`"
        class="provider-card pa-4 d-flex flex-column provider-card--sponsor"
        variant="outlined"
      >
        <div class="d-flex align-center ga-3 mb-2">
          <img :src="sponsorLogos[sponsor.providerId]" :alt="sponsor.displayName" class="sponsor-logo flex-shrink-0" />
          <div class="text-subtitle-1 font-weight-bold">{{ sponsor.displayName }}</div>
          <v-chip size="x-small" color="deep-purple" variant="tonal" class="ml-auto">
            {{ t('subscription.sponsorBadge') }}
          </v-chip>
        </div>
        <div class="text-caption text-medium-emphasis mb-3 provider-card__desc">
          {{ sponsor.description }}
        </div>
        <v-spacer />
        <div class="d-flex align-center ga-2 mt-auto">
          <v-btn
            v-if="sponsor.hasTemplate"
            size="small"
            color="primary"
            variant="flat"
            @click="emit('add', sponsor.providerId)"
          >
            {{ t('subscription.addProvider') }}
          </v-btn>
          <v-btn
            v-if="providerPromotionLinks[sponsor.providerId]"
            size="small"
            :color="sponsor.hasTemplate ? 'secondary' : 'deep-purple'"
            :variant="sponsor.hasTemplate ? 'text' : 'flat'"
            append-icon="mdi-open-in-new"
            @click="openProviderPromotion(sponsor.providerId)"
          >
            {{ t('subscription.visitSite') }}
          </v-btn>
          <v-btn
            v-if="providerConsoleLinks[sponsor.providerId]"
            size="small"
            variant="text"
            append-icon="mdi-open-in-new"
            @click="openProviderConsole(sponsor.providerId)"
          >
            {{ t('subscription.visitConsole') }}
          </v-btn>
        </div>
      </v-card>

      <v-card
        v-for="provider in otherProviders"
        :key="provider.providerId"
        class="provider-card pa-4 d-flex flex-column"
        variant="outlined"
      >
        <div class="d-flex align-center ga-3 mb-2">
          <v-icon size="32" color="secondary">mdi-domain</v-icon>
          <div class="text-subtitle-1 font-weight-bold">{{ provider.displayName }}</div>
        </div>
        <div class="text-caption text-medium-emphasis mb-3 provider-card__desc">
          {{ provider.description }}
        </div>
        <v-spacer />
        <div class="d-flex align-center ga-2 mt-auto">
          <v-btn
            size="small"
            color="primary"
            variant="flat"
            @click="emit('add', provider.providerId)"
          >
            {{ t('subscription.addProvider') }}
          </v-btn>
          <v-btn
            v-if="providerConsoleLinks[provider.providerId]"
            size="small"
            variant="text"
            append-icon="mdi-open-in-new"
            @click="openProviderConsole(provider.providerId)"
          >
            {{ t('subscription.visitConsole') }}
          </v-btn>
          <v-btn
            v-if="providerPromotionLinks[provider.providerId]"
            size="small"
            variant="text"
            color="secondary"
            append-icon="mdi-open-in-new"
            @click="openProviderPromotion(provider.providerId)"
          >
            {{ t('subscription.visitSite') }}
          </v-btn>
        </div>
      </v-card>

      <v-card
        class="provider-card pa-4 cursor-pointer"
        variant="outlined"
        :class="{ 'provider-card--active': selectedProvider === 'new-api' }"
        @click="selectProvider('new-api')"
      >
        <div class="d-flex align-center ga-3">
          <v-icon size="32" color="warning">mdi-server-network</v-icon>
          <div>
            <div class="text-subtitle-1 font-weight-bold">new-api</div>
            <div class="text-caption text-medium-emphasis">{{ t('subscription.newApiDescription') }}</div>
          </div>
        </div>
      </v-card>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from '@/i18n'
import { getProviderTemplates, type ProviderTemplate } from '@/services/autopilot-api'
import {
  providerConsoleLinks,
  providerPromotionLinks,
  openProviderConsole,
  openProviderPromotion,
} from '@/utils/provider-links'
import runapiLogo from '@/assets/runapi.svg'
import compshareLogo from '@/assets/compshare.png'
import volcengineLogo from '@/assets/volc-ark.png'

const { t } = useI18n()
const emit = defineEmits<{
  select: [provider: string]
  add: [providerId: string]
}>()
const selectedProvider = ref('')

// 服务商模板（从 autopilot 模板表加载）
const builtinProviders = ref<ProviderTemplate[]>([])

// 赞助商 logo（真实品牌图）：内置 provider 与独立赞助卡共用
const sponsorLogos: Record<string, string> = {
  compshare: compshareLogo,
  volcengine: volcengineLogo,
  runapi: runapiLogo,
}

// 赞助商展示顺序与 README 赞助商表格保持一致，排在服务商列表最前
const sponsorOrder = [
  { providerId: 'volcengine', displayName: '火山引擎' },
  { providerId: 'compshare', displayName: '优云智算' },
  { providerId: 'runapi', displayName: 'RunAPI' },
]

interface SponsorCard {
  providerId: string
  displayName: string
  description: string
  hasTemplate: boolean
}

// 赞助商卡片：有内置模板则复用模板文案与添加按钮，否则退化为纯推广卡
const sponsorCards = computed<SponsorCard[]>(() =>
  sponsorOrder.map(sponsor => {
    const template = builtinProviders.value.find(item => item.providerId === sponsor.providerId)
    return {
      providerId: sponsor.providerId,
      displayName: template?.displayName || sponsor.displayName,
      description: template?.description || t(`subscription.sponsors.${sponsor.providerId}.description`),
      hasTemplate: !!template,
    }
  })
)

// 非赞助商模板，保持后端返回顺序
const otherProviders = computed(() =>
  builtinProviders.value.filter(item => !(item.providerId in sponsorLogos))
)

function selectProvider(provider: string) {
  selectedProvider.value = provider
  emit('select', provider)
}

onMounted(async () => {
  try {
    builtinProviders.value = await getProviderTemplates()
  } catch (err) {
    console.error('[Subscription-Providers] 加载服务商失败:', err)
    builtinProviders.value = []
  }
})
</script>

<style scoped>
.provider-card {
  min-width: 240px;
  max-width: 300px;
  flex: 1;
  transition: all 0.2s ease;
}
.provider-card:hover {
  border-color: rgb(var(--v-theme-primary));
  background-color: rgba(var(--v-theme-primary), 0.04);
}
.provider-card--active {
  border-color: rgb(var(--v-theme-primary));
  background-color: rgba(var(--v-theme-primary), 0.08);
}
.provider-card--sponsor:hover {
  border-color: rgb(var(--v-theme-deep-purple, var(--v-theme-secondary)));
}
.provider-card__desc {
  display: -webkit-box;
  -webkit-line-clamp: 3;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.cursor-pointer {
  cursor: pointer;
}
.sponsor-logo {
  width: 32px;
  height: 32px;
  border-radius: 6px;
  object-fit: cover;
  display: block;
}
</style>
