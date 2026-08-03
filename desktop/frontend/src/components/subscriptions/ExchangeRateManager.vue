<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ArrowRightLeft, Loader2, Plus, Save, Trash2 } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useAdminApi } from '@/composables/useAdminApi'
import { useLanguage } from '@/composables/useLanguage'
import type { ExchangeRateQuote, ExchangeRatesResponse } from '@/services/admin-api'

const { t } = useLanguage()
const api = useAdminApi()
const quotes = ref<ExchangeRateQuote[]>([])
const snapshot = ref<ExchangeRatesResponse['snapshot']>()
const source = ref('')
const loading = ref(false)
const saving = ref(false)
const error = ref('')
const success = ref('')

function addQuote() {
  quotes.value.push({ sourceAmount: 1, sourceUnit: '', targetAmount: 1, targetUnit: 'USD' })
}
function removeQuote(index: number) { quotes.value.splice(index, 1) }
function validQuote(quote: ExchangeRateQuote) {
  return Number.isFinite(quote.sourceAmount) && quote.sourceAmount > 0
    && Number.isFinite(quote.targetAmount) && quote.targetAmount > 0
    && !!quote.sourceUnit.trim() && !!quote.targetUnit.trim()
}
async function load() {
  loading.value = true; error.value = ''
  try {
    const result = await api.getExchangeRates()
    quotes.value = result.quotes.map(quote => ({ ...quote }))
    snapshot.value = result.snapshot
    source.value = result.source || ''
  } catch (cause) { error.value = cause instanceof Error ? cause.message : String(cause) }
  finally { loading.value = false }
}
async function save() {
  if (!quotes.value.every(validQuote)) { error.value = t('exchangeRates.invalidQuote'); return }
  saving.value = true; error.value = ''; success.value = ''
  try {
    const result = await api.replaceExchangeRates({ quotes: quotes.value, expectedSnapshotVersion: snapshot.value?.version })
    quotes.value = result.quotes.map(quote => ({ ...quote }))
    snapshot.value = result.snapshot
    source.value = result.source || ''
    success.value = t('exchangeRates.saved')
  } catch (cause) { error.value = cause instanceof Error ? cause.message : String(cause) }
  finally { saving.value = false }
}
onMounted(load)
</script>

<template>
  <section class="space-y-4 rounded-xl border border-border bg-card/40 p-4">
    <div class="flex items-center justify-between gap-3">
      <div><div class="flex items-center gap-2 text-sm font-semibold"><ArrowRightLeft class="h-4 w-4 text-primary" />{{ t('exchangeRates.title') }}</div><p class="mt-1 text-xs text-muted-foreground">{{ t('exchangeRates.description') }}</p></div>
      <Button size="sm" variant="outline" @click="addQuote"><Plus class="h-3.5 w-3.5" />{{ t('exchangeRates.add') }}</Button>
    </div>
    <div v-if="loading" class="flex items-center gap-2 text-xs text-muted-foreground"><Loader2 class="h-4 w-4 animate-spin" />{{ t('common.loading') }}</div>
    <div v-else class="space-y-2">
      <div v-for="(quote, index) in quotes" :key="index" class="grid grid-cols-[1fr_1.2fr_auto_1fr_1.2fr_auto] items-center gap-2 rounded-lg border border-border/70 bg-background/60 p-2">
        <Input v-model.number="quote.sourceAmount" type="number" min="0" step="any" :aria-label="t('exchangeRates.sourceAmount')" />
        <Input v-model="quote.sourceUnit" :placeholder="t('exchangeRates.sourceUnit')" />
        <span class="text-xs text-muted-foreground">→</span>
        <Input v-model.number="quote.targetAmount" type="number" min="0" step="any" :aria-label="t('exchangeRates.targetAmount')" />
        <Input v-model="quote.targetUnit" :placeholder="t('exchangeRates.targetUnit')" />
        <Button size="icon-sm" variant="ghost" class="text-destructive" @click="removeQuote(index)"><Trash2 class="h-3.5 w-3.5" /></Button>
      </div>
      <p v-if="quotes.length === 0" class="rounded-lg border border-dashed p-3 text-xs text-muted-foreground">{{ t('exchangeRates.empty') }}</p>
    </div>
    <div v-if="snapshot" class="rounded-lg border border-border/60 bg-secondary/30 p-3 text-xs">
      <div class="mb-2 text-muted-foreground">{{ t('exchangeRates.snapshot', { version: snapshot.version }) }}<span v-if="source"> · {{ source }}</span></div>
      <div class="flex flex-wrap gap-2"><span v-for="(usd, unit) in snapshot.usdUnitPrices" :key="unit" class="rounded border border-border bg-background px-2 py-1 font-mono">1 {{ unit }} = ${{ usd }}</span></div>
    </div>
    <p v-if="error" class="text-xs text-destructive">{{ error }}</p><p v-if="success" class="text-xs text-emerald-600">{{ success }}</p>
    <Button size="sm" :disabled="saving" @click="save"><Loader2 v-if="saving" class="h-3.5 w-3.5 animate-spin" /><Save v-else class="h-3.5 w-3.5" />{{ t('exchangeRates.replace') }}</Button>
  </section>
</template>
