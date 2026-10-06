<template>
  <div>
    <Toolbar>
      <template #left>
        <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
        <span class="sk-hint">声明式部署栈——YAML 一键 compose up，源自学习笔记的实战配置；部署后可在「容器 → 编排」停止/下线</span>
      </template>
      <span class="count">共 {{ stacks.length }} 个栈 · 已部署 {{ deployedCount }} 个</span>
    </Toolbar>

    <el-row :gutter="16">
      <el-col v-for="st in stacks" :key="st.id" :xs="24" :sm="12" :md="8">
        <el-card shadow="hover" class="sk-card">
          <div class="sk-head">
            <span class="sk-name">{{ st.id }}</span>
            <el-tag size="small" effect="plain" type="info">{{ st.category || '未分类' }}</el-tag>
            <el-tag v-if="st.deployed" size="small" type="success" effect="light">已部署</el-tag>
          </div>
          <p class="sk-desc">{{ st.description }}</p>
          <div class="sk-services">
            <el-tag v-for="s in st.services" :key="s" size="small" effect="plain" class="sk-svc mono">{{ s }}</el-tag>
          </div>
          <div class="sk-actions">
            <el-button
              type="primary" size="small" :icon="VideoPlay"
              :loading="deploying === st.id" :disabled="!!st.deployed"
              @click="deploy(st)"
            >{{ st.deployed ? '已部署' : '一键部署' }}</el-button>
            <el-button v-if="st.docs && st.docs.length" text size="small" :icon="Reading" @click="openDocs(st)">参考笔记</el-button>
          </div>
        </el-card>
      </el-col>
    </el-row>
    <el-empty v-if="!stacks.length && !loading" description="栈目录为空（stacks/*.yml）" :image-size="80" />

    <!-- 参考笔记抽屉：markdown 渲染（sanitize 后 v-html） -->
    <el-drawer v-model="docsDrawer" :title="'参考笔记 — ' + docsStack" size="55%">
      <div v-loading="docsLoading" class="sk-docs">
        <div v-if="docsHtml" class="markdown-body" v-html="docsHtml" />
        <el-empty v-else-if="!docsLoading" description="笔记不可读（NOTES_DIR 未配置或路径不存在）" :image-size="60" />
      </div>
    </el-drawer>
  </div>
</template>

<script setup>
// 部署栈商店（P2A）：栈卡片（分类/服务徽标/部署状态/参考笔记）+ 一键部署 + 笔记抽屉。
// markdown 渲染复用 AiChat 同款 marked + DOMPurify（v-html 前必须 sanitize）。
import { computed, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh, VideoPlay, Reading } from '@element-plus/icons-vue'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import { api } from '../../api'
import { errMsg, isCancel } from '../../utils/format'
import Toolbar from '../../components/Toolbar.vue'

const stacks = ref([])
const loading = ref(false)
const deploying = ref('')
const deployedCount = computed(() => stacks.value.filter((s) => s.deployed).length)

async function load() {
  loading.value = true
  try {
    const res = await api.listStacks()
    stacks.value = (res.data && res.data.items) || []
  } catch (e) {
    ElMessage.error(errMsg(e, '获取栈清单失败'))
  } finally {
    loading.value = false
  }
}

async function deploy(st) {
  try {
    await ElMessageBox.confirm(
      `一键部署「${st.id}」？包含服务：${st.services.join('、')}。镜像拉取可能需要数分钟，部署期间请勿关闭页面。`,
      '部署确认',
      { type: 'info', confirmButtonText: '部署', cancelButtonText: '取消' }
    )
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '操作失败'))
    return
  }
  deploying.value = st.id
  try {
    await api.deployStack(st.id)
    ElMessage.success(`「${st.id}」部署完成`)
    await load()
  } catch (e) {
    ElMessage.error(errMsg(e, '部署失败'))
  } finally {
    deploying.value = ''
  }
}

const docsDrawer = ref(false)
const docsLoading = ref(false)
const docsStack = ref('')
const docsHtml = ref('')

async function openDocs(st) {
  docsStack.value = st.id
  docsHtml.value = ''
  docsDrawer.value = true
  docsLoading.value = true
  try {
    const md = await api.stackDocs(st.id, st.docs[0])
    docsHtml.value = DOMPurify.sanitize(marked.parse(String(md)))
  } catch (e) {
    docsHtml.value = ''
  } finally {
    docsLoading.value = false
  }
}

load()
</script>

<style scoped>
.sk-hint {
  color: var(--color-muted-foreground);
  font-size: 0.85rem;
}
.sk-card {
  margin-bottom: 16px;
  display: flex;
  flex-direction: column;
  transition: transform var(--dur-base) var(--ease-standard), box-shadow var(--dur-base) var(--ease-standard);
}
.sk-card:hover {
  transform: translateY(-2px);
}
.sk-card :deep(.el-card__body) {
  display: flex;
  flex-direction: column;
  flex: 1;
}
.sk-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}
.sk-name {
  font-weight: 600;
  font-size: 1rem;
  color: var(--color-foreground);
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.sk-desc {
  margin: 0 0 10px;
  font-size: 0.84rem;
  color: var(--color-muted-foreground);
  line-height: 1.6;
  min-height: 40px;
}
.sk-services {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  margin-bottom: 12px;
}
.sk-svc {
  font-size: 0.72rem;
}
.sk-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: auto;
  padding-top: 10px;
  border-top: 1px solid var(--color-border);
}
.sk-docs {
  min-height: 200px;
}
.markdown-body {
  font-size: 0.9rem;
  line-height: 1.7;
}
.markdown-body :deep(pre) {
  background: var(--color-muted);
  padding: 12px;
  border-radius: var(--radius-sm);
  overflow: auto;
}
.markdown-body :deep(code) {
  font-family: var(--font-mono);
  font-size: 0.82rem;
}
.markdown-body :deep(h1),
.markdown-body :deep(h2),
.markdown-body :deep(h3) {
  margin: 16px 0 8px;
}
.markdown-body :deep(img) {
  max-width: 100%;
}
</style>
