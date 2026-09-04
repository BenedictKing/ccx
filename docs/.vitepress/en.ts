import type { DefaultTheme, LocaleSpecificConfig } from 'vitepress'

export const enConfig: LocaleSpecificConfig<DefaultTheme.Config> = {
  title: 'CCX Docs',
  description: 'AI API Proxy & Protocol Translation Gateway',
  themeConfig: {
    nav: [
      { text: 'Guide', link: '/en/guide/getting-started' },
      { text: 'Client Setup', link: '/en/guide/clients/' },
      { text: 'Provider Setup', link: '/en/providers/' },
    ],
    sidebar: {
      '/en/guide/clients/': [
        {
          text: 'Client Setup',
          items: [
            { text: 'Overview', link: '/en/guide/clients/' },
            { text: 'Claude Code', link: '/en/guide/clients/claude-code' },
            { text: 'Claude Desktop', link: '/en/guide/clients/claude-desktop' },
            { text: 'Codex CLI / Codex App', link: '/en/guide/clients/codex' },
            { text: 'OpenCode', link: '/en/guide/clients/opencode' },
            { text: 'CCX Desktop', link: '/en/guide/desktop/' },
          ],
        },
      ],
      '/en/guide/': [
        {
          text: 'Getting Started',
          items: [
            { text: 'Quick Start', link: '/en/guide/getting-started' },
            { text: 'CCX Desktop', link: '/en/guide/desktop/' },
            { text: 'Desktop Troubleshooting', link: '/en/guide/desktop/troubleshooting' },
            { text: 'Deployment', link: '/en/guide/deployment' },
            { text: 'Environment Variables', link: '/en/guide/environment' },
          ],
        },
        {
          text: 'Advanced',
          items: [
            { text: 'Architecture', link: '/en/guide/architecture' },
            { text: 'Benchmark Dashboard', link: '/en/guide/benchmark' },
            { text: 'Development', link: '/en/guide/development' },
            { text: 'Release', link: '/en/guide/release' },
            { text: 'Contributing', link: '/en/guide/contributing' },
            { text: 'Privacy Policy', link: '/en/guide/privacy' },
          ],
        },
      ],
      '/en/providers/': [
        {
          text: 'Provider Setup',
          items: [
            { text: 'Overview', link: '/en/providers/' },
            { text: 'DeepSeek', link: '/en/providers/deepseek' },
            { text: 'GLM (Zhipu AI)', link: '/en/providers/glm' },
            { text: 'MiniMax', link: '/en/providers/minimax' },
            { text: 'Kimi (Moonshot)', link: '/en/providers/kimi' },
            { text: 'OpenAI GPT', link: '/en/providers/openai' },
            { text: 'Xiaomi MiMo', link: '/en/providers/mimo' },
            { text: 'Claude', link: '/en/providers/claude' },
            { text: 'Gemini', link: '/en/providers/gemini' },
          ],
        },
      ],
    },
  },
}
