<template>
  <div>
    <PageHead title="容器" subtitle="Docker 容器与编排（compose）一体化管理（对标 1Panel 容器页）">
      <el-tooltip content="刷新" placement="top">
        <el-button :icon="Refresh" circle text aria-label="刷新" :loading="refreshing" @click="refreshActive" />
      </el-tooltip>
    </PageHead>

    <DockerGate @retry="refreshActive">
      <el-tabs v-model="activeTab">
        <el-tab-pane label="容器" name="containers">
          <ContainerTab ref="containerRef" />
        </el-tab-pane>
        <el-tab-pane label="编排" name="compose" lazy>
          <ComposeTab ref="composeRef" />
        </el-tab-pane>
      </el-tabs>
    </DockerGate>
  </div>
</template>

<script setup>
// 容器页（Docker 管理拆分批次 2026-10）：容器 + 编排（compose）一页，
// 原 DockerPage 壳的页头/503 门控/刷新职责收编于此（镜像/网络/卷已分散到各自语义页）。
// 两 tab 均常驻（compose lazy 首次激活才挂载），刷新只作用于当前激活 tab。
import { computed, ref } from 'vue'
import { Refresh } from '@element-plus/icons-vue'
import PageHead from '../components/PageHead.vue'
import DockerGate from '../components/DockerGate.vue'
import ContainerTab from './docker/components/ContainerTab.vue'
import ComposeTab from './docker/components/ComposeTab.vue'

const activeTab = ref('containers')
const containerRef = ref(null)
const composeRef = ref(null)
const refreshing = ref(false)

const activeRef = computed(() => (activeTab.value === 'compose' ? composeRef.value : containerRef.value))

async function refreshActive() {
  if (refreshing.value) return
  refreshing.value = true
  try {
    await activeRef.value?.refresh?.()
  } finally {
    refreshing.value = false
  }
}
</script>
