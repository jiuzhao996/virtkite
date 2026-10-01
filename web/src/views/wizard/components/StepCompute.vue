<template>
  <div class="step-pane">
    <div class="step-head">
      <h3 class="step-title">计算资源</h3>
      <p class="step-desc">分配 CPU、内存与系统盘容量。</p>
    </div>
    <el-form label-width="140px" class="step-form">
      <el-form-item v-if="installMode !== 'clone'" label="虚拟机名称" required>
        <el-input v-model="form.name" placeholder="仅字母、数字、_、-" style="width: 380px" />
      </el-form-item>
      <el-form-item v-else label="虚拟机名称">
        <el-input :model-value="form.name" disabled style="width: 380px" />
        <span class="os-hint">克隆名称已在安装方式中填写</span>
      </el-form-item>
      <el-form-item label="vCPU 核数" required>
        <el-input-number v-model="form.vcpu" :min="1" :max="64" controls-position="right" />
        <span class="os-hint">1 ~ 64 核</span>
      </el-form-item>
      <el-form-item label="内存 (MB)" required>
        <el-input-number v-model="form.memoryMb" :min="256" :max="131072" :step="256" controls-position="right" />
        <span class="os-hint">256 MB ~ 131072 MB（步进 256）</span>
      </el-form-item>
      <el-form-item v-if="installMode === 'iso'" label="系统盘容量 (GB)" required>
        <el-input-number v-model="form.diskGb" :min="1" :max="500" controls-position="right" />
        <span class="os-hint">新建空白系统盘容量，默认 20 GB</span>
      </el-form-item>
      <!-- 云镜像方式同样是增量盘：容量是 qcow2 虚拟容量（读写上限），实际占用从元数据级起步 -->
      <el-form-item v-if="installMode === 'cloudimage'" label="系统盘容量 (GB)" required>
        <el-input-number v-model="form.diskGb" :min="5" :max="500" :step="5" controls-position="right" />
        <span class="os-hint">增量盘的读写上限（qcow2 虚拟容量），实际占用从几 MB 起步</span>
      </el-form-item>
      <el-form-item v-if="installMode === 'iso'" label="系统盘卷名">
        <el-input v-model="form.sysVolName" :placeholder="'默认 ' + (form.name || '虚拟机名') + '.qcow2，可自定义'" style="width: 380px" clearable />
        <div class="os-hint">留空自动命名；仅允许字母、数字、下划线和连字符</div>
      </el-form-item>
      <el-form-item label="存储池">
        <el-select v-model="form.storagePool" style="width: 380px" placeholder="选择存储池">
          <el-option v-for="p in usablePools" :key="p.name" :label="poolLabel(p)" :value="p.name" />
        </el-select>
        <div v-if="diskOverPool" class="os-hint" style="color: var(--el-color-danger)">
          <el-icon style="vertical-align: -2px"><WarningFilled /></el-icon>
          新系统盘 {{ form.diskGb }} GB 超出该池剩余空间（{{ poolAvailText(form.storagePool) }}），创建可能失败
        </div>
      </el-form-item>
      <el-form-item label="机器类型">
        <el-select v-model="form.machine" style="width: 380px">
          <el-option label="自动（libvirt 默认）" value="" />
          <el-option label="q35（PCIe 拓扑，推荐）" value="q35" />
          <el-option label="pc（i440fx，兼容旧系统）" value="pc" />
        </el-select>
        <span class="os-hint">CPU 直通与 Guest Agent 通道默认启用</span>
      </el-form-item>
    </el-form>
  </div>
</template>

<script setup>
// 向导第 2 步「计算资源」：CPU/内存/系统盘/存储池/机器类型。form 由壳持有（props 传入，
// v-model 直接写回共享对象）；池标签与超池预警函数由壳统一实现传入。
import { WarningFilled } from '@element-plus/icons-vue'

defineProps({
  form: { type: Object, required: true },
  installMode: { type: String, required: true },
  usablePools: { type: Array, required: true },
  diskOverPool: { type: Boolean, default: false },
  poolLabel: { type: Function, required: true },
  poolAvailText: { type: Function, required: true }
})
</script>

<style scoped>
.step-pane {
  padding: var(--space-lg);
}

.step-head {
  margin-bottom: var(--space-2xl);
}

.step-title {
  margin: 0 0 var(--space-sm);
  font-size: 1.05rem;
  font-weight: 600;
}

.step-desc {
  margin: 0;
  color: var(--color-muted-foreground);
  font-size: 0.88rem;
}

.step-form {
  max-width: 720px;
}

.os-hint {
  margin-left: var(--space-lg);
  color: var(--color-muted-foreground);
  font-size: 0.82rem;
}
</style>
