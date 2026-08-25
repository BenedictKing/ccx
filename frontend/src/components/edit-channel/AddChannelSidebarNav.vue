<template>
  <div class="sidebar-nav">
    <div class="sidebar-nav-title">{{ title }}</div>
    <button
      v-for="item in sections"
      :key="item.id"
      type="button"
      :class="[
        'sidebar-nav-item',
        activeSection === item.id && 'sidebar-nav-item--active',
      ]"
      @click="$emit('navigate', item.id)"
    >
      <div v-if="activeSection === item.id" class="sidebar-nav-indicator"></div>
      <span class="sidebar-nav-label">{{ item.label }}</span>
    </button>
  </div>
</template>

<script setup lang="ts">
interface NavSection {
  id: string
  label: string
}

interface Props {
  title?: string
  sections: NavSection[]
  activeSection?: string
}

withDefaults(defineProps<Props>(), {
  title: '',
  activeSection: '',
})

defineEmits<{
  navigate: [sectionId: string]
}>()
</script>

<style scoped>
.sidebar-nav {
  width: 190px;
  min-width: 190px;
  flex-shrink: 0;
  border-right: 1px solid rgba(var(--v-border-color), 0.12);
  background: rgba(var(--v-theme-surface), 1);
  overflow-y: auto;
  padding: 16px 12px;
}

.sidebar-nav-title {
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.12em;
  text-transform: uppercase;
  color: rgba(var(--v-theme-on-surface), 0.4);
  padding: 0 12px 8px;
}

.sidebar-nav-item {
  display: flex;
  align-items: center;
  width: 100%;
  padding: 8px 12px;
  border-radius: 6px;
  font-size: 0.8125rem;
  font-weight: 500;
  color: rgba(var(--v-theme-on-surface), 0.65);
  background: transparent;
  border: 1px solid transparent;
  cursor: pointer;
  transition: all 0.2s ease;
  text-align: left;
  position: relative;
  margin-bottom: 2px;
}

.sidebar-nav-item:hover {
  color: rgb(var(--v-theme-on-surface));
  background: rgba(var(--v-theme-on-surface), 0.04);
}

.sidebar-nav-item--active {
  color: rgb(var(--v-theme-primary));
  background: rgba(var(--v-theme-primary), 0.08);
  border-color: rgba(var(--v-theme-primary), 0.12);
  font-weight: 600;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.04);
}

.sidebar-nav-indicator {
  position: absolute;
  left: 0;
  top: 8px;
  bottom: 8px;
  width: 3px;
  border-radius: 0 3px 3px 0;
  background: rgb(var(--v-theme-primary));
  box-shadow: 0 0 8px rgba(var(--v-theme-primary), 0.4);
}

.sidebar-nav-label {
  line-height: 1.3;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

@media (max-width: 960px) {
  .sidebar-nav {
    display: none;
  }
}
</style>
