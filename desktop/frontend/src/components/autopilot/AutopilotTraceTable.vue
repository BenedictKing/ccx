<script setup lang="ts">
import { computed, ref } from 'vue'
import { RefreshCw } from 'lucide-vue-next'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { useLanguage } from '@/composables/useLanguage'
import type { TraceSummary } from '@/services/admin-api'

const props = defineProps<{
  traces: TraceSummary[]
  loading: boolean
}>()

const emit = defineEmits<{
  refresh: []
  select: [traceUid: string]
}>()

const { t } = useLanguage()

const mismatchOnly = ref(false)

const filteredTraces = computed(() => {
  if (!mismatchOnly.value) return props.traces
  return props.traces.filter((tr) => tr.comparisonStatus === 'mismatched')
})

function formatTime(iso: string): string {
  if (!iso) return '-'
  try {
    return new Date(iso).toLocaleString()
  } catch {
    return iso
  }
}

function shortenUid(uid?: string): string {
  if (!uid) return '-'
  const stripped = uid.replace(/^ch_/, '')
  return stripped.length > 8 ? `${stripped.slice(0, 8)}...` : stripped
}

function comparisonLabel(status: string): string {
  if (status === 'matched') return t('autopilot.traceTable.yes')
  if (status === 'mismatched') return t('autopilot.traceTable.no')
  return '-'
}

function comparisonClass(status: string): string {
  if (status === 'matched') return 'border-emerald-500 text-emerald-600'
  if (status === 'mismatched') return 'border-red-500 text-red-600'
  return 'border-muted-foreground text-muted-foreground'
}

function outcomeClass(outcome?: string): string {
  if (outcome === 'success') return 'border-emerald-500 text-emerald-600'
  if (outcome === 'cancelled') return 'border-muted-foreground text-muted-foreground'
  if (outcome === 'attempt_failed') return 'border-amber-500 text-amber-600'
  return 'border-red-500 text-red-600'
}
</script>

<template>
  <div class="rounded-xl border border-border/60 bg-card/40 p-4">
    <div class="mb-3 flex flex-wrap items-center justify-between gap-2">
      <h4 class="text-sm font-bold">{{ t('autopilot.traceTable.title') }}</h4>
      <div class="flex items-center gap-3">
        <div class="flex items-center gap-2">
          <Switch
            :model-value="mismatchOnly"
            @update:model-value="mismatchOnly = Boolean($event)"
          />
          <span class="text-xs text-muted-foreground">{{ t('autopilot.traceTable.mismatchOnly') }}</span>
        </div>
        <Button variant="outline" size="sm" :disabled="loading" @click="emit('refresh')">
          <RefreshCw class="size-3.5" :class="{ 'animate-spin': loading }" />
          {{ t('app.actions.refresh') }}
        </Button>
      </div>
    </div>

    <div v-if="filteredTraces.length === 0 && !loading" class="py-8 text-center text-sm text-muted-foreground">
      {{ t('autopilot.traceTable.empty') }}
    </div>

    <div v-else class="overflow-hidden rounded-lg border border-border/50">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{{ t('autopilot.traceTable.col.time') }}</TableHead>
            <TableHead>{{ t('autopilot.traceTable.col.kind') }}</TableHead>
            <TableHead>{{ t('autopilot.traceTable.col.taskClass') }}</TableHead>
            <TableHead>{{ t('autopilot.traceTable.col.model') }}</TableHead>
            <TableHead>Actual Model</TableHead>
            <TableHead>{{ t('autopilot.traceTable.col.shadowVsActual') }}</TableHead>
            <TableHead>{{ t('autopilot.traceTable.col.match') }}</TableHead>
            <TableHead>{{ t('autopilot.traceTable.col.mode') }}</TableHead>
            <TableHead class="w-[90px]">{{ t('autopilot.traceTable.col.outcome') }}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow
            v-for="trace in filteredTraces"
            :key="trace.traceUid"
            class="cursor-pointer"
            @click="emit('select', trace.traceUid)"
          >
            <TableCell class="text-xs">{{ formatTime(trace.createdAt) }}</TableCell>
            <TableCell>
              <Badge variant="outline">{{ trace.requestKind }}</Badge>
            </TableCell>
            <TableCell>
              <Badge variant="secondary">{{ trace.taskClass || '-' }}</Badge>
            </TableCell>
            <TableCell class="max-w-[160px] truncate text-xs">{{ trace.requestedModel || '-' }}</TableCell>
            <TableCell class="max-w-[160px] truncate text-xs">{{ trace.actualModel || '-' }}</TableCell>
            <TableCell>
              <div class="flex items-center gap-1 text-xs">
                <Badge variant="secondary">{{ shortenUid(trace.recommendedChannelUid || trace.actualChannelUid) }}</Badge>
                <span class="text-muted-foreground">→</span>
                <Badge variant="outline">{{ shortenUid(trace.actualChannelUid) }}</Badge>
              </div>
            </TableCell>
            <TableCell>
              <Badge
                v-if="trace.comparisonStatus !== 'uncompared'"
                variant="outline"
                :class="comparisonClass(trace.comparisonStatus)"
              >
                {{ comparisonLabel(trace.comparisonStatus) }}
              </Badge>
              <span v-else class="text-xs text-muted-foreground">-</span>
            </TableCell>
            <TableCell>
              <Badge variant="outline">{{ t(`autopilot.mode.${trace.mode}`) || trace.mode }}</Badge>
            </TableCell>
            <TableCell>
              <Badge
                v-if="trace.outcome"
                variant="outline"
                :class="outcomeClass(trace.outcome)"
              >
                {{ trace.outcome }}
              </Badge>
              <span v-else class="text-xs text-muted-foreground">-</span>
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </div>
  </div>
</template>
