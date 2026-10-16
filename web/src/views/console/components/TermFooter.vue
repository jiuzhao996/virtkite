<template>
  <div class="term-footer">
    <div class="term-footer-left">
      <template v-if="connected">
        <el-button size="small" class="ft-btn" :loading="connecting" @click="emit('reconnect')">
          <el-icon v-if="!connecting"><Refresh /></el-icon><span>重新连接</span>
        </el-button>
        <el-button size="small" class="ft-btn" @click="emit('disconnect')">断开</el-button>
      </template>
      <template v-else>
        <el-button size="small" class="ft-btn" @click="emit('back')">
          <el-icon><ArrowLeft /></el-icon><span>返回选择</span>
        </el-button>
      </template>
    </div>
    <div class="term-footer-right">
      <template v-if="connected">
        <span class="term-shortcut" title="xterm 内置快捷键"><el-icon><InfoFilled /></el-icon>复制 Ctrl+Shift+C ｜ 粘贴 Ctrl+Shift+V / Ctrl+V</span>
        <el-button size="small" class="ft-btn" title="粘贴剪贴板内容到终端（需浏览器授权剪贴板）" @click="emit('paste')">
          <el-icon><CopyDocument /></el-icon><span>粘贴</span>
        </el-button>
        <el-button size="small" class="ft-btn" title="缩小字号（11 ~ 24）" @click="emit('font-change', -1)">A-</el-button>
        <el-button size="small" class="ft-btn" title="放大字号（11 ~ 24）" @click="emit('font-change', 1)">A+</el-button>
        <el-button size="small" class="ft-btn" :title="isTermFull ? '退出全屏' : '终端全屏'" @click="emit('fullscreen')">
          <el-icon><FullScreen /></el-icon><span>{{ isTermFull ? '退出全屏' : '全屏' }}</span>
        </el-button>
      </template>
    </div>
  </div>
</template>

<script setup>
// 终端底部操作栏：左「重新连接 / 断开 / 返回选择」，右「粘贴 / 字号 A- A+ / 全屏」。
// 纯按钮组：所有动作 emit 上抛由 TermView 执行（重连/断开走 WS 生命周期，字号/全屏改终端状态）。
import { ArrowLeft, CopyDocument, FullScreen, InfoFilled, Refresh } from '@element-plus/icons-vue'

defineProps({
  connected: { type: Boolean, default: false },
  connecting: { type: Boolean, default: false },
  // 是否处于终端全屏（决定按钮文案）
  isTermFull: { type: Boolean, default: false }
})
const emit = defineEmits(['back', 'reconnect', 'disconnect', 'paste', 'font-change', 'fullscreen'])
</script>

<style scoped>
/* 以下规则自 TermView.vue 原样搬入（类名不变，DOM 不变） */
/* ---------- 底部操作栏 ---------- */
.term-footer {
  position: relative;
  z-index: 3;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 16px;
  background: var(--term-overlay);
  backdrop-filter: blur(4px);
  border-top: 1px solid var(--term-line);
  font-size: 0.85rem;
  color: var(--term-text-sub);
}
.term-footer-left { display: flex; gap: 8px; }
.term-footer-right { display: flex; align-items: center; gap: 14px; }
.ft-btn {
  color: var(--term-accent-bright);
  border-color: rgba(88, 166, 255, 0.3);
}
.ft-btn:hover {
  color: var(--term-accent-bright);
  border-color: rgba(88, 166, 255, 0.5);
  background: rgba(88, 166, 255, 0.08);
}
.term-shortcut { display: inline-flex; align-items: center; gap: 4px; color: var(--term-off); font-size: 12px; margin-right: 8px; }

/* 窄屏适配（原 TermView ≤640px 媒体查询中与 footer 相关的部分） */
@media (max-width: 640px) {
  .term-footer {
    flex-wrap: wrap;
    row-gap: 6px;
    padding: 8px 12px;
  }
  .term-footer-right { flex-wrap: wrap; row-gap: 4px; }
}
</style>
