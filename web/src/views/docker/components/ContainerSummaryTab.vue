<template>
  <!-- 容器「概要」tab：基础信息 descriptions + 端口映射表 + 挂载表 + 网络接入/断开。
       数据由抽屉壳传入（row / parseInspect 结果），网络操作以事件上抛。 -->
  <el-descriptions :column="2" border size="small">
    <el-descriptions-item label="名称"><span class="mono">{{ row.Names || '—' }}</span></el-descriptions-item>
    <el-descriptions-item label="状态"><el-tag :type="stateTag(row.State)" effect="light" size="small">{{ stateText(row.State) }}</el-tag></el-descriptions-item>
    <el-descriptions-item label="镜像" :span="2">
      <router-link v-if="row.Image" class="mono lk" to="/images">{{ row.Image }}</router-link>
      <span v-else>—</span>
    </el-descriptions-item>
    <el-descriptions-item label="容器 ID" :span="2"><span class="mono">{{ shortId(row.ID) }}</span></el-descriptions-item>
    <el-descriptions-item label="启动命令" :span="2"><span class="mono">{{ vm.summary.command || '—' }}</span></el-descriptions-item>
    <el-descriptions-item label="重启策略">{{ vm.summary.restartPolicy || 'no' }}</el-descriptions-item>
    <el-descriptions-item label="网络模式">{{ vm.summary.networkMode || '—' }}</el-descriptions-item>
    <el-descriptions-item label="已接入网络" :span="2">
      <div class="net-row">
        <el-tag v-for="n in connectedNetworks" :key="n" size="small" closable @close="emit('disconnect', n)">{{ n }}</el-tag>
        <el-select
          :model-value="connectTarget"
          size="small"
          placeholder="接入网络"
          style="width: 170px"
          :disabled="!connectableNetworks.length"
          @update:model-value="emit('update:connectTarget', $event)"
          @change="emit('connect', $event)"
        >
          <el-option v-for="n in connectableNetworks" :key="n" :label="n" :value="n" />
        </el-select>
      </div>
    </el-descriptions-item>
    <el-descriptions-item label="创建时间" :span="2">{{ dockerTime(row.CreatedAt || row.Created) }}</el-descriptions-item>
  </el-descriptions>

  <div v-if="vm.ports.length" class="cd-sec">
    <div class="cd-sec-title">端口映射</div>
    <el-table :data="vm.ports" size="small" stripe>
      <el-table-column label="映射" min-width="200"><template #default="{ row: p }"><span class="mono">{{ portText(p) }}</span></template></el-table-column>
      <el-table-column label="" width="50"><template #default="{ row: p }"><CopyButton :text="portCopyText(p)" tip="复制 host:port" /></template></el-table-column>
    </el-table>
  </div>

  <div v-if="vm.mounts.length" class="cd-sec">
    <div class="cd-sec-title">挂载</div>
    <el-table :data="vm.mounts" size="small" stripe>
      <el-table-column label="源" min-width="180"><template #default="{ row: m }"><span class="mono">{{ m.source }}</span></template></el-table-column>
      <el-table-column label="目标" min-width="180"><template #default="{ row: m }"><span class="mono">{{ m.destination }}</span></template></el-table-column>
      <el-table-column label="读写" width="70"><template #default="{ row: m }"><el-tag size="small" :type="m.rw ? 'success' : 'warning'" effect="light">{{ m.mode }}</el-tag></template></el-table-column>
    </el-table>
  </div>
</template>

<script setup>
// 容器概要 tab：纯展示 + 网络接入/断开意图上抛（实际 API 调用仍在抽屉壳）。
import CopyButton from '../../../components/CopyButton.vue'
import { stateTag, stateText, dockerTime, shortId } from '../../../utils/docker-format'
import { portText, portCopyText } from '../../../utils/docker-inspect'

defineProps({
  row: { type: Object, required: true },
  vm: { type: Object, required: true },
  connectedNetworks: { type: Array, default: () => [] },
  connectableNetworks: { type: Array, default: () => [] },
  connectTarget: { type: String, default: '' }
})

const emit = defineEmits(['update:connectTarget', 'connect', 'disconnect'])
</script>

<style scoped>
/* 已接入网络：标签 + 接入下拉横排 */
.net-row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
}
.cd-sec {
  margin-top: 14px;
}
.cd-sec-title {
  font-weight: 600;
  font-size: 0.9rem;
  margin-bottom: 6px;
}
.lk {
  color: var(--el-color-primary);
  text-decoration: none;
}
.lk:hover {
  text-decoration: underline;
}
</style>
