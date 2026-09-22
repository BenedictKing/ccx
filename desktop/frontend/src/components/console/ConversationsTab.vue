<script setup lang="ts">
// 会话雷达 tab：挂载已实现的 ConversationDashboard 三件套。
// 3s 数据轮询由 App.vue 经 isConsoleConversationsActive 接管，本组件零轮询逻辑。
import ConversationDashboard from './ConversationDashboard.vue'
</script>

<template>
  <div class="mx-auto flex w-full max-w-[1680px] flex-col gap-4">
    <ConversationDashboard />
  </div>
</template>
