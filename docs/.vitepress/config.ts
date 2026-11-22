import { defineConfig, type HeadConfig } from 'vitepress'
import { zhConfig } from './zh'
import { enConfig } from './en'

const SITE_URL = 'https://benedictking.github.io/ccx'

const localeMap = [
  { lang: 'zh-CN', prefix: '/' },
  { lang: 'en', prefix: '/en/' },
]

export default defineConfig({
  title: 'CCX',
  description: 'AI API Proxy & Protocol Translation Gateway',
  base: '/ccx/',
  srcExclude: ['superpowers/**', 'design/**', 'reviews/**'],
  head: [
    ['link', { rel: 'icon', type: 'image/svg+xml', href: '/ccx/logo.svg' }],
    ['meta', { name: 'author', content: 'BenedictKing' }],
    ['meta', { property: 'og:type', content: 'website' }],
    ['meta', { property: 'og:site_name', content: 'CCX Docs' }],
  ],
  locales: {
    root: {
      label: '简体中文',
      lang: 'zh-CN',
      description: '多上游 AI API 代理与协议转换网关',
      head: [
        ['meta', { name: 'keywords', content: 'CCX, AI API 代理, Claude, OpenAI, Gemini, 协议转换网关, API 调度, 渠道编排' }],
        ['meta', { property: 'og:locale', content: 'zh_CN' }],
      ],
      ...zhConfig,
    },
    en: {
      label: 'English',
      lang: 'en',
      description: 'Multi-upstream AI API Proxy & Protocol Translation Gateway',
      head: [
        ['meta', { name: 'keywords', content: 'CCX, AI API proxy, Claude, OpenAI, Gemini, protocol translation, API gateway, channel orchestration' }],
        ['meta', { property: 'og:locale', content: 'en_US' }],
      ],
      ...enConfig,
    },
  },
  themeConfig: {
    socialLinks: [
      { icon: 'github', link: 'https://github.com/BenedictKing/ccx' },
    ],
    search: {
      provider: 'local',
    },
  },
  markdown: {
    languageAlias: {
      env: 'ini',
      dotenv: 'ini',
    },
  },

  // Canonical URL per page
  transformPageData(pageData) {
    const canonicalUrl = `${SITE_URL}/${pageData.relativePath}`
      .replace(/index\.md$/, '')
      .replace(/\.md$/, '.html')

    pageData.frontmatter.head ??= []
    pageData.frontmatter.head.push([
      'link',
      { rel: 'canonical', href: canonicalUrl },
    ])
  },

  // hreflang tags (build-time only)
  async transformHead(context): Promise<HeadConfig[]> {
    const { pageData } = context
    const head: HeadConfig[] = []

    // Strip locale prefix to get content path.
    // pageData.relativePath: en/guide/architecture.md or guide/architecture.md
    let contentPath = pageData.relativePath
    for (const loc of localeMap) {
      const localePrefix = loc.prefix === '/' ? '' : loc.prefix.slice(1)
      if (localePrefix && contentPath.startsWith(localePrefix)) {
        contentPath = contentPath.slice(localePrefix.length)
        break
      }
    }
    contentPath = contentPath
      .replace(/index\.md$/, '')
      .replace(/\.md$/, '')

    for (const loc of localeMap) {
      const href = `${SITE_URL}${loc.prefix}${contentPath}`
      head.push([
        'link',
        { rel: 'alternate', hreflang: loc.lang, href },
      ])
    }
    // x-default -> Chinese (root locale)
    head.push([
      'link',
      { rel: 'alternate', hreflang: 'x-default', href: `${SITE_URL}/${contentPath}` },
    ])

    // Open Graph locale alternates
    head.push(
      ['meta', { property: 'og:locale', content: pageData.relativePath.startsWith('en/') ? 'en_US' : 'zh_CN' }],
      ['meta', { property: 'og:locale:alternate', content: pageData.relativePath.startsWith('en/') ? 'zh_CN' : 'en_US' }],
    )

    return head
  },

  // Sitemap with hreflang alternates
  sitemap: {
    // VitePress sitemap keeps path prefixes only when hostname ends with the base slash.
    hostname: `${SITE_URL}/`,
    transformItems(items) {
      return items.map((item) => {
        // item.url: en/guide/architecture.html or guide/architecture.html
        let contentPath = item.url
        if (contentPath.startsWith('en/')) {
          contentPath = contentPath.slice('en/'.length)
        }

        const links = localeMap.map((loc) => {
          const prefix = loc.prefix === '/' ? '' : loc.prefix.slice(1)
          return {
            lang: loc.lang,
            url: `${SITE_URL}/${prefix}${contentPath}`,
          }
        })
        // x-default always points to the zh-CN (root) version
        links.push({ lang: 'x-default', url: `${SITE_URL}/${contentPath}` })

        return { ...item, links }
      })
    },
  },
})
