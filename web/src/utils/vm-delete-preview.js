// 删除预检文案（A 批次）：把后端 delete-preview 的结构化结果渲染成人话。
// 详情页（单台）与列表页（批量）共用，保证两处口径一致。

/** 容量格式化：GB → 人读（<1GB 显示 MB） */
export function gbText(gb) {
  const n = Number(gb) || 0
  if (n <= 0) return '未知大小'
  if (n < 1) return (n * 1024).toFixed(0) + ' MB'
  return n >= 100 ? n.toFixed(0) + ' GB' : n.toFixed(1) + ' GB'
}

const esc = (s) =>
  String(s ?? '').replace(/[&<>"]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]))

/**
 * 单台删除预检 → HTML 片段（不含标题）。
 * pv 为 null/拉取失败时返回空串，调用方退回旧文案（预检失败不阻断删除）。
 */
export function deletePreviewHtml(pv) {
  if (!pv) return ''
  const dels = (pv.volumes || []).filter((v) => !v.keep)
  const keeps = (pv.volumes || []).filter((v) => v.keep)
  const snaps = pv.snapshots || []
  const rows = []

  if (dels.length) {
    const detail = dels.map((v) => `${esc(v.volume)}（${gbText(v.size_gb)}）`).join('、')
    rows.push(`<li><b>物理删除 ${dels.length} 块磁盘</b>，共 ${gbText(pv.delete_gb)}：<span class="dp-mono">${detail}</span></li>`)
  } else {
    rows.push('<li>没有将被删除的磁盘（域名下未探到磁盘卷）</li>')
  }
  if (snaps.length) {
    const show = snaps.slice(0, 5).map(esc).join('、')
    const more = snaps.length > 5 ? ` 等 ${snaps.length} 个` : ''
    rows.push(`<li><b>${snaps.length} 个快照将一并丢弃</b>：<span class="dp-mono">${show}${more}</span></li>`)
  } else {
    rows.push('<li>无快照</li>')
  }
  if (keeps.length) {
    const detail = keeps.map((v) => `${esc(v.volume)}（${esc(v.reason)}）`).join('；')
    rows.push(`<li class="dp-keep">保留 ${keeps.length} 个卷（${gbText(pv.keep_gb)}）：<span class="dp-mono">${detail}</span></li>`)
  }
  if (pv.run_state === 'running') {
    rows.push('<li class="dp-warn">虚拟机<b>正在运行</b>，删除时会先被强制关机（未保存的数据会丢失）</li>')
  }
  return `<ul class="dp-list">${rows.join('')}</ul>`
}

/** 批量删除预检聚合 → HTML 片段。items: [{name, pv}] */
export function deletePreviewBulkHtml(items) {
  const ok = items.filter((x) => x && x.pv)
  if (!ok.length) return ''
  let disks = 0, snaps = 0, gb = 0, keeps = 0
  const perVM = []
  for (const { name, pv } of ok) {
    const d = (pv.volumes || []).filter((v) => !v.keep).length
    const k = (pv.volumes || []).filter((v) => v.keep).length
    disks += d
    keeps += k
    snaps += (pv.snapshots || []).length
    gb += Number(pv.delete_gb) || 0
    perVM.push(`${esc(name)}（磁盘 ${d} · 快照 ${(pv.snapshots || []).length}）`)
  }
  const rows = [
    `<li><b>物理删除 ${disks} 块磁盘</b>，共 ${gbText(gb)}</li>`,
    `<li><b>${snaps} 个快照将一并丢弃</b></li>`,
    keeps ? `<li class="dp-keep">${keeps} 个卷因保护（共享基镜像/克隆父盘/池外）保留</li>` : '',
    `<li class="dp-mono">${perVM.join('；')}</li>`
  ].filter(Boolean)
  return `<ul class="dp-list">${rows.join('')}</ul>`
}
