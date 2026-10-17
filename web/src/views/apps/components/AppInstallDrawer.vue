<template>
  <!-- 安装抽屉：应用名 + 版本 + 动态参数表单（按后端 schema 渲染）+ 高级折叠区。
       表单对象由父级持有（提交逻辑在 AppStore.submitInstall），这里只做 v-model 透传与事件上抛。 -->
  <el-drawer
    :model-value="modelValue"
    :title="currentApp ? `安装 ${currentApp.name}` : '安装应用'"
    size="40%"
    :close-on-click-modal="false"
    :close-on-press-escape="!installing"
    :show-close="!installing"
    @update:model-value="emit('update:modelValue', $event)"
    @closed="emit('closed')"
  >
    <div v-if="currentApp" class="drawer-sub">
      <el-tag v-if="currentApp.version" effect="plain" size="small">版本 {{ currentApp.version }}</el-tag>
      <span class="drawer-desc">{{ currentApp.desc }}</span>
    </div>
    <el-form label-width="96px">
      <el-form-item label="目标虚拟机" required>
        <el-select v-model="form.vm_id" placeholder="选择运行中的虚拟机" style="width: 100%" :disabled="installing" @change="emit('vm-change', $event)">
          <el-option v-for="vm in runningVMs" :key="vm.id" :label="vm.name" :value="vm.id" />
        </el-select>
      </el-form-item>
      <el-form-item label="SSH 地址" required>
        <el-input v-model="form.host" placeholder="虚拟机 IP，如 10.0.0.15" :disabled="installing" />
      </el-form-item>
      <el-form-item label="登录密码" required>
        <el-input v-model="form.password" type="password" show-password placeholder="SSH 登录密码" :disabled="installing" />
      </el-form-item>

      <!-- 动态参数表单：由后端 fields schema 通用渲染，前端不再硬编码每个应用的参数 -->
      <template v-if="normalFields.length">
        <el-divider content-position="left">安装参数</el-divider>
        <el-form-item v-for="f in normalFields" :key="f.key" :label="f.label" :required="f.required">
          <el-input v-if="f.type === 'text'" v-model="form.params[f.key]" :placeholder="f.placeholder || ''" :disabled="installing" />
          <el-input v-else-if="f.type === 'password'" v-model="form.params[f.key]" type="password" show-password :placeholder="f.placeholder || ''" :disabled="installing" />
          <el-input v-else-if="f.type === 'textarea'" v-model="form.params[f.key]" type="textarea" :rows="3" :placeholder="f.placeholder || ''" :disabled="installing" />
          <el-input-number v-else-if="f.type === 'number'" v-model="form.params[f.key]" :min="f.min" :max="f.max" controls-position="right" :disabled="installing" style="width: 100%" />
          <el-select v-else-if="f.type === 'select'" v-model="form.params[f.key]" :disabled="installing" style="width: 100%">
            <el-option v-for="o in (f.options || [])" :key="o.value" :label="o.label" :value="o.value" />
          </el-select>
          <el-switch v-else-if="f.type === 'bool'" v-model="form.params[f.key]" active-value="true" inactive-value="false" :disabled="installing" />
          <div v-if="f.help" class="field-help">{{ f.help }}</div>
        </el-form-item>
      </template>

      <!-- 高级折叠区：advanced 参数 + SSH 端口/用户名（有默认值，通常无需改动） -->
      <el-collapse class="adv-collapse">
        <el-collapse-item title="高级选项" name="adv">
          <el-form-item v-for="f in advancedFields" :key="f.key" :label="f.label" :required="f.required">
            <el-input v-if="f.type === 'text'" v-model="form.params[f.key]" :placeholder="f.placeholder || ''" :disabled="installing" />
            <el-input v-else-if="f.type === 'password'" v-model="form.params[f.key]" type="password" show-password :placeholder="f.placeholder || ''" :disabled="installing" />
            <el-input v-else-if="f.type === 'textarea'" v-model="form.params[f.key]" type="textarea" :rows="3" :placeholder="f.placeholder || ''" :disabled="installing" />
            <el-input-number v-else-if="f.type === 'number'" v-model="form.params[f.key]" :min="f.min" :max="f.max" controls-position="right" :disabled="installing" style="width: 100%" />
            <el-select v-else-if="f.type === 'select'" v-model="form.params[f.key]" :disabled="installing" style="width: 100%">
              <el-option v-for="o in (f.options || [])" :key="o.value" :label="o.label" :value="o.value" />
            </el-select>
            <el-switch v-else-if="f.type === 'bool'" v-model="form.params[f.key]" active-value="true" inactive-value="false" :disabled="installing" />
            <div v-if="f.help" class="field-help">{{ f.help }}</div>
          </el-form-item>
          <el-form-item label="SSH 端口 / 用户">
            <div class="port-user">
              <el-input-number v-model="form.port" :min="1" :max="65535" controls-position="right" :disabled="installing" />
              <el-input v-model="form.user" placeholder="用户名" style="flex: 1" :disabled="installing" />
            </div>
          </el-form-item>
        </el-collapse-item>
      </el-collapse>
    </el-form>
    <template #footer>
      <el-button :disabled="installing" @click="emit('update:modelValue', false)">取消</el-button>
      <el-button type="primary" :loading="installing" @click="emit('submit')">{{ installing ? '提交中…' : '开始安装' }}</el-button>
    </template>
  </el-drawer>
</template>

<script setup>
// 安装抽屉（纯表单壳）：渲染后端 schema 驱动的动态参数，字段值直接写入父级传入的 form 对象；
// 提交、校验、任务轮询与日志抽屉编排全部留在 AppStore，避免把请求逻辑散落到展示组件。
defineProps({
  modelValue: { type: Boolean, default: false },
  currentApp: { type: Object, default: null },
  runningVMs: { type: Array, default: () => [] },
  installing: { type: Boolean, default: false },
  // 父级持有的表单对象：{ vm_id, host, port, user, password, params }
  form: { type: Object, required: true },
  normalFields: { type: Array, default: () => [] },
  advancedFields: { type: Array, default: () => [] }
})

const emit = defineEmits(['update:modelValue', 'vm-change', 'submit', 'closed'])
</script>

<style scoped>
.drawer-sub {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 12px;
}
.drawer-desc {
  font-size: 0.8rem;
  color: var(--el-text-color-secondary, #909399);
}
.adv-collapse {
  margin-top: 4px;
}
.port-user {
  display: flex;
  gap: 8px;
  width: 100%;
}
.field-help {
  margin-top: 2px;
  font-size: 0.75rem;
  line-height: 1.4;
  color: var(--el-text-color-secondary, #909399);
}
</style>
