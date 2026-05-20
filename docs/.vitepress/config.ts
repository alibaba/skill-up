import { defineConfig } from 'vitepress'

// GitHub Pages base path: https://<org>.github.io/skill-up/
const base = '/skill-up/'

export default defineConfig({
  base,
  title: 'skill-up',
  description: 'CLI evaluation framework for Agent Skill developers.',
  lastUpdated: true,
  cleanUrls: true,
  ignoreDeadLinks: true,

  head: [
    ['link', { rel: 'icon', href: `${base}logo.png` }],
  ],

  themeConfig: {
    logo: '/logo.png',
    socialLinks: [
      { icon: 'github', link: 'https://github.com/alibaba/skill-up' },
    ],
    search: {
      provider: 'local',
    },
  },

  locales: {
    root: {
      label: 'English',
      lang: 'en-US',
      title: 'skill-up',
      description: 'CLI evaluation framework for Agent Skill developers.',
      themeConfig: {
        nav: [
          { text: 'Guide', link: '/guide/getting-started' },
          {
            text: 'Reference',
            items: [
              { text: 'CLI Reference', link: '/guide/cli-reference' },
              { text: 'User Configuration', link: '/guide/user-config' },
              { text: 'Writing Evals', link: '/guide/writing-evals' },
              { text: 'Migrating from Anthropic', link: '/guide/migration' },
            ],
          },
          { text: 'GitHub', link: 'https://github.com/alibaba/skill-up' },
        ],
        sidebar: {
          '/guide/': [
            {
              text: 'Introduction',
              items: [
                { text: 'Getting Started', link: '/guide/getting-started' },
              ],
            },
            {
              text: 'Authoring',
              items: [
                { text: 'Writing Evals', link: '/guide/writing-evals' },
              ],
            },
            {
              text: 'Reference',
              items: [
                { text: 'CLI Reference', link: '/guide/cli-reference' },
                { text: 'User Configuration', link: '/guide/user-config' },
                { text: 'Migrating from Anthropic', link: '/guide/migration' },
              ],
            },
          ],
        },
        editLink: {
          pattern: 'https://github.com/alibaba/skill-up/edit/main/docs/:path',
          text: 'Edit this page on GitHub',
        },
        footer: {
          message: 'Released under the Apache 2.0 License.',
          copyright: 'Copyright © Alibaba',
        },
      },
    },

    zh: {
      label: '简体中文',
      lang: 'zh-CN',
      link: '/zh/',
      title: 'skill-up',
      description: '面向 Agent Skill 开发者的 CLI 评测框架。',
      themeConfig: {
        nav: [
          { text: '指南', link: '/zh/guide/getting-started' },
          {
            text: '参考',
            items: [
              { text: 'CLI 命令参考', link: '/zh/guide/cli-reference' },
              { text: '用户配置', link: '/zh/guide/user-config' },
              { text: '编写评测配置与用例', link: '/zh/guide/writing-evals' },
              { text: '从 Anthropic 格式迁移', link: '/zh/guide/migration' },
            ],
          },
          { text: 'GitHub', link: 'https://github.com/alibaba/skill-up' },
        ],
        sidebar: {
          '/zh/guide/': [
            {
              text: '入门',
              items: [
                { text: '快速上手', link: '/zh/guide/getting-started' },
              ],
            },
            {
              text: '编写评测',
              items: [
                { text: '编写评测配置与用例', link: '/zh/guide/writing-evals' },
              ],
            },
            {
              text: '参考',
              items: [
                { text: 'CLI 命令参考', link: '/zh/guide/cli-reference' },
                { text: '用户配置', link: '/zh/guide/user-config' },
                { text: '从 Anthropic 格式迁移', link: '/zh/guide/migration' },
              ],
            },
          ],
        },
        editLink: {
          pattern: 'https://github.com/alibaba/skill-up/edit/main/docs/:path',
          text: '在 GitHub 上编辑此页',
        },
        footer: {
          message: '基于 Apache 2.0 协议发布。',
          copyright: 'Copyright © Alibaba',
        },
        outline: { label: '本页目录' },
        docFooter: { prev: '上一页', next: '下一页' },
        lastUpdatedText: '最后更新',
        darkModeSwitchLabel: '主题',
        sidebarMenuLabel: '菜单',
        returnToTopLabel: '回到顶部',
      },
    },
  },
})
