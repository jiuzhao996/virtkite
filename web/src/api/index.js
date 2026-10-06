// API 统一封装（目录化组装）：页面一律 `import { api } from '@/api'`，
// 禁止直接 import http 裸调——Docker/Crons/AppStore/回收站/镜像市场/AI/VM 文件
// 曾因此形成双轨制（裸调走 res.data.data 解包、api 走 res.data，路径混乱且丢统一拦截语义）。
// 各域拆分：http.js（axios 基建）/ core.js（既有核心域）/ docker.js（Docker 全域）/ ops.js（运维与扩展域）。
import { http } from './http'
import { core } from './core'
import { docker } from './docker'
import { ops } from './ops'

export const api = { ...core, ...docker, ...ops }

// TOKEN_KEY 实际定义在 store/auth.js，这里原样 re-export，保持既有的「从 ../api 导入 TOKEN_KEY」写法可用
import { TOKEN_KEY } from '../store/auth'
export { TOKEN_KEY }

export default http
