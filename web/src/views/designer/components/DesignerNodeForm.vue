<template>
  <!-- 节点属性表单：VM 节点暴露落地参数（云镜像/规格/SSH/应用/Playbook），
       容器栈/网络节点只读引用或建议值；下方为该节点的连线列表与删除节点入口。
       根为「表单 + 分隔线 + 连线区 + 删除按钮」多个节点，与原内联块一致。 -->
  <el-form label-width="64px" size="small">
    <el-form-item label="名称"><el-input v-model="selected.name" /></el-form-item>
    <el-form-item label="类型"><el-tag size="small" effect="plain">{{ kindLabel[selected.kind] }}</el-tag></el-form-item>
    <!-- VM 节点落地参数：这些字段随计划保存，口令除外 -->
    <template v-if="selected.kind === 'vm'">
      <el-form-item label="云镜像">
        <el-select v-model="selected.ref" filterable placeholder="选择云镜像" style="width: 100%">
          <el-option v-for="img in cloudImages" :key="img.id" :label="img.name" :value="String(img.id)">
            <span>{{ img.name }}</span>
            <span class="ds-opt-sub">{{ img.os_version }}</span>
          </el-option>
        </el-select>
      </el-form-item>
      <el-form-item label="存储池">
        <el-select v-model="selected.pool" placeholder="默认池" style="width: 100%">
          <el-option v-for="p in storagePools" :key="p.name" :label="p.name" :value="p.name" />
        </el-select>
      </el-form-item>
      <el-form-item label="规格">
        <div class="ds-spec">
          <el-input-number v-model="selected.vcpu" :min="1" :max="16" size="small" controls-position="right" style="width: 88px" />
          <span class="ds-spec-unit">vCPU</span>
          <el-input-number v-model="selected.memory_mb" :min="512" :step="512" size="small" controls-position="right" style="width: 96px" />
          <span class="ds-spec-unit">MB</span>
        </div>
      </el-form-item>
      <el-form-item label="SSH 用户"><el-input v-model="selected.ssh_user" placeholder="root" /></el-form-item>
      <el-form-item label="SSH 口令">
        <el-input v-model="selected._sshSecret" type="password" show-password autocomplete="new-password" placeholder="仅本次落地使用，不随计划保存" />
      </el-form-item>
      <el-form-item label="应用">
        <el-select v-model="selected.apps" multiple filterable placeholder="落地后自动安装" style="width: 100%">
          <el-option v-for="a in apps" :key="a.id" :label="a.name" :value="a.id" />
        </el-select>
      </el-form-item>
      <el-form-item label="Playbook">
        <el-select v-model="selected.playbooks" multiple filterable placeholder="落地后自动执行（初始化/加固/优化）" style="width: 100%">
          <el-option v-for="p in playbooks" :key="p.id" :label="p.id + (p.desc ? '（' + p.desc + '）' : '')" :value="p.id" />
        </el-select>
      </el-form-item>
    </template>
    <el-form-item v-else-if="selected.kind === 'container'" label="引用栈">
      <span class="mono ds-ref">{{ selected.ref }}</span>
    </el-form-item>
    <el-form-item v-else label="建议值">
      <el-input v-model="selected.ref" placeholder="网段/用途" />
    </el-form-item>
    <el-form-item label="备注"><el-input v-model="selected.note" type="textarea" :rows="2" /></el-form-item>
  </el-form>
  <el-divider>连线</el-divider>
  <div class="ds-links">
    <div v-for="l in edgesOfSelected" :key="l.id" class="ds-link-row">
      <span class="mono">{{ l.text }}</span>
      <el-button text size="small" type="danger" :icon="Delete" @click="emit('remove-edge', l.id)" />
    </div>
    <div v-if="!edgesOfSelected.length" class="ds-link-empty">从节点边缘的连接点拉线到目标节点</div>
  </div>
  <el-divider />
  <el-button text type="danger" size="small" :icon="Delete" @click="emit('remove-node')">删除节点</el-button>
</template>

<script setup>
// 架构设计器右栏的「节点属性」编辑表单：直接编辑页面传入的 selected 对象字段
// （原地改属性，父级的 deep watch 会自动把改动回写 X6 节点文字与数据）。
// 删除连线/删除节点通过事件上抛，由页面操作 X6 图（本组件不持有 graph）。
import { Delete } from '@element-plus/icons-vue'

defineProps({
  // 当前选中节点（reactive 对象）：直接 v-model 改其字段
  selected: { type: Object, required: true },
  // 与该节点相连的连线（含文字描述），用于「连线」区展示与删除
  edgesOfSelected: { type: Array, default: () => [] },
  cloudImages: { type: Array, default: () => [] },
  storagePools: { type: Array, default: () => [] },
  apps: { type: Array, default: () => [] },
  playbooks: { type: Array, default: () => [] },
  // 节点类型 → 中文标签
  kindLabel: { type: Object, default: () => ({}) }
})
const emit = defineEmits(['remove-edge', 'remove-node'])
</script>

<style scoped>
/* 以下规则自 DesignerPage.vue 原样搬入（类名不变，DOM 不变）；
   .ds-h / .ds-right 等与左卡共用的样式仍留在页面 */
.ds-ref { font-size: 0.8rem; word-break: break-all; }
.ds-spec { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; }
.ds-spec-unit { font-size: 0.78rem; color: var(--color-muted-foreground); }
.ds-opt-sub { float: right; font-size: 0.75rem; color: var(--color-muted-foreground); }
.ds-links { display: flex; flex-direction: column; gap: 2px; }
.ds-link-row {
  display: flex; align-items: center; justify-content: space-between;
  font-size: 0.8rem; padding: 4px 0; color: var(--color-muted-foreground);
}
.ds-link-empty { font-size: 0.75rem; color: var(--color-muted-foreground); padding: 4px 0; }
</style>
