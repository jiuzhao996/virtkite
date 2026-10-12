<template>
  <div class="ce">
    <textarea
      v-model="text"
      class="ce-editor"
      spellcheck="false"
      :disabled="disabled"
      :placeholder="placeholder"
      @input="onInput"
    />
    <div class="ce-status">
      <span v-if="checking" class="ce-checking">校验中…</span>
      <span v-else-if="valid === true" class="ce-ok">
        <el-icon><CircleCheck /></el-icon> 校验通过
      </span>
      <span v-else-if="valid === false" class="ce-bad">
        <el-icon><Warning /></el-icon> 校验未通过
      </span>
      <span v-else class="ce-idle">编辑后自动校验（防抖 800ms）</span>
    </div>
    <ul v-if="issues.length" class="ce-issues">
      <li v-for="(it, i) in issues" :key="i">
        <span v-if="it.line" class="ce-line mono">第 {{ it.line }} 行</span>
        <span class="ce-msg mono">{{ it.message }}</span>
      </li>
    </ul>
  </div>
</template>

<script setup>
// compose 在线编辑器的即时校验（借 Dockge 思路：仅编辑时校验 + 防抖降噪）。
//
// 与 Dockge 的差异：Dockge 用 CodeMirror6 + yaml.parseDocument 在前端解析；本平台前端无
// YAML 库（刻意不新增依赖），改走服务端权威校验（docker compose config），错误更准。
//
// 关键约定（对齐 Dockge）：**只在用户正在编辑时校验**——首次加载/切换栈时不校验，
// 避免一进编辑器就弹一堆历史错误；输入后防抖 800ms 再发请求，连续敲键只发一次。
import { ref, watch, onBeforeUnmount } from 'vue'
import { CircleCheck, Warning } from '@element-plus/icons-vue'
import { errMsg } from '../../../utils/format'
import { api } from '../../../api'

const props = defineProps({
  modelValue: { type: String, default: '' },
  disabled: { type: Boolean, default: false },
  placeholder: { type: String, default: '' }
})
const emit = defineEmits(['update:modelValue', 'valid'])

const text = ref(props.modelValue)
const issues = ref([])
const valid = ref(null) // null=未校验 / true=通过 / false=未通过
const checking = ref(false)

// 外部（父组件加载栈详情）重置内容：同步到内部 text，但**不触发校验**
watch(
  () => props.modelValue,
  (v) => {
    if (v !== text.value) {
      text.value = v
      reset()
    }
  }
)

let timer = null
let edited = false

function reset() {
  clearTimeout(timer)
  edited = false
  valid.value = null
  issues.value = []
}

function onInput() {
  emit('update:modelValue', text.value)
  edited = true
  clearTimeout(timer)
  timer = setTimeout(run, 800) // 防抖：连续输入只发最后一次
}

async function run() {
  if (!edited) return
  checking.value = true
  try {
    const res = await api.composeValidate(text.value)
    const d = res.data || {}
    valid.value = !!d.valid
    issues.value = d.issues || []
    emit('valid', valid.value)
  } catch (e) {
    valid.value = false
    issues.value = [{ line: 0, message: errMsg(e, '校验请求失败') }]
    emit('valid', false)
  } finally {
    checking.value = false
  }
}

onBeforeUnmount(() => clearTimeout(timer))

// 暴露手动校验（父组件「保存」前可调用，确保最新内容已过校验）
defineExpose({ validate: run })
</script>

<style scoped>
.ce {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.ce-editor {
  width: 100%;
  min-height: 340px;
  max-height: calc(100vh - 360px);
  padding: 12px;
  box-sizing: border-box;
  background: #0d1b2a;
  color: #cfe8ff;
  border: 1px solid var(--color-border, #e4e7ed);
  border-radius: var(--radius-md, 8px);
  font-family: 'SFMono-Regular', Consolas, 'Liberation Mono', Menlo, monospace;
  font-size: 0.8rem;
  line-height: 1.6;
  resize: vertical;
}
.ce-status {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 0.8rem;
}
.ce-status .el-icon {
  vertical-align: -2px;
}
.ce-ok {
  color: var(--el-color-success, #67c23a);
}
.ce-bad {
  color: var(--el-color-danger, #f56c6c);
}
.ce-checking {
  color: var(--el-color-warning, #e6a23c);
}
.ce-idle {
  color: var(--color-muted-foreground, #909399);
}
.ce-issues {
  margin: 0;
  padding: 8px 12px 8px 28px;
  background: rgba(245, 108, 108, 0.08);
  border: 1px solid rgba(245, 108, 108, 0.3);
  border-radius: var(--radius-sm, 4px);
  max-height: 160px;
  overflow: auto;
}
.ce-issues li {
  font-size: 0.8rem;
  color: var(--el-color-danger, #f56c6c);
  line-height: 1.7;
}
.ce-line {
  font-weight: 600;
  margin-right: 8px;
}
</style>
