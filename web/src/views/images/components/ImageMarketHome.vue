<template>
  <div>
    <!-- 二级入口落地页（1Panel 式）：选择「云镜像」或「安装 ISO」后进入对应视图 -->
    <div v-if="!sub" class="pick-panel">
      <PageHead embedded title="镜像市场" subtitle="选择要下载的镜像类型；下载为分钟级后台任务，可离开页面，进度在任务中心跟踪" />
      <div class="cards">
        <div class="card" @click="sub = 'cloud'">
          <div class="card-icon"><el-icon><Files /></el-icon></div>
          <div class="card-title">云镜像</div>
          <div class="card-desc">qcow2 磁盘镜像，预装 cloud-init——创建虚拟机免安装流程，分钟级出机。Ubuntu / Debian / Rocky / Alma / Fedora / Arch。</div>
          <div class="card-badge ok">● {{ cloudCount }} 个可选</div>
        </div>
        <div class="card serial" @click="sub = 'iso'">
          <div class="card-icon"><el-icon><CircleCheck /></el-icon></div>
          <div class="card-title">安装 ISO</div>
          <div class="card-desc">传统光盘安装介质——走创建向导「本地安装介质 (ISO)」流程，适合演示完整装机过程。Ubuntu / Rocky / Fedora / Debian / openSUSE。</div>
          <div class="card-badge gold">● {{ isoCount }} 个可选</div>
        </div>
      </div>
    </div>

    <!-- 二级视图：顶部返回条 + 对应市场 -->
    <div v-else class="sub-view">
      <div class="sub-back">
        <el-button text :icon="ArrowLeft" @click="sub = ''">返回镜像市场</el-button>
        <el-divider direction="vertical" />
        <span class="sub-title">{{ sub === 'cloud' ? '云镜像' : '安装 ISO' }}</span>
      </div>
      <ImageMarket v-if="sub === 'cloud'" embedded />
      <ImageIsoMarket v-else embedded />
    </div>
  </div>
</template>

<script setup>
// 镜像市场落地页（统称）：云镜像 / 安装 ISO 二选一进入。
// sub = ''（入口）| 'cloud' | 'iso'；两视图各自持数据与下载状态，互不干扰。
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { ArrowLeft, CircleCheck, Files } from '@element-plus/icons-vue'
import PageHead from '../../../components/PageHead.vue'
import ImageMarket from './ImageMarket.vue'
import ImageIsoMarket from './ImageIsoMarket.vue'
import { api } from '../../../api'

const sub = ref('')
const cloudCount = ref(0)
const isoCount = ref(0)

async function loadCounts() {
  try {
    const [c, i] = await Promise.all([api.imageMarket(), api.imageMarketIso()])
    const dc = c && (c.data || c)
    const di = i && (i.data || i)
    cloudCount.value = dc.total || (dc.items || []).length
    isoCount.value = di.total || (di.items || []).length
  } catch {
    /* 计数失败不阻塞入口点击 */
  }
}

onMounted(loadCounts)
onUnmounted(() => {})
</script>

<style scoped>
/* 入口卡片：对齐控制台「选择连接方式」三卡模式（同款 .cards/.card 视觉语言） */
.pick-panel {
  padding: 8px 0;
}
.cards {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(320px, 1fr));
  gap: 16px;
  margin-top: 16px;
}
.card {
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  background: var(--color-card);
  padding: 24px;
  cursor: pointer;
  transition: all 0.2s ease;
}
.card:hover {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-md);
  transform: translateY(-2px);
}
.card.serial {
  border-color: color-mix(in srgb, var(--color-serial-gold) 50%, var(--color-border));
}
.card.serial:hover {
  box-shadow: 0 10px 24px rgba(240, 185, 11, 0.22);
}
.card-icon {
  font-size: 2rem;
  color: var(--color-primary);
  margin-bottom: 12px;
}
.card.serial .card-icon {
  color: var(--color-serial-gold);
}
.card-title {
  font-weight: 700;
  font-size: 1.05rem;
  margin-bottom: 6px;
}
.card-desc {
  color: var(--color-muted-foreground);
  font-size: 0.88rem;
  line-height: 1.6;
  min-height: 3.2em;
  margin-bottom: 12px;
}
.card-badge {
  display: inline-block;
  font-size: 0.78rem;
  font-weight: 600;
  padding: 3px 10px;
  border-radius: 999px;
}
.card-badge.ok {
  color: #166534;
  background: #dcfce7;
}
.card-badge.gold {
  color: #7c4a03;
  background: #fde68a;
}
/* 二级视图返回条 */
.sub-back {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 12px;
}
.sub-title {
  font-weight: 700;
  font-size: 1.05rem;
}
</style>
