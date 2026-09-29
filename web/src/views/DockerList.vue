<script>
// Docker 管理页实体已拆分至 ./docker/（薄壳 index.vue + components/*）。
// 路由（router/index.js）仍以 import('../views/DockerList.vue') 指向本文件，且路由不在本次拆分的
// 允许改动范围内，故这里只做转发，不承载任何逻辑。
import DockerPageIndex from './docker/index.vue'

export default DockerPageIndex
</script>
