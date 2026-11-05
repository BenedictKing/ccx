<template>
  <div class="site-onboarding-view">
    <v-card class="mb-4">
      <v-card-title>{{ t('siteOnboarding.title') }}</v-card-title>
      <v-card-subtitle>{{ t('siteOnboarding.subtitle') }}</v-card-subtitle>
      <v-card-text>
        <SiteOnboardingWizard @provisioned="onProvisioned" />
      </v-card-text>
    </v-card>

    <v-card>
      <v-card-title>{{ t('siteOnboarding.accounts') }}</v-card-title>
      <v-card-text>
        <v-table v-if="accounts.length > 0" density="compact">
          <thead>
            <tr>
              <th>{{ t('siteOnboarding.baseUrl') }}</th>
              <th>{{ t('siteOnboarding.accessToken') }}</th>
              <th>{{ t('siteOnboarding.provisioned') }}</th>
              <th>{{ t('siteOnboarding.balance') }}</th>
              <th>{{ t('siteOnboarding.actions') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="account in accounts" :key="account.uid">
              <td>{{ account.baseUrl }}</td>
              <td>{{ account.maskedAccessToken }}</td>
              <td>
                <v-chip v-for="key in account.provisionedKeys ?? []" :key="key.protocol + key.group" size="x-small" class="mr-1" variant="outlined">
                  {{ key.protocol }}/{{ key.group }}
                </v-chip>
              </td>
              <td>
                <v-btn size="small" variant="text" @click="showBalance(account)">{{ t('siteOnboarding.balance') }}</v-btn>
              </td>
              <td>
                <v-btn size="small" variant="text" @click="verify(account)">{{ t('siteOnboarding.verify') }}</v-btn>
                <v-btn size="small" variant="text" color="error" @click="remove(account)">{{ t('siteOnboarding.delete') }}</v-btn>
              </td>
            </tr>
          </tbody>
        </v-table>
        <div v-else class="text-medium-emphasis">{{ t('siteOnboarding.noAccounts') }}</div>
        <v-alert v-if="actionMessage" variant="tonal" density="compact" class="mt-3">{{ actionMessage }}</v-alert>
      </v-card-text>
    </v-card>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from '../i18n'
import api from '../services/api'
import type { SiteAccount } from '../services/api-types'
import SiteOnboardingWizard from '../components/SiteOnboardingWizard.vue'

const { t } = useI18n()
const accounts = ref<SiteAccount[]>([])
const actionMessage = ref('')

async function refresh() {
  const { accounts: list } = await api.listSiteAccounts()
  accounts.value = list
}

async function onProvisioned() {
  await refresh()
}

async function verify(account: SiteAccount) {
  const result = await api.verifySiteAccount(account.uid)
  actionMessage.value = result.verified
    ? t('siteOnboarding.verifyOk', { url: account.baseUrl })
    : (result.error || t('siteOnboarding.realVerifyFailed'))
}

async function showBalance(account: SiteAccount) {
  const result = await api.getSiteAccountBalance(account.uid)
  actionMessage.value = result.identified
    ? `${account.baseUrl}: ${result.balance} (${result.currency})`
    : (result.error || t('siteOnboarding.realVerifyFailed'))
}

async function remove(account: SiteAccount) {
  await api.deleteSiteAccount(account.uid)
  await refresh()
}

onMounted(refresh)
</script>
