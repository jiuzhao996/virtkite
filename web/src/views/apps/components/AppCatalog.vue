<template>
  <!-- 应用目录：分类/关键词过滤条 + 应用卡片网格。数据与安装状态全部由 AppStore 传入，
       本组件只负责展示与把「安装 / 查看脚本」意图抛回父级（不直接发请求）。 -->
  <el-card shadow="never" v-loading="loading">
    <!-- 分类筛选与搜索：radio-button 形态横排分类，关键词前端过滤 -->
    <div class="cat-filter">
      <div class="cat-filter-left">
        <el-radio-group :model-value="activeCategory" @update:model-value="emit('update:activeCategory', $event)">
          <el-radio-button v-for="cat in categoryTabs" :key="cat.value" :value="cat.value">{{ cat.label }}</el-radio-button>
        </el-radio-group>
        <el-input
          :model-value="appKeyword"
          class="app-search"
          placeholder="按应用名搜索"
          clearable
          :prefix-icon="Search"
          @update:model-value="emit('update:appKeyword', $event)"
        />
      </div>
    </div>

    <el-empty v-if="apps.length === 0" description="该分类下没有匹配的应用" :image-size="80" />
    <el-row v-else :gutter="12">
      <el-col v-for="app in apps" :key="app.id" :xs="24" :sm="12" :md="8" style="margin-bottom: 12px">
        <el-card shadow="hover" class="app-card">
          <div class="app-head">
            <span class="app-name">
              {{ app.name }}
              <span v-if="app.version" class="app-version">{{ app.version }}</span>
            </span>
            <div class="app-tags">
              <el-tag :type="categoryTag(app.category)" effect="light" size="small">{{ categoryText(app.category) }}</el-tag>
              <!-- 安装状态机：installing / installed / already / failed，行操作随状态置灰 -->
              <el-tag v-if="installTag(app.id)" :type="installTag(app.id).type" effect="light" size="small">
                {{ installTag(app.id).text }}
              </el-tag>
            </div>
          </div>
          <p class="app-desc">{{ app.desc || '暂无介绍' }}</p>
          <div class="app-actions">
            <el-button
              type="primary"
              :disabled="runningVMs.length === 0 || installing || installState[app.id] === 'installing'"
              :title="runningVMs.length === 0 ? '需要至少一台运行中的虚拟机' : ''"
              @click="emit('install', app)"
            >{{ installState[app.id] === 'installing' ? '安装中…' : '安装' }}</el-button>
            <el-button text type="primary" :disabled="installing" @click="emit('script', app)">查看脚本</el-button>
          </div>
        </el-card>
      </el-col>
    </el-row>
  </el-card>
</template>

<script setup>
// 「脚本应用」目录区：分类/搜索展示层 + 卡片操作意图上抛；安装请求与任务编排留在 AppStore。
import { Search } from '@element-plus/icons-vue'

const props = defineProps({
  loading: { type: Boolean, default: false },
  // 已按分类 + 关键词过滤后的应用列表（过滤在父级 computed 完成）
  apps: { type: Array, default: () => [] },
  runningVMs: { type: Array, default: () => [] },
  installing: { type: Boolean, default: false },
  // appId -> '' | 'installing' | 'installed' | 'already' | 'failed'
  installState: { type: Object, default: () => ({}) },
  activeCategory: { type: String, default: 'all' },
  appKeyword: { type: String, default: '' }
})

const emit = defineEmits(['update:activeCategory', 'update:appKeyword', 'install', 'script'])

const CATEGORY_TEXT = {
  web: 'Web 服务',
  database: '数据库',
  cache: '缓存',
  runtime: '运行时',
  cms: '内容管理',
  ops: '运维工具'
}

const categoryTabs = [
  { value: 'all', label: '全部' },
  { value: 'web', label: CATEGORY_TEXT.web },
  { value: 'database', label: CATEGORY_TEXT.database },
  { value: 'cache', label: CATEGORY_TEXT.cache },
  { value: 'runtime', label: CATEGORY_TEXT.runtime },
  { value: 'cms', label: CATEGORY_TEXT.cms },
  { value: 'ops', label: CATEGORY_TEXT.ops }
]

function categoryText(c) {
  return CATEGORY_TEXT[c] || c || '其他'
}

function categoryTag(c) {
  const map = { web: 'primary', database: 'success', cache: 'warning', runtime: 'info', cms: 'info', ops: 'info' }
  return map[c] || 'info'
}

function installTag(id) {
  switch (props.installState[id]) {
    case 'installing':
      return { type: 'warning', text: '安装中' }
    case 'installed':
      return { type: 'success', text: '已安装' }
    case 'already':
      return { type: 'success', text: '已安装' }
    case 'failed':
      return { type: 'danger', text: '安装失败' }
    default:
      return null
  }
}
</script>

<style scoped>
.cat-filter {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 12px;
}
.cat-filter-left {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.app-search {
  width: 200px;
}
.app-card :deep(.el-card__body) {
  display: flex;
  flex-direction: column;
  height: 100%;
}
.app-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 8px;
}
.app-name {
  font-size: 0.95rem;
  font-weight: 600;
}
.app-version {
  margin-left: 6px;
  font-size: 0.72rem;
  font-weight: normal;
  color: var(--el-text-color-secondary, #909399);
}
.app-tags {
  display: flex;
  align-items: center;
  gap: 4px;
  flex: none;
}
.app-desc {
  margin: 8px 0 12px;
  color: var(--el-text-color-secondary, #909399);
  font-size: 0.82rem;
  line-height: 1.5;
  min-height: 38px;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.app-actions {
  margin-top: auto;
  display: flex;
  align-items: center;
}
</style>
