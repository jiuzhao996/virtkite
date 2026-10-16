<template>
  <div class="serial-panel">
    <div class="serial-big-icon"><el-icon><Connection /></el-icon></div>
    <div class="serial-title">串口 Console · 免 IP 直连</div>
    <div class="serial-desc">等价 virsh console，直接读写 guest 串口 ttyS0。无需 IP / 账号，无网卡也能进系统，建议优先尝试。</div>
    <el-button type="primary" size="large" class="serial-btn" :loading="connecting" @click="emit('connect')">
      <el-icon v-if="!connecting"><CaretRight /></el-icon>
      <span>{{ connecting ? '连接中…' : '连接串口 Console' }}</span>
    </el-button>
    <div class="serial-hint">
      <el-icon><InfoFilled /></el-icon>
      <span>连上却无输出、敲键无回显？通常是客户机没在 ttyS0 起终端：请在客户机内执行 <code>systemctl enable --now serial-getty@ttyS0</code>，并把 <code>console=ttyS0</code> 追加到内核 cmdline（写入 <code>/etc/default/grub</code> 后执行 <code>grub2-mkconfig -o /boot/grub2/grub.cfg</code> 并重启生效）。</span>
    </div>
  </div>
</template>

<script setup>
// 串口未连接时的入口面板（无表单，只有一个醒目按钮 + 客户机侧排障提示）。
// 连接动作 emit('connect') 由 TermView 执行 connectSerial（含 2.5s 探测兜底）。
import { CaretRight, Connection, InfoFilled } from '@element-plus/icons-vue'

defineProps({
  connecting: { type: Boolean, default: false }
})
const emit = defineEmits(['connect'])
</script>

<style scoped>
/* 以下规则自 TermView.vue 原样搬入（类名不变，DOM 不变） */
/* ---------- 串口连接面板 ---------- */
.serial-panel {
  position: relative;
  z-index: 2;
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 12px;
  text-align: center;
  padding: 24px;
}
.serial-big-icon {
  width: 84px;
  height: 84px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--color-serial-gold);
  border: 2px solid rgba(240, 185, 11, 0.5);
  border-radius: 50%;
  background: rgba(8, 14, 24, 0.6);
}
/* 圆盘内的大图标：svg 不吃 text-shadow，原来的金色发光改用 drop-shadow 保留 */
.serial-big-icon .el-icon {
  font-size: 2.8rem;
  filter: drop-shadow(0 0 12px rgba(240, 185, 11, 0.55));
}
.serial-title { color: var(--term-text); font-size: 1.25rem; font-weight: 600; }
.serial-desc { max-width: 460px; color: var(--term-text-sub); font-size: 0.88rem; line-height: 1.6; }
.serial-btn { margin-top: 8px; }
/* 客户机侧排障提示：串口连上但无输出/无回显多半是 guest 没起 getty（见诊断结论），一句话提示即可 */
.serial-hint {
  display: flex;
  align-items: flex-start;
  gap: 4px;
  max-width: 500px;
  margin-top: 10px;
  color: var(--term-text-dim);
  font-size: 0.78rem;
  line-height: 1.7;
  text-align: left;
}
.serial-hint .el-icon { margin-top: 0.3em; flex: none; }
.serial-hint code {
  padding: 0 4px;
  border-radius: var(--radius-sm);
  background: rgba(240, 185, 11, 0.12);
  color: var(--color-serial-gold);
  font-family: 'SF Mono', 'Cascadia Code', Consolas, monospace;
  font-size: 0.75rem;
}
</style>
