import { defineConfig, type HeadConfig } from 'vitepress'

const SITE = 'https://mcphubcli.dev'

// VitePress configuration for the mcphub website + docs (deployed at mcphubcli.dev).
export default defineConfig({
  title: 'mcphub',
  titleTemplate: ':title · mcphub',
  description:
    'One hub for all your MCP servers. Define them once, run a single gateway that proxies them all, and sync the config into 12 agent harnesses.',
  lang: 'en-US',
  cleanUrls: true,
  lastUpdated: true,

  sitemap: { hostname: SITE },

  head: [
    ['link', { rel: 'icon', type: 'image/svg+xml', href: '/favicon.svg' }],
    ['link', { rel: 'apple-touch-icon', href: '/apple-touch-icon.png' }],
    ['meta', { name: 'theme-color', content: '#0b0f14' }],
    ['meta', { property: 'og:type', content: 'website' }],
    ['meta', { property: 'og:site_name', content: 'mcphub' }],
    ['meta', { property: 'og:image', content: `${SITE}/og.png` }],
    ['meta', { property: 'og:image:width', content: '1200' }],
    ['meta', { property: 'og:image:height', content: '630' }],
    ['meta', { name: 'twitter:card', content: 'summary_large_image' }],
    ['meta', { name: 'twitter:image', content: `${SITE}/og.png` }],
    ['link', { rel: 'preconnect', href: 'https://fonts.googleapis.com' }],
    ['link', { rel: 'preconnect', href: 'https://fonts.gstatic.com', crossorigin: '' }],
    [
      'link',
      {
        rel: 'stylesheet',
        href: 'https://fonts.googleapis.com/css2?family=Space+Grotesk:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500;600;700&display=swap',
      },
    ],
    // Vercel Web Analytics (enabled on the Vercel project; script is a no-op locally).
    ['script', {}, 'window.va = window.va || function () { (window.vaq = window.vaq || []).push(arguments); };'],
    ['script', { defer: '', src: '/_vercel/insights/script.js' }],
    [
      'script',
      { type: 'application/ld+json' },
      JSON.stringify({
        '@context': 'https://schema.org',
        '@type': 'SoftwareApplication',
        name: 'mcphub',
        operatingSystem: 'macOS, Linux',
        applicationCategory: 'DeveloperApplication',
        offers: { '@type': 'Offer', price: '0' },
        url: SITE,
        sameAs: ['https://github.com/abdul-hamid-achik/mcphub'],
        description:
          'Gateway and control plane for MCP servers: one config, one stdio gateway, synced into 12 agent harnesses.',
      }),
    ],
  ],

  transformPageData(pageData) {
    const path = pageData.relativePath.replace(/(^|\/)index\.md$/, '$1').replace(/\.md$/, '')
    const canonical = `${SITE}/${path}`
    const head: HeadConfig[] = (pageData.frontmatter.head ??= [])
    head.push(
      ['link', { rel: 'canonical', href: canonical }],
      ['meta', { property: 'og:url', content: canonical }],
      ['meta', { property: 'og:title', content: pageData.title ? `${pageData.title} · mcphub` : 'mcphub' }],
    )
    if (pageData.description) {
      head.push(['meta', { property: 'og:description', content: pageData.description }])
    }
  },

  themeConfig: {
    logo: { light: '/logo.svg', dark: '/logo-dark.svg', alt: 'mcphub' },

    nav: [
      { text: 'Guide', link: '/guide/getting-started', activeMatch: '^/guide/' },
      { text: 'Reference', link: '/reference/cli', activeMatch: '^/reference/' },
      {
        text: 'Concepts',
        items: [
          { text: 'Gateway & namespacing', link: '/guide/concepts' },
          { text: 'Lazy mode & pinning', link: '/guide/lazy-mode' },
          { text: 'Contextual routing', link: '/guide/contextual-routing' },
          { text: 'Sync', link: '/guide/sync' },
          { text: 'Intelligence', link: '/guide/intelligence' },
        ],
      },
    ],

    sidebar: [
      {
        text: 'Guide',
        items: [
          { text: 'Getting started', link: '/guide/getting-started' },
          { text: 'Concepts', link: '/guide/concepts' },
          { text: 'Sync to your agents', link: '/guide/sync' },
          { text: 'Lazy mode & pinning', link: '/guide/lazy-mode' },
          { text: 'Contextual routing', link: '/guide/contextual-routing' },
          { text: 'Oversized results', link: '/guide/results' },
          { text: 'Intelligence', link: '/guide/intelligence' },
          { text: 'Secrets via tvault', link: '/guide/secrets' },
          { text: 'Per-agent routing', link: '/guide/routing' },
          { text: 'Studio (TUI)', link: '/guide/studio' },
          { text: 'Agent harnesses', link: '/guide/harnesses' },
          { text: 'Connect Bob', link: '/guide/bob' },
          { text: 'Fetch with Hitspec', link: '/guide/hitspec' },
          { text: 'Troubleshooting', link: '/guide/troubleshooting' },
        ],
      },
      {
        text: 'Reference',
        items: [
          { text: 'CLI', link: '/reference/cli' },
          { text: 'Configuration', link: '/reference/config' },
          { text: 'Gateway meta-tools', link: '/reference/meta-tools' },
        ],
      },
    ],

    socialLinks: [
      { icon: 'github', link: 'https://github.com/abdul-hamid-achik/mcphub' },
    ],

    search: {
      provider: 'local',
    },

    editLink: {
      pattern:
        'https://github.com/abdul-hamid-achik/mcphub/edit/main/docs/:path',
      text: 'Edit this page on GitHub',
    },

    footer: {
      message: 'Released under the MIT License.',
      copyright: 'Copyright © 2026 Abdul Hamid Achik',
    },
  },
})
