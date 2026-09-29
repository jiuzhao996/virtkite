import { defineConfig } from 'vitepress'

export default defineConfig({
  lang: 'zh-CN',
  title: '鸢航 VirtKite',
  description: '基于 KVM 的轻量级私有云管理平台——从 0 到 1 官方文档',
  base: '/virtkite-docs/',
  themeConfig: {
    siteTitle: '🪁 鸢航 VirtKite 文档',
    nav: [
      { text: '快速开始', link: '/quick-start' },
      { text: '核心概念', link: '/concepts' },
      { text: '使用手册', link: '/manual' },
      { text: '4A 教学闭环', link: '/4a' },
      { text: '运维手册', link: '/ops' },
      { text: '构建之旅', link: '/journey' }
    ],
    sidebar: [
      {
        text: '起步',
        items: [
          { text: '这是什么？', link: '/' },
          { text: '快速开始（10 分钟）', link: '/quick-start' },
          { text: '五分钟理解核心概念', link: '/concepts' }
        ]
      },
      {
        text: '使用',
        items: [
          { text: '使用手册', link: '/manual' },
          { text: '4A 教学闭环', link: '/4a' }
        ]
      },
      {
        text: '进阶',
        items: [
          { text: '运维手册（备份/升级/排障）', link: '/ops' },
          { text: '从 0 到 1 构建之旅', link: '/journey' }
        ]
      }
    ],
    outline: { level: [2, 3], label: '本页目录' },
    docFooter: { prev: '上一页', next: '下一页' },
    lastUpdated: { text: '最后更新' },
    search: { provider: 'local', options: { translations: { search: { placeholder: '搜索文档' } } } }
  }
})
