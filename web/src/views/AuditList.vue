<template>
  <div v-loading="loading">
    <el-card shadow="never">
      <div class="filters">
        <el-select v-model="q.action" placeholder="操作类型" clearable style="width: 150px" @change="load">
          <el-option v-for="a in actionOptions" :key="a" :label="a" :value="a" />
        </el-select>
        <el-select v-model="q.object_type" placeholder="对象类型" clearable style="width: 130px" @change="load">
          <el-option v-for="o in objectOptions" :key="o" :label="o" :value="o" />
        </el-select>
        <el-input v-model="q.username" placeholder="操作人" clearable style="width: 140px" @keyup.enter="load" @clear="load" />
        <el-select v-model="q.status" placeholder="状态" clearable style="width: 110px" @change="load">
          <el-option label="成功" value="success" />
          <el-option label="失败" value="failed" />
        </el-select>
        <el-date-picker
          v-model="range"
          type="daterange"
          range-separator="至"
          start-placeholder="开始日期"
          end-placeholder="结束日期"
          value-format="YYYY-MM-DD"
          @change="onRange"
        />
        <el-button type="primary" :icon="Refresh" :loading="loading" @click="load">查询</el-button>
        <el-button :icon="Search" @click="reset">重置</el-button>
      </div>

      <el-table :data="items" stripe border style="width: 100%">
        <el-table-column prop="created_at" label="时间" min-width="180" />
        <el-table-column prop="username" label="操作人" width="120" />
        <el-table-column label="操作" width="140">
          <template #default="{ row }">
            <el-tag effect="plain">{{ row.action }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="object_type" label="对象" width="100" />
        <el-table-column prop="source_ip" label="来源 IP" width="140" />
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag :type="row.status === 'success' ? 'success' : 'danger'" effect="light">
              {{ row.status === 'success' ? '成功' : '失败' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="detail" label="详情" min-width="200" show-overflow-tooltip />
      </el-table>

      <el-pagination
        class="pager"
        layout="total, prev, pager, next"
        :total="total"
        :current-page="q.page"
        :page-size="q.page_size"
        @current-change="onPage"
      />
    </el-card>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { Refresh, Search } from '@element-plus/icons-vue'
import { api } from '../api'

const items = ref([])
const total = ref(0)
const loading = ref(false)
const range = ref(null)

const actionOptions = ['login', 'create_vm', 'start_vm', 'stop_vm', 'restart_vm', 'delete_vm', 'create_host', 'update_host', 'delete_host', 'upload_image', 'delete_image']
const objectOptions = ['vm', 'host', 'image', 'user', 'system']

const q = reactive({ action: '', object_type: '', username: '', status: '', start: '', end: '', page: 1, page_size: 20 })

function onRange(val) {
  if (val && val.length === 2) {
    q.start = val[0]
    q.end = val[1]
  } else {
    q.start = ''
    q.end = ''
  }
  load()
}

function onPage(p) {
  q.page = p
  load()
}

function reset() {
  Object.assign(q, { action: '', object_type: '', username: '', status: '', start: '', end: '', page: 1 })
  range.value = null
  load()
}

async function load() {
  loading.value = true
  try {
    const params = {}
    for (const k of ['action', 'object_type', 'username', 'status', 'start', 'end', 'page', 'page_size']) {
      if (q[k]) params[k] = q[k]
    }
    const res = await api.listAudit(params)
    items.value = (res.data && res.data.items) || []
    total.value = (res.data && res.data.total) || 0
  } catch (e) {
    ElMessage.error('获取审计日志失败')
  } finally {
    loading.value = false
  }
}

onMounted(load)
</script>

<style scoped>
.filters {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  margin-bottom: 14px;
}
.pager {
  margin-top: 14px;
  justify-content: flex-end;
}
</style>
