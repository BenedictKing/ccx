import DefaultTheme from 'vitepress/theme'
import { defineComponent, h } from 'vue'
import { useData } from 'vitepress'
import RecommendedDownload from './components/RecommendedDownload.vue'
import BenchmarkUpdatedAt from './components/BenchmarkUpdatedAt.vue'
import './custom.css'

import type { Theme } from 'vitepress'

export default {
  extends: DefaultTheme,
  Layout: defineComponent({
    setup() {
      const { lang } = useData()

      return () => h(DefaultTheme.Layout, null, {
        'home-features-before': () => h(RecommendedDownload, {
          locale: lang.value.startsWith('en') ? 'en' : 'zh',
        }),
      })
    },
  }),
  enhanceApp({ app }) {
    app.component('BenchmarkUpdatedAt', BenchmarkUpdatedAt)
  },
} satisfies Theme
