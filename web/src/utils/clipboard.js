// 剪贴板纯函数：navigator.clipboard 仅在安全上下文（localhost/HTTPS）可用，
// 失败降级 execCommand（隐藏 textarea 选中复制），全程不抛错只返回布尔。
export async function copyText(text) {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    try {
      const ta = document.createElement('textarea')
      ta.value = text
      ta.style.position = 'fixed'
      ta.style.opacity = '0'
      document.body.appendChild(ta)
      ta.select()
      const ok = document.execCommand('copy')
      ta.remove()
      return ok
    } catch {
      return false
    }
  }
}
