import { defineConfig } from 'vitepress'

// VitePress 1.x configuration for the mcphub documentation site.
export default defineConfig({
  title: 'mcphub',
  description: 'One hub for all your MCP servers, synced into every agent.',
  lang: 'en-US',
  cleanUrls: true,
  lastUpdated: true,

  themeConfig: {
    nav: [
      { text: 'Guide', link: '/guide/getting-started' },
      { text: 'Reference', link: '/reference/cli' },
      {
        text: 'Concepts',
        items: [
          { text: 'Gateway & namespacing', link: '/guide/concepts' },
          { text: 'Sync', link: '/guide/sync' },
          { text: 'Intelligence', link: '/guide/intelligence' },
        ],
      },
    ],

    sidebar: {
      '/guide/': [
        {
          text: 'Guide',
          items: [
            { text: 'Getting started', link: '/guide/getting-started' },
            { text: 'Concepts', link: '/guide/concepts' },
            { text: 'Sync to your agents', link: '/guide/sync' },
            { text: 'Studio (TUI)', link: '/guide/studio' },
            { text: 'Intelligence', link: '/guide/intelligence' },
          ],
        },
        {
          text: 'Reference',
          items: [
            { text: 'CLI', link: '/reference/cli' },
            { text: 'Configuration', link: '/reference/config' },
          ],
        },
      ],
      '/reference/': [
        {
          text: 'Reference',
          items: [
            { text: 'CLI', link: '/reference/cli' },
            { text: 'Configuration', link: '/reference/config' },
          ],
        },
        {
          text: 'Guide',
          items: [
            { text: 'Getting started', link: '/guide/getting-started' },
            { text: 'Concepts', link: '/guide/concepts' },
            { text: 'Sync to your agents', link: '/guide/sync' },
            { text: 'Studio (TUI)', link: '/guide/studio' },
            { text: 'Intelligence', link: '/guide/intelligence' },
          ],
        },
      ],
    },

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
