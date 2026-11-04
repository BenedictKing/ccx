<template>
  <section class="space-y-3 rounded-xl border border-primary/20 bg-gradient-to-b from-primary/[0.02] to-transparent p-5 shadow-sm">
    <div>
      <Label class="text-xs font-semibold text-muted-foreground">
        {{ t('channelEditor.nav.redirect') }}
      </Label>
      <p class="mt-1 text-[10px] leading-4 text-muted-foreground">
        {{ t('channelEditor.mapping.hint') }}
      </p>
    </div>
    <div v-for="row in rows" :key="row.id" class="flex items-center gap-2">
      <Input
        :model-value="row.source"
        :placeholder="t('channelEditor.mapping.source.placeholder')"
        class="h-9 font-mono text-xs"
        @update:model-value="updateRow(row.id, 'source', String($event))"
      />
      <ArrowRight class="h-4 w-4 shrink-0 text-primary" />
      <Input
        :model-value="row.target"
        :placeholder="t('channelEditor.mapping.target.placeholder')"
        class="h-9 font-mono text-xs"
        @update:model-value="updateRow(row.id, 'target', String($event))"
      />
      <Button type="button" variant="ghost" size="icon" class="size-8 shrink-0 text-destructive" @click="removeRow(row.id)">
        <X class="h-4 w-4" />
      </Button>
    </div>
    <Button
      type="button"
      variant="outline"
      size="sm"
      class="h-8"
      :disabled="rows.some(row => !row.source.trim() || !row.target.trim())"
      @click="addRow"
    >
      <Plus class="mr-1 h-4 w-4" />
      {{ t('channelEditor.mapping.addRedirect') }}
    </Button>
  </section>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { ArrowRight, Plus, X } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { useLanguage } from '@/composables/useLanguage'

interface MappingRow {
  id: number
  source: string
  target: string
}

const props = defineProps<{
  modelValue: Record<string, string>
}>()
const emit = defineEmits<{
  'update:modelValue': [value: Record<string, string>]
}>()
const { t } = useLanguage()
const rows = ref<MappingRow[]>([])
let nextId = 1

function fromRecord(value: Record<string, string>): MappingRow[] {
  return Object.entries(value).map(([source, target]) => ({ id: nextId++, source, target }))
}
function toRecord() {
  return Object.fromEntries(rows.value
    .map(row => ({ source: row.source.trim(), target: row.target.trim() }))
    .filter(row => row.source && row.target)
    .map(row => [row.source, row.target]))
}
function emitRows() {
  emit('update:modelValue', toRecord())
}
function addRow() {
  rows.value.push({ id: nextId++, source: '', target: '' })
}
function removeRow(id: number) {
  rows.value = rows.value.filter(row => row.id !== id)
  emitRows()
}
function updateRow(id: number, field: 'source' | 'target', value: string) {
  const row = rows.value.find(item => item.id === id)
  if (!row) return
  row[field] = value
  emitRows()
}

rows.value = fromRecord(props.modelValue)
watch(() => props.modelValue, value => {
  if (JSON.stringify(value) !== JSON.stringify(toRecord())) {
    rows.value = fromRecord(value)
  }
})
</script>
