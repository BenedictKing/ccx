<template>
  <v-card variant="outlined" rounded="lg">
    <v-card-title class="d-flex align-center ga-2 pa-4 pb-2">
      <v-icon color="primary">mdi-swap-horizontal</v-icon>
      <span>{{ t('channelEditor.nav.redirect') }}</span>
    </v-card-title>
    <v-card-text class="pt-2">
      <p class="text-body-2 text-medium-emphasis mb-4">
        {{ t('channelEditor.mapping.hint') }}
      </p>
      <div v-for="row in rows" :key="row.id" class="d-flex align-center ga-2 mb-3">
        <v-text-field
          :model-value="row.source"
          :label="t('channelEditor.mapping.source.label')"
          :placeholder="t('channelEditor.mapping.source.placeholder')"
          variant="outlined"
          density="comfortable"
          hide-details
          @update:model-value="updateRow(row.id, 'source', $event)"
        />
        <v-icon color="primary">mdi-arrow-right</v-icon>
        <v-text-field
          :model-value="row.target"
          :label="t('channelEditor.mapping.target.label')"
          :placeholder="t('channelEditor.mapping.target.placeholder')"
          variant="outlined"
          density="comfortable"
          hide-details
          @update:model-value="updateRow(row.id, 'target', $event)"
        />
        <v-btn icon="mdi-close" variant="text" color="error" @click="removeRow(row.id)" />
      </div>
      <v-btn
        color="primary"
        variant="tonal"
        prepend-icon="mdi-plus"
        :disabled="rows.some(row => !row.source.trim() || !row.target.trim())"
        @click="addRow"
      >
        {{ t('channelEditor.mapping.addRedirect') }}
      </v-btn>
    </v-card-text>
  </v-card>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from '../../i18n'

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
const { t } = useI18n()
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
