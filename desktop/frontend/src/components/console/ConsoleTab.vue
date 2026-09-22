<script setup lang="ts">
import { onMounted, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Alert } from '@/components/ui/alert'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Globe } from 'lucide-vue-next'
import { useStatus } from '@/composables/useStatus'
import { useLanguage } from '@/composables/useLanguage'
import { useConsoleChannels } from '@/composables/useConsoleChannels'
import {
  channelSelectionPath,
  consoleSelectionChannelType,
  type ConsoleSelection,
} from '@/composables/useConsoleSelection'
import { OpenWebUIInBrowser } from '@bindings/github.com/BenedictKing/ccx/desktop/desktopservice'
import ChannelManager from '@/components/console/ChannelManager.vue'
import type { ManagedChannelType } from '@/utils/channel-type-api'

const props = withDefaults(defineProps<{
  selection?: ConsoleSelection
}>(), {
  selection: '/channels/messages',
})

const emit = defineEmits<{
  'update:selection': [selection: ConsoleSelection]
}>()

const { status } = useStatus()
const { t } = useLanguage()
const { activeTab, refreshError } = useConsoleChannels()

// 子 Tab 定义：对齐 web 端 IA——LLM 多协议合并为统一列表（logical 聚合）+ Images / Vectors
const protocolTabs: { value: ManagedChannelType; label: string }[] = [
  { value: 'messages', label: t('console.view.llm') },
  { value: 'images', label: t('console.view.images') },
  { value: 'vectors', label: t('console.view.vectors') },
]

const applySelection = (selection: ConsoleSelection) => {
  activeTab.value = consoleSelectionChannelType(selection)
}

const updateProtocolTab = (next: ManagedChannelType) => {
  activeTab.value = next
  emit('update:selection', channelSelectionPath(next))
}

const protocolTabClass = (tab: ManagedChannelType) => [
  'inline-flex h-full items-center justify-center px-2 py-0.5 text-xs font-medium transition-colors',
  activeTab.value === tab
    ? 'bg-primary text-primary-foreground'
    : 'text-muted-foreground hover:bg-accent/50 hover:text-foreground',
]


const openInBrowser = async () => {
  try {
    await OpenWebUIInBrowser()
  } catch (e) {
    console.warn('Failed to open WebUI in browser:', e)
  }
}

onMounted(() => {
  applySelection(props.selection)
})

watch(() => props.selection, (selection) => {
  applySelection(selection)
})

</script>

<template>
  <div class="h-full flex flex-col gap-4">
    <!-- 服务未运行提示 -->
    <Alert v-if="!status.running" variant="destructive" class="shrink-0">
      <p class="text-sm">
        {{ t('webui.notRunning') }}
      </p>
    </Alert>

    <!-- 加载状态 -->
    <div v-else-if="refreshError" class="shrink-0">
      <Alert variant="destructive">
        <p class="text-sm">{{ refreshError }}</p>
      </Alert>
    </div>

    <!-- 管理控制台主体 -->
    <div v-else class="flex-1 flex flex-col min-h-0">
      <!-- 顶部紧凑工具栏：协议子 Tab + 操作按钮 -->
      <div class="shrink-0 border-b border-border bg-card/50 px-2 py-1.5 flex items-center gap-2">
        <!-- 协议子 Tab -->
        <div
          role="tablist"
          :aria-label="t('nav.channels')"
          class="inline-flex h-7 items-center gap-0.5 border border-border bg-secondary/40 p-0.5"
        >
          <button
            v-for="tab in protocolTabs"
            :key="tab.value"
            type="button"
            role="tab"
            :aria-selected="activeTab === tab.value"
            :class="protocolTabClass(tab.value)"
            @click="updateProtocolTab(tab.value)"
          >
            {{ tab.label }}
          </button>
        </div>

        <!-- 弹性占位 -->
        <div class="flex-1" />

        <!-- 右侧操作按钮 -->
        <Button variant="outline" size="sm" class="h-7 text-xs" @click="openInBrowser">
          <Globe class="h-3 w-3" />
          {{ t('webui.openInBrowser') }}
        </Button>
      </div>

      <!-- 内容区域 -->
      <div class="flex-1 min-h-0">
        <!-- 频道管理面板 -->
        <div class="h-full">
          <ScrollArea class="h-full">
            <div class="p-3">
              <ChannelManager :type="activeTab" />
            </div>
          </ScrollArea>
        </div>
      </div>
    </div>
  </div>
</template>
