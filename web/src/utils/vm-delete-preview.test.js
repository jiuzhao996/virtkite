// vm-delete-preview.js 单测：删除预检文案的容量口径、保留原因与 HTML 转义。
// 这段 HTML 会被 v-html 渲染，卷名/快照名来自 libvirt，转义必须锁死。
import { describe, it, expect } from 'vitest'
import { deletePreviewHtml, deletePreviewBulkHtml } from './vm-delete-preview'

describe('deletePreviewHtml（单台）', () => {
  it('预检缺失时返回空串，调用方退回旧文案', () => {
    expect(deletePreviewHtml(null)).toBe('')
  })

  it('列出待删磁盘、快照与保留卷', () => {
    const html = deletePreviewHtml({
      volumes: [
        { volume: 'web-root', size_gb: 20, keep: false },
        { volume: 'base.qcow2', size_gb: 10, keep: true, reason: '共享基镜像' },
      ],
      snapshots: ['snap-a', 'snap-b'],
      delete_gb: 20,
      keep_gb: 10,
      run_state: 'running',
    })
    expect(html).toContain('物理删除 1 块磁盘')
    expect(html).toContain('20.0 GB')
    expect(html).toContain('2 个快照将一并丢弃')
    expect(html).toContain('保留 1 个卷')
    expect(html).toContain('共享基镜像')
    expect(html).toContain('正在运行')
  })

  it('卷名与保留原因做 HTML 转义（防 v-html 注入）', () => {
    const html = deletePreviewHtml({
      volumes: [{ volume: '<img src=x onerror=alert(1)>', size_gb: 1, keep: true, reason: '"><script>' }],
      snapshots: ['<b>x</b>'],
      delete_gb: 0,
      keep_gb: 1,
    })
    expect(html).not.toContain('<img')
    expect(html).toContain('&lt;img')
    expect(html).not.toContain('<script>')
    expect(html).not.toContain('<b>x</b>')
  })

  it('小于 1GB 显示 MB，0 显示未知大小', () => {
    const html = deletePreviewHtml({
      volumes: [{ volume: 'tiny', size_gb: 0.5, keep: false }],
      snapshots: [],
      delete_gb: 0.5,
    })
    expect(html).toContain('512 MB')
    // 卷大小缺失（0）时明细里写「未知大小」，而不是 0 GB 这种误导性数字
    const zero = deletePreviewHtml({
      volumes: [{ volume: 'ghost', size_gb: 0, keep: false }],
      snapshots: [],
      delete_gb: 0,
    })
    expect(zero).toContain('未知大小')
  })
})

describe('deletePreviewBulkHtml（批量）', () => {
  it('无有效预检时返回空串', () => {
    expect(deletePreviewBulkHtml([])).toBe('')
    expect(deletePreviewBulkHtml([{ name: 'a', pv: null }])).toBe('')
  })

  it('聚合磁盘/快照/保留数量', () => {
    const html = deletePreviewBulkHtml([
      { name: 'vm1', pv: { volumes: [{ keep: false }, { keep: true }], snapshots: ['s1'], delete_gb: 10 } },
      { name: 'vm2', pv: { volumes: [{ keep: false }], snapshots: [], delete_gb: 5 } },
    ])
    expect(html).toContain('物理删除 2 块磁盘')
    expect(html).toContain('15.0 GB')
    expect(html).toContain('1 个快照将一并丢弃')
    expect(html).toContain('1 个卷因保护')
    expect(html).toContain('vm1')
    expect(html).toContain('vm2')
  })

  it('虚拟机名同样转义', () => {
    const html = deletePreviewBulkHtml([{ name: '<script>x</script>', pv: { volumes: [], snapshots: [] } }])
    expect(html).not.toContain('<script>')
  })
})
