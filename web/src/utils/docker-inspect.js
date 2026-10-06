// docker inspect 原始 JSON → 结构化视图模型（纯函数，无请求无状态）。
// ContainerInspectDrawer（R2）与 ContainerDetailDrawer（R3）共用同一解析，避免两处漂移。

// 兼容 docker inspect 的字段缺省与类型抖动：取不到一律给安全默认，绝不让渲染层炸。
function obj(v) {
  return v && typeof v === 'object' ? v : {}
}

// parseInspect 把 docker inspect 对象拆成展示区块。
export function parseInspect(raw) {
  const d = obj(raw)
  const config = obj(d.Config)
  const state = obj(d.State)
  const hostConfig = obj(d.HostConfig)
  const netSettings = obj(d.NetworkSettings)

  // 概要
  const summary = {
    id: d.Id || '',
    name: String(d.Name || '').replace(/^\//, ''),
    image: config.Image || '',
    imageId: d.Image || '',
    created: d.Created || '',
    state: state.Status || '',
    running: !!state.Running,
    paused: !!state.Paused,
    startedAt: state.StartedAt || '',
    finishedAt: state.FinishedAt || '',
    exitCode: state.ExitCode,
    restartCount: state.RestartCount,
    command: Array.isArray(config.Cmd) ? config.Cmd.join(' ') : (config.Cmd || ''),
    entrypoint: Array.isArray(config.Entrypoint) ? config.Entrypoint.join(' ') : (config.Entrypoint || ''),
    restartPolicy: obj(hostConfig.RestartPolicy).Name || 'no',
    networkMode: hostConfig.NetworkMode || '',
    ip: obj(netSettings.IPAddress).toString() || '',
    pidMode: hostConfig.PidMode || ''
  }

  // 环境变量：["KEY=VALUE", ...] → [{key, value}]（首个 = 分割，值可含 =）
  const env = (Array.isArray(config.Env) ? config.Env : []).map((s) => {
    const str = String(s)
    const i = str.indexOf('=')
    return i === -1 ? { key: str, value: '' } : { key: str.slice(0, i), value: str.slice(i + 1) }
  })

  // 端口映射：NetworkSettings.Ports = { "80/tcp": [{HostIp, HostPort}] | null }
  const ports = []
  const portMap = obj(netSettings.Ports)
  for (const [container, bindings] of Object.entries(portMap)) {
    if (Array.isArray(bindings) && bindings.length) {
      for (const b of bindings) {
        ports.push({ container, hostIp: obj(b).HostIp || '', hostPort: obj(b).HostPort || '' })
      }
    } else {
      ports.push({ container, hostIp: '', hostPort: '' }) // 未映射到宿主（仅容器内暴露）
    }
  }

  // 挂载：Mounts = [{Type, Source, Destination, Mode, RW, Name}]
  const mounts = (Array.isArray(d.Mounts) ? d.Mounts : []).map((m) => {
    const mm = obj(m)
    return {
      type: mm.Type || '',
      source: mm.Source || mm.Name || '',
      destination: mm.Destination || '',
      mode: mm.Mode || (mm.RW === false ? 'ro' : 'rw'),
      rw: mm.RW !== false
    }
  })

  // 网络：NetworkSettings.Networks = { name: {IPAddress, Gateway, MacAddress, ...} }
  const networks = Object.entries(obj(netSettings.Networks)).map(([name, n]) => {
    const nn = obj(n)
    return {
      name,
      ip: nn.IPAddress || '',
      gateway: nn.Gateway || '',
      mac: nn.MacAddress || '',
      aliases: Array.isArray(nn.Aliases) ? nn.Aliases.join(', ') : ''
    }
  })

  return { summary, env, ports, mounts, networks }
}

// 端口映射短显示：hostIp:hostPort -> container/tcp（无映射显示容器内端口）
export function portText(p) {
  if (!p) return ''
  if (!p.hostPort) return `${p.container}（未映射）`
  const host = p.hostIp ? `${p.hostIp}:${p.hostPort}` : p.hostPort
  return `${host} → ${p.container}`
}

// 复制用：只取 hostPort（最常用），无则空
export function portCopyText(p) {
  return p && p.hostPort ? `${p.hostIp || '0.0.0.0'}:${p.hostPort}` : ''
}
