<template>
  <!-- 落地/收敛预览对话框：列出后端算好的动作集合（新增/下线/重建/重启/无动作）+ 理由 -->
  <el-dialog v-model="visible" :title="diffMode === 'reconcile' ? '按差异收敛预览' : '落地预览'" width="720px">
    <p v-if="diffMode === 'reconcile'" class="ds-diff-tip">
      只对下列差异执行：<b class="ds-new">将创建</b> 补齐缺失、<b class="ds-del">将下线</b> 移除画布已删的、<b class="ds-warn">将重建/重启</b> 修正规格或运行态；未变化的资源不动。
    </p>
    <p v-else class="ds-diff-tip">
      全量落地：按依赖顺序落地画布内全部节点（<b class="ds-new">将创建</b> 新建，<b class="ds-old">无需动作</b> 的已存在按类型跳过/复用）。
    </p>
    <el-table :data="diffRows" size="small" max-height="360">
      <template #empty><el-empty description="无差异 / 画布为空" :image-size="60" /></template>
      <el-table-column label="名称" prop="name" min-width="150" />
      <el-table-column label="类型" prop="kind" width="90" />
      <el-table-column label="动作" width="96">
        <template #default="{ row }">
          <el-tag :type="actionMeta(row.action).type" effect="light" size="small">{{ actionMeta(row.action).label }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="理由" prop="reason" min-width="240" />
    </el-table>
    <template #footer>
      <el-button @click="visible = false">取消</el-button>
      <el-button type="primary" :loading="applying" @click="emit('confirm')">{{ diffMode === 'reconcile' ? '确认收敛' : '确认落地' }}</el-button>
    </template>
  </el-dialog>
</template>

<script setup>
// 架构设计器的「落地预览 / 按差异收敛预览」对话框：动作枚举与理由由后端收敛预演算出，
// 本组件只负责展示与标色；确认后由父级真正发起 apply / reconcile。
import { computed } from 'vue'

const props = defineProps({
  // 对话框开关（v-model）
  modelValue: { type: Boolean, default: false },
  // 'apply'（全量落地预览）| 'reconcile'（按差异收敛预览）——决定标题与提示文案
  diffMode: { type: String, default: 'apply' },
  // 差异行 [{ node, name, kind, action, side, reason }]
  diffRows: { type: Array, default: () => [] },
  // 提交中（按钮 loading）
  applying: { type: Boolean, default: false },
  // 动作枚举 → 展示元信息翻译函数（复用页面 ACTION_META）
  actionMeta: { type: Function, required: true }
})
const emit = defineEmits(['update:modelValue', 'confirm'])

// el-dialog 需要可写 v-model，经计算代理回写父级
const visible = computed({ get: () => props.modelValue, set: (v) => emit('update:modelValue', v) })
</script>

<style scoped>
/* 以下 .ds-diff-tip* 规则自 DesignerPage.vue 原样搬入（类名不变，DOM 不变） */
.ds-diff-tip {
  margin: 0 0 12px;
  font-size: 0.85rem;
  color: var(--color-muted-foreground);
  line-height: 1.6;
}
.ds-diff-tip .ds-new {
  color: #3aa76d;
}
.ds-diff-tip .ds-old {
  color: #909399;
}
.ds-diff-tip .ds-del {
  color: #f56c6c;
}
.ds-diff-tip .ds-warn {
  color: #e6a23c;
}
</style>
