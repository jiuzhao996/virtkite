<template>
  <div v-loading="loading">
    <div class="page-head">
      <h2 class="page-title">宿主机管理</h2>
    </div>
    <el-card shadow="never">
      <div class="toolbar">
        <div>
          <el-button type="primary" :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
          <el-button v-if="isAdmin" type="success" :icon="Plus" @click="openCreate">添加宿主机</el-button>
        </div>
        <span class="count">共 {{ total }} 台</span>
      </div>

      <!-- 宿主机健康大卡（腾讯云服务器概要风格）：单管理节点下对象少，一行表格太空；
           每台一张大卡：头=名称/状态/操作，体=连接信息行 + 描述；实时资源仍走「查看状态」弹窗（SSH 采集） -->
      <el-empty v-if="!items.length && !loading" description="暂无宿主机，点击右上角「添加宿主机」登记本机信息" :image-size="80" />
      <div v-else class="host-cards">
        <el-card v-for="row in items" :key="row.id" shadow="hover" class="host-card">
          <div class="hc-head">
            <span class="hc-name">{{ row.name }}</span>
            <el-tag :type="hostStatusTag(row.status)" effect="light">{{ hostStatusText(row.status) }}</el-tag>
          </div>
          <div class="hc-rows">
            <div class="hc-row"><span class="hc-label">SSH 连接</span><span class="mono">{{ row.ssh_user }}@{{ row.ssh_ip }}:{{ row.ssh_port || 22 }}</span></div>
            <div class="hc-row"><span class="hc-label">描述</span><span>{{ row.description || '—' }}</span></div>
            <div class="hc-row"><span class="hc-label">登记时间</span><span>{{ row.created_at ? new Date(row.created_at).toLocaleString() : '—' }}</span></div>
          </div>
          <div class="hc-actions">
            <el-button :icon="Connection" :loading="testBusy.has(row.id)" @click="test(row)">测试连通</el-button>
            <el-button :icon="DataLine" @click="showStats(row)">查看状态</el-button>
            <el-button v-if="isAdmin" :icon="Edit" @click="openEdit(row)">编辑</el-button>
            <el-button v-if="isAdmin" type="danger" :icon="Delete" @click="remove(row)">删除</el-button>
          </div>
        </el-card>
      </div>
    </el-card>

    <el-dialog v-model="statsDialog" :title="(statsTarget ? statsTarget.name : '') + ' · 资源状态'" width="460px">
      <div v-if="!statsData" class="loading">加载中...</div>
      <el-alert v-else-if="statsData.error" :title="statsData.error" type="error" :closable="false" />
      <el-descriptions v-else :column="1" border>
        <el-descriptions-item label="主机名">{{ statsData.hostname || '—' }}</el-descriptions-item>
        <el-descriptions-item label="内核">{{ statsData.kernel || '—' }}</el-descriptions-item>
        <el-descriptions-item label="CPU">{{ statsData.cpu_cores || '—' }} 核</el-descriptions-item>
        <el-descriptions-item label="内存">{{ statsData.memory_used || '—' }} / {{ statsData.memory_total || '—' }}</el-descriptions-item>
        <el-descriptions-item label="运行时长">{{ statsData.uptime || '—' }}</el-descriptions-item>
      </el-descriptions>
    </el-dialog>

    <!-- 添加 / 编辑 复用一个弹窗：editingId 区分模式 -->
    <el-dialog v-model="dialog" :title="editingId ? '编辑宿主机' : '添加宿主机'" width="480px">
      <el-form :model="form" label-width="90px">
        <el-form-item label="名称" required>
          <el-input v-model="form.name" />
        </el-form-item>
        <el-form-item label="SSH IP" required>
          <el-input v-model="form.ssh_ip" />
        </el-form-item>
        <el-form-item label="SSH 端口">
          <el-input-number v-model="form.ssh_port" :min="1" :max="65535" />
        </el-form-item>
        <el-form-item label="SSH 用户">
          <el-input v-model="form.ssh_user" />
        </el-form-item>
        <el-form-item label="描述">
          <el-input v-model="form.description" type="textarea" :rows="2" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialog = false">取消</el-button>
        <el-button type="primary" :loading="creating" @click="submit">{{ editingId ? '保存' : '添加' }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh, Plus, Connection, DataLine, Edit, Delete } from '@element-plus/icons-vue'
import { api } from '../api'
import { useAuth } from '../store/auth'
import { hostStatusText, hostStatusTag, errMsg, isCancel } from '../utils/format'

const { isAdmin } = useAuth()

const items = ref([])
const total = ref(0)
const loading = ref(false)
const testBusy = ref(new Set())
const statsDialog = ref(false)
const statsTarget = ref(null)
const statsData = ref(null)
const dialog = ref(false)
const creating = ref(false)
// 编辑模式标识：null = 新建；编辑与新建复用同一弹窗与表单
const editingId = ref(null)

const form = reactive({
  name: '',
  ssh_ip: '',
  ssh_port: 22,
  ssh_user: 'root',
  description: ''
})

async function load() {
  loading.value = true
  try {
    const res = await api.listHosts()
    items.value = (res.data && res.data.items) || []
    total.value = (res.data && res.data.total) || 0
  } catch (e) {
    ElMessage.error('获取宿主机列表失败')
  } finally {
    loading.value = false
  }
}

async function test(h) {
  testBusy.value.add(h.id)
  testBusy.value = new Set(testBusy.value)
  try {
    const res = await api.testHost(h.id)
    const d = res.data || {}
    if (d.reachable) ElMessage.success('连通，延迟 ' + (d.latency_ms || '—') + ' ms')
    else ElMessage.warning('无法连通该宿主机')
  } catch (e) {
    ElMessage.error(errMsg(e, '连通性测试失败'))
  } finally {
    testBusy.value.delete(h.id)
    testBusy.value = new Set(testBusy.value)
  }
}

async function showStats(h) {
  statsTarget.value = h
  statsData.value = null
  statsDialog.value = true
  try {
    const res = await api.hostStats(h.id)
    statsData.value = res.data || {}
  } catch (e) {
    statsData.value = { error: errMsg(e, '获取失败') }
  }
}

async function remove(h) {
  try {
    await ElMessageBox.confirm('确定删除宿主机「' + h.name + '」？', '确认删除', { type: 'warning' })
    await api.deleteHost(h.id)
    ElMessage.success('已删除')
    await load()
  } catch (e) {
    // 点右上角 X 关闭返回 'close'，同样视为取消，不弹错误提示
    if (isCancel(e)) return
    ElMessage.error(errMsg(e, '删除失败'))
  }
}

function openCreate() {
  editingId.value = null
  Object.assign(form, {
    name: '',
    ssh_ip: '',
    ssh_port: 22,
    ssh_user: 'root',
    description: ''
  })
  dialog.value = true
}

function openEdit(row) {
  editingId.value = row.id
  Object.assign(form, {
    name: row.name || '',
    ssh_ip: row.ssh_ip || '',
    ssh_port: row.ssh_port || 22,
    ssh_user: row.ssh_user || 'root',
    description: row.description || ''
  })
  dialog.value = true
}

async function submit() {
  if (!form.name || !form.ssh_ip) {
    ElMessage.warning('请填写名称和 SSH IP')
    return
  }
  creating.value = true
  try {
    if (editingId.value) {
      await api.updateHost(editingId.value, { ...form })
      ElMessage.success('已保存')
    } else {
      await api.createHost({ ...form })
      ElMessage.success('添加成功')
    }
    dialog.value = false
    await load()
  } catch (e) {
    ElMessage.error(errMsg(e, editingId.value ? '保存失败' : '添加失败'))
  } finally {
    creating.value = false
  }
}

onMounted(load)
</script>

<style scoped>
/* .page-head / .page-title / .toolbar / .count 已收进 global.css */
.loading {
  color: var(--color-muted-foreground);
  text-align: center;
  padding: 30px 0;
}
.hc-head {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 12px;
}
.hc-name {
  font-size: 1.05rem;
  font-weight: 600;
}
.hc-rows {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.hc-row {
  display: flex;
  gap: 12px;
  font-size: 0.88rem;
}
.hc-label {
  width: 64px;
  flex-shrink: 0;
  color: var(--color-muted-foreground);
}
.hc-actions {
  display: flex;
  gap: 8px;
  margin-top: 14px;
  padding-top: 12px;
  border-top: 1px solid var(--color-border);
}
</style>
