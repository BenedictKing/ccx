<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import { CircleHelp, ArrowLeft, ArrowRight, Check, Plus, Pencil, Activity, History, RefreshCw, GripVertical, KeyRound } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { useLanguage } from '@/composables/useLanguage'
import { cn } from '@/lib/utils'

const open = defineModel<boolean>('open', { required: true })

const { t } = useLanguage()

const STEP_COUNT = 4
const step = ref(0)

// 每次打开指引时回到第一步
watch(open, (isOpen) => {
  if (isOpen) step.value = 0
})

const demoProtocolTabs = ['Claude', 'OpenAI Chat', 'Images', 'Codex', 'Gemini', 'Cockpit']

// 渠道列表示意行：一个正常、一个暂停
const demoRows = computed<Array<{ priority: number; paused: boolean; name: string; keys: number }>>(() => [
  { priority: 1, paused: false, name: t('guide.channelList.demoNormalName'), keys: 3 },
  { priority: 2, paused: true, name: t('guide.channelList.demoPausedName'), keys: 2 },
])

const demoClickRows = computed<Array<{ icon: typeof Pencil; iconClass: string; text: string }>>(() => [
  { icon: Pencil, iconClass: 'text-primary', text: t('guide.channelList.clickName') },
  { icon: Activity, iconClass: 'text-primary', text: t('guide.channelList.clickRow') },
  { icon: History, iconClass: 'text-primary', text: t('guide.channelList.clickLogs') },
  { icon: RefreshCw, iconClass: 'text-amber-500', text: t('guide.channelList.clickResume') },
  { icon: GripVertical, iconClass: 'text-muted-foreground', text: t('guide.channelList.drag') },
])

function prev() {
  if (step.value > 0) step.value -= 1
}

function next() {
  if (step.value < STEP_COUNT - 1) step.value += 1
}
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent class="max-h-[90vh] overflow-y-auto sm:max-w-[720px]">
      <DialogHeader>
        <DialogTitle class="flex items-center gap-2">
          <CircleHelp class="size-5 text-primary" />
          <span>{{ t('guide.title') }}</span>
          <span class="ml-auto pr-6 text-xs font-normal text-muted-foreground">
            {{ t('guide.stepIndicator', { current: step + 1, total: STEP_COUNT }) }}
          </span>
        </DialogTitle>
      </DialogHeader>

      <!-- 步骤进度点 -->
      <div class="flex items-center justify-center gap-2">
        <button
          v-for="i in STEP_COUNT"
          :key="i"
          type="button"
          :aria-label="`${i}`"
          :class="cn(
            'h-2 cursor-pointer rounded-full transition-all duration-200',
            i - 1 === step ? 'w-5 bg-primary' : 'w-2 bg-muted-foreground/25 hover:bg-muted-foreground/45',
          )"
          @click="step = i - 1"
        />
      </div>

      <div class="min-h-[280px]">
        <!-- 1. 欢迎总览 -->
        <section v-show="step === 0">
          <h3 class="mb-3 font-bold">{{ t('guide.welcome.title') }}</h3>
          <p class="mb-2 text-sm leading-relaxed text-muted-foreground">{{ t('guide.welcome.body') }}</p>
          <ol class="list-decimal space-y-1 pl-5 text-sm leading-loose">
            <li>{{ t('guide.welcome.step1') }}</li>
            <li>{{ t('guide.welcome.step2') }}</li>
            <li>{{ t('guide.welcome.step3') }}</li>
          </ol>
        </section>

        <!-- 2. 协议切换 -->
        <section v-show="step === 1">
          <h3 class="mb-3 font-bold">{{ t('guide.protocol.title') }}</h3>
          <div class="mb-3 flex flex-wrap items-center gap-1.5 rounded-lg border border-border bg-secondary/40 px-3.5 py-2.5 font-semibold">
            <template v-for="(tab, idx) in demoProtocolTabs" :key="tab">
              <span :class="idx === 0 ? 'text-primary' : 'text-muted-foreground'">{{ tab }}</span>
              <span v-if="idx < demoProtocolTabs.length - 1" class="text-muted-foreground/40">/</span>
            </template>
          </div>
          <p class="mb-1.5 text-sm leading-relaxed text-muted-foreground">{{ t('guide.protocol.body1') }}</p>
          <p class="mb-1.5 text-sm leading-relaxed text-muted-foreground">{{ t('guide.protocol.body2') }}</p>
          <p class="text-sm leading-relaxed text-muted-foreground">{{ t('guide.protocol.body3') }}</p>
        </section>

        <!-- 3. 添加渠道 -->
        <section v-show="step === 2">
          <h3 class="mb-3 font-bold">{{ t('guide.addChannel.title') }}</h3>
          <div class="mb-3 flex justify-center rounded-lg border border-border bg-secondary/40 p-4">
            <Button size="sm" class="pointer-events-none opacity-90" tabindex="-1">
              <Plus class="size-3.5" />
              {{ t('app.actions.addChannel') }}
            </Button>
          </div>
          <p class="mb-1.5 text-sm leading-relaxed text-muted-foreground">{{ t('guide.addChannel.body1') }}</p>
          <p class="text-sm leading-relaxed text-muted-foreground">{{ t('guide.addChannel.body2') }}</p>
        </section>

        <!-- 4. 看懂渠道列表（自绘两渠道示意） -->
        <section v-show="step === 3">
          <h3 class="mb-3 font-bold">{{ t('guide.channelList.title') }}</h3>
          <p class="mb-3 text-sm leading-relaxed text-muted-foreground">{{ t('guide.channelList.intro') }}</p>

          <!-- 示意渠道行 -->
          <div class="mb-4 flex flex-col gap-2">
            <div
              v-for="row in demoRows"
              :key="row.priority"
              :class="row.paused ? 'border-amber-500/40 bg-amber-500/5' : 'border-border'"
              class="grid grid-cols-[20px_22px_10px_1fr_auto_auto] items-center gap-2.5 rounded-lg border px-3 py-2.5"
            >
              <GripVertical class="size-4 text-muted-foreground/60" />
              <span class="inline-flex h-[22px] items-center justify-center rounded bg-secondary text-xs font-bold">{{ row.priority }}</span>
              <span :class="row.paused ? 'bg-amber-500' : 'bg-emerald-500'" class="size-2 rounded-full" />
              <span class="truncate text-sm font-semibold">{{ row.name }}</span>
              <span class="whitespace-nowrap text-xs text-muted-foreground">15m · 99%</span>
              <span class="inline-flex items-center gap-1 rounded-full border border-border px-2 py-0.5 text-xs text-muted-foreground">
                <KeyRound class="size-3" />
                {{ row.keys }}
              </span>
            </div>
          </div>

          <ul class="m-0 list-none p-0">
            <li
              v-for="row in demoClickRows"
              :key="row.text"
              class="flex items-center gap-2.5 border-b border-dashed border-border/60 py-2 text-sm leading-relaxed last:border-b-0"
            >
              <component :is="row.icon" :class="['size-4 shrink-0', row.iconClass]" />
              <span>{{ row.text }}</span>
            </li>
          </ul>
        </section>
      </div>

      <DialogFooter class="sm:justify-between">
        <Button v-if="step > 0" variant="outline" size="sm" @click="prev">
          <ArrowLeft class="size-3.5" />
          {{ t('guide.prev') }}
        </Button>
        <span v-else />
        <Button v-if="step < STEP_COUNT - 1" size="sm" @click="next">
          {{ t('guide.next') }}
          <ArrowRight class="size-3.5" />
        </Button>
        <Button v-else size="sm" @click="open = false">
          <Check class="size-3.5" />
          {{ t('guide.gotIt') }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
