// Docker 域：容器/镜像/卷/网络/compose（docker/components 下七个组件共用）。
// 此前该域 20+ 端点全部绕过 api 裸调 http（2026-10 API 收口批次统一迁入）。
import { http, unwrap } from './http'

export const docker = {
  // 容器
  listContainers: () => unwrap(http.get('/docker/containers')),
  dockerStats: () => unwrap(http.get('/docker/stats')),
  // 单容器动作：start/stop/restart/pause/unpause
  dockerContainerAction: (id, action) => unwrap(http.post(`/docker/containers/${id}/${action}`)),
  // 重命名（复用 :action 路由，请求体 {"name":"新名称"}）
  dockerContainerRename: (id, name) => unwrap(http.post(`/docker/containers/${id}/rename`, { name })),
  // 单容器实时 stats（--no-stream，含 CPU%/内存/网络/块 IO/PIDs）
  dockerContainerStats: (id) => unwrap(http.get(`/docker/containers/${id}/stats`)),
  // 删除容器：force=true 时运行中容器强制删除（后端按 State 决定 force 参数）
  dockerContainerDelete: (id, force) => unwrap(http.delete('/docker/containers/' + id, { params: force ? { force: 'true' } : {} })),
  dockerContainerInspect: (id) => unwrap(http.get(`/docker/containers/${id}/inspect`)),
  dockerContainerLogs: (id, tail) => unwrap(http.get(`/docker/containers/${id}/logs`, { params: { tail } })),
  // 创建并启动容器（name/image 必填；ports/volumes/envs 为字符串数组；restart ∈ no/always/unless-stopped/on-failure；command 可空）
  createContainer: (payload) => unwrap(http.post('/docker/containers', payload)),

  // 镜像
  dockerImages: () => unwrap(http.get('/docker/images')),
  // 拉镜像可能数分钟：timeout 0 不限时（与镜像上传同一理由，15s 全局超时必错）
  dockerPullImage: (name) => unwrap(http.post('/docker/images/pull', { name }, { timeout: 0 })),
  // type ∈ containers/images/volumes/networks（后端 docker system prune 语义）
  dockerPrune: (type) => unwrap(http.post('/docker/prune', { type })),
  dockerDeleteImage: (imageId) => unwrap(http.delete('/docker/images/' + encodeURIComponent(imageId))),

  // 卷
  dockerVolumes: () => unwrap(http.get('/docker/volumes')),
  dockerCreateVolume: (payload) => unwrap(http.post('/docker/volumes', payload)),
  dockerDeleteVolume: (name) => unwrap(http.delete('/docker/volumes/' + encodeURIComponent(name))),
  dockerPruneVolumes: () => unwrap(http.post('/docker/volumes/prune')),

  // 网络
  dockerNetworks: () => unwrap(http.get('/docker/networks')),
  dockerCreateNetwork: (payload) => unwrap(http.post('/docker/networks', payload)),
  dockerDeleteNetwork: (name) => unwrap(http.delete('/docker/networks/' + encodeURIComponent(name))),

  // compose 项目：action ∈ up/start/stop/restart/down（down 含重建，耗时可达分钟级，单独放宽超时）
  dockerComposeList: () => unwrap(http.get('/docker/compose')),
  dockerComposeAction: (project, action) => unwrap(http.post('/docker/compose/' + encodeURIComponent(project) + '/' + action, null, { timeout: 150000 }))
}
