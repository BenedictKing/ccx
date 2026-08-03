<template>
  <v-card variant="outlined" class="mt-6">
    <v-card-title class="d-flex align-center justify-space-between ga-3 flex-wrap">
      <span>{{ t('subscription.exchangeRates.title') }}</span>
      <v-btn size="small" variant="text" :loading="loading" @click="load">{{ t('app.actions.refresh') }}</v-btn>
    </v-card-title>
    <v-card-text>
      <v-alert v-if="error" color="error" variant="tonal" density="compact" class="mb-3">{{ error }}</v-alert>
      <div v-if="snapshot" class="text-caption text-medium-emphasis mb-3">
        {{ t('subscription.exchangeRates.snapshot', { version: snapshot.version, builtAt: formatTime(snapshot.builtAt) }) }}
        <div v-if="usdPrices.length">{{ t('subscription.exchangeRates.usdPrices') }}: {{ usdPrices.join(' · ') }}</div>
      </div>
      <div v-for="(quote, index) in quotes" :key="index" class="rate-row mb-3">
        <v-text-field v-model.number="quote.sourceAmount" type="number" min="0" step="any" density="compact" variant="outlined" :label="t('subscription.exchangeRates.sourceAmount')" />
        <v-text-field v-model="quote.sourceUnit" density="compact" variant="outlined" :label="t('subscription.exchangeRates.sourceUnit')" />
        <v-text-field v-model.number="quote.targetAmount" type="number" min="0" step="any" density="compact" variant="outlined" :label="t('subscription.exchangeRates.targetAmount')" />
        <v-text-field v-model="quote.targetUnit" density="compact" variant="outlined" :label="t('subscription.exchangeRates.targetUnit')" />
        <v-btn icon size="small" variant="text" color="error" :title="t('app.actions.delete')" @click="remove(index)"><v-icon>mdi-delete</v-icon></v-btn>
      </div>
      <div class="d-flex ga-2 justify-end">
        <v-btn variant="text" @click="add">{{ t('subscription.exchangeRates.add') }}</v-btn>
        <v-btn color="primary" :loading="saving" @click="save">{{ t('app.actions.save') }}</v-btn>
      </div>
    </v-card-text>
  </v-card>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from '@/i18n'
import { api, ApiError } from '@/services/api'
import type { ExchangeRateQuote, ExchangeRateSnapshot } from '@/services/api-types'
import { defaultExchangeRateQuotes, normalizeExchangeRateQuotes } from '@/utils/exchangeRates'

const { t } = useI18n()
const quotes = ref<ExchangeRateQuote[]>(defaultExchangeRateQuotes())
const snapshot = ref<ExchangeRateSnapshot>()
const loading = ref(false)
const saving = ref(false)
const error = ref('')

const usdPrices = computed(() => Object.entries(snapshot.value?.usdUnitPrices || {}).map(([unit, price]) => `1 ${unit} = ${price.toPrecision(6)} USD`))

function formatTime(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}
function add() { quotes.value.push({ sourceAmount: 1, sourceUnit: '', targetAmount: 1, targetUnit: '' }) }
function remove(index: number) { quotes.value.splice(index, 1) }

async function load() {
  loading.value = true
  error.value = ''
  try {
    const response = await api.getExchangeRates()
    quotes.value = response.quotes.length ? response.quotes.map(item => ({ ...item })) : defaultExchangeRateQuotes()
    snapshot.value = response.snapshot
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : String(cause)
  } finally { loading.value = false }
}

async function save() {
  saving.value = true
  error.value = ''
  try {
    const response = await api.replaceExchangeRates({
      quotes: normalizeExchangeRateQuotes(quotes.value),
      expectedSnapshotVersion: snapshot.value?.version,
    })
    quotes.value = response.quotes.map(item => ({ ...item }))
    snapshot.value = response.snapshot
  } catch (cause) {
    error.value = cause instanceof ApiError && cause.status === 409
      ? t('subscription.exchangeRates.versionConflict')
      : cause instanceof Error ? cause.message : String(cause)
  } finally { saving.value = false }
}

onMounted(load)
</script>

<style scoped>
.rate-row { display: grid; grid-template-columns: 1fr 1fr 1fr 1fr auto; gap: 8px; align-items: start; }
@media (max-width: 760px) { .rate-row { grid-template-columns: 1fr 1fr; } }
</style>
