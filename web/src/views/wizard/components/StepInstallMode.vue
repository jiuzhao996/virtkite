<template>
  <div class="step-pane">
    <div class="step-head">
      <h3 class="step-title">选择安装方式</h3>
      <p class="step-desc">选择最合适的安装方式，设备型号将随操作系统自动推荐。</p>
    </div>

    <el-radio-group v-model="installMode" class="mode-grid" @change="onModeChange">
      <el-radio v-for="m in installModes" :key="m.value" :value="m.value" border class="mode-card">
        <span class="mode-icon"><el-icon><component :is="m.icon" /></el-icon></span>
        <span class="mode-label">{{ m.label }}</span>
        <span class="mode-desc">{{ m.desc }}</span>
      </el-radio>
    </el-radio-group>

    <el-divider />

    <el-form v-if="installMode === 'iso'" label-width="110px" class="step-form">
      <!-- 先选介质，再按 ISO 文件名自动识别系统（识别不出可手动改）——对齐 virt-manager 的介质优先顺序 -->
      <el-form-item label="安装介质" required>
        <div class="iso-pick">
          <el-select
            v-if="!iso.manual"
            v-model="iso.pick"
            placeholder="选择 ISO 安装镜像（全部存储池）"
            style="width: 100%"
            @change="onIsoPick"
          >
            <el-option
              v-for="c in isoFlatList"
              :key="c.path"
              :label="c.label"
              :value="c.path"
            >
              <span>{{ c.name }}</span>
              <span class="opt-hint" style="float: right">{{ c.pool }} 池 · {{ c.sizeText }}</span>
            </el-option>
          </el-select>
          <el-input v-else v-model="iso.isoPath" placeholder="/path/to/install.iso" />
          <el-checkbox v-model="iso.manual" class="manual-toggle">手动输入路径</el-checkbox>
        </div>
      </el-form-item>
      <el-form-item label="操作系统" required>
        <el-select v-model="iso.osName" filterable placeholder="选择操作系统（可按 ISO 文件名自动识别）" style="width: 380px" @change="isoAutoDetected = false">
          <el-option v-for="os in options.osList" :key="os.name" :label="os.name" :value="os.name">
            <span>{{ os.name }}</span>
            <span class="opt-hint">{{ os.disk_bus }} 磁盘 / {{ os.nic_model }} 网卡</span>
          </el-option>
        </el-select>
        <div v-if="isoAutoDetected" class="os-hint" style="color: var(--el-color-success)">
          <el-icon style="vertical-align: -2px"><CircleCheck /></el-icon>
          已根据 ISO 文件名自动识别为「{{ iso.osName }}」，识别错误可手动更改
        </div>
      </el-form-item>
      <el-alert type="info" :closable="false" show-icon title="安装介质将挂载为只读光驱，系统安装到新建的系统盘中。列表覆盖全部激活存储池中的 ISO。" />
    </el-form>

    <el-form v-if="installMode === 'cloudimage'" label-width="110px" class="step-form">
      <el-form-item label="云镜像 / 模板" required>
        <el-select v-model="cloudImage.imageId" filterable placeholder="选择云镜像或模板" style="width: 380px" @change="onCloudImageChange">
          <el-option v-for="img in cloudImageList" :key="img.id" :label="img.name" :value="img.id">
            <div class="opt-line">
              <span>{{ img.name }}</span>
              <span class="opt-tags">
                <el-tag v-if="img.is_template" size="small" type="success" effect="plain">模板</el-tag>
                <el-tag size="small" type="info" effect="plain">{{ img.format }}</el-tag>
                <span class="opt-hint">{{ img.os_version || '' }} · {{ img.size_gb }} GB · {{ img.path }}</span>
              </span>
            </div>
          </el-option>
        </el-select>
        <div class="os-hint">镜像库是池内共享盘的「登记索引」：这里列出已登记的云镜像/模板（可跨池引用，建机基于它创建增量盘，不复制镜像数据）。想上架新的？到「存储池 → 卷抽屉」把任意卷登记进库。</div>
      </el-form-item>
      <el-form-item v-if="cloudImage.imageId" label="识别系统">
        <template v-if="cloudImage.osName">
          <el-tag type="info" effect="plain">{{ cloudImage.osName }}</el-tag>
          <span class="os-hint">自动带出设备型号：{{ diskBus }} 磁盘 / {{ nicModel }} 网卡</span>
        </template>
        <span v-else class="os-hint">未匹配到已知系统，将使用默认设备型号（virtio）</span>
      </el-form-item>
      <el-form-item v-if="cloudImage.imageId" label="系统盘">
        <span class="os-hint">
          将创建基于「{{ cloudImageName }}」的<b>增量盘</b>（qcow2 backing，不复制镜像文件，初始仅占用元数据级别空间；
          第 2 步的「系统盘容量」即该盘读写上限，实际占用随写入增长）
        </span>
      </el-form-item>
      <!-- cloud-init 只属于云镜像方式：镜像选中后就地展开配置（用户拍板：第 4 步对 ISO/导入方式显示 cloud-init 很乱） -->
      <el-form-item v-if="cloudImage.imageId && cloudInitSupported" label="cloud-init">
        <el-switch v-model="cloudInitEnabled" active-text="启用" />
        <span class="os-hint">首次启动自动完成主机名 / 用户 / 密码 / SSH 初始化（下方展开配置）</span>
      </el-form-item>
      <el-collapse v-if="cloudImage.imageId && cloudInitSupported && cloudInitEnabled" v-model="ciPanels" class="ci-collapse">
        <el-collapse-item name="ci" title="cloud-init 配置">
          <!-- 模板工具行：套用（回填）/ 存为模板 / 管理模板（v3 批次 L） -->
          <div class="ci-toolbar">
            <el-select
              v-model="ciTemplateId"
              placeholder="套用模板：一键回填下方配置"
              clearable
              style="width: 380px"
              @change="applyCiTemplate"
            >
              <el-option v-for="t in ciTemplates" :key="t.id" :label="t.name" :value="t.id">
                <span>{{ t.name }}</span>
                <span class="opt-hint" style="float: right">{{ t.spec && t.spec.net_mode === 'static' ? '静态 IP' : 'DHCP' }}</span>
              </el-option>
              <template #empty><span class="opt-hint">暂无模板，可点击右侧「存为模板」创建</span></template>
            </el-select>
            <el-button text type="primary" :icon="Plus" @click="saveAsTemplate">存为模板</el-button>
            <!-- 模板管理入口在系统设置页（IA 精简批次并入），设置页 admin-only，故仅管理员可见 -->
            <el-button v-if="isAdmin" text type="primary" @click="router.push('/settings')">管理模板</el-button>
          </div>
          <el-form label-width="110px" class="ci-form">
            <el-form-item label="主机名">
              <el-input v-model="cloudInit.hostname" :placeholder="'默认：' + (form.name || '虚拟机名')" style="width: 380px" />
            </el-form-item>
            <el-form-item label="用户名">
              <el-input v-model="cloudInit.user" placeholder="如 ubuntu / root，可选" style="width: 380px" />
            </el-form-item>
            <el-form-item label="密码">
              <el-input v-model="cloudInit.password" type="password" show-password placeholder="可选" style="width: 380px" />
            </el-form-item>
            <el-form-item label="SSH 公钥">
              <el-input v-model="cloudInit.sshKey" type="textarea" :rows="3" placeholder="粘贴 ssh-rsa / ssh-ed25519 公钥，可选" style="width: 480px" />
            </el-form-item>
            <el-form-item label="网络模式">
              <el-radio-group v-model="cloudInit.netMode">
                <el-radio value="dhcp">DHCP（自动获取）</el-radio>
                <el-radio value="static">静态 IP</el-radio>
              </el-radio-group>
            </el-form-item>
            <template v-if="cloudInit.netMode === 'static'">
              <el-form-item label="IP 地址">
                <el-input v-model="cloudInit.ip" placeholder="如 192.168.122.10" style="width: 380px" />
              </el-form-item>
              <el-form-item label="网关">
                <el-input v-model="cloudInit.gateway" placeholder="如 192.168.122.1" style="width: 380px" />
              </el-form-item>
              <el-form-item label="DNS">
                <el-input v-model="cloudInit.dns" placeholder="逗号分隔，如 114.114.114.114" style="width: 380px" />
              </el-form-item>
            </template>
          </el-form>
        </el-collapse-item>
      </el-collapse>
    </el-form>

    <el-form v-if="installMode === 'clone'" label-width="110px" class="step-form">
      <el-form-item label="源虚拟机" required>
        <el-select v-model="cloneVm.sourceVmId" filterable placeholder="选择要克隆的虚拟机" style="width: 380px">
          <el-option v-for="vm in vms" :key="vm.id" :label="vm.name" :value="vm.id">
            <div class="opt-line">
              <span>{{ vm.name }}</span>
              <span class="opt-hint">{{ vm.vcpu }} 核 / {{ (vm.memory_mb / 1024).toFixed(0) }} GB / {{ vm.disk_gb }} GB</span>
            </div>
          </el-option>
        </el-select>
      </el-form-item>
      <el-form-item label="新名称" required>
        <el-input v-model="form.name" placeholder="仅字母、数字、_、-" style="width: 380px" />
      </el-form-item>
      <el-alert type="info" :closable="false" show-icon title="将基于源虚拟机磁盘创建链接克隆（linked clone），父盘保留；克隆不支持修改磁盘。" />
    </el-form>
  </div>
</template>

<script setup>
// 向导第 1 步「安装方式」：ISO / 云镜像+cloud-init / 克隆 三形态的选择与就地配置。
// 共享表单对象（form/iso/cloudImage/cloneVm/cloudInit/options）由壳持有并以 props 传入——
// 同一实例跨步共享，成员变更（含 v-model）直接写回壳状态；installMode/cloudInitEnabled 因被
// 后续步骤与提交装配依赖，走 defineModel 由壳拥有。
import { ref, computed, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { CircleCheck, Plus, Monitor, Cloudy, CopyDocument } from '@element-plus/icons-vue'
import { api } from '../../../api'
import { errMsg, fmtSizeBytes } from '../../../utils/format.js'

const props = defineProps({
  form: { type: Object, required: true },
  iso: { type: Object, required: true },
  cloudImage: { type: Object, required: true },
  cloneVm: { type: Object, required: true },
  vms: { type: Array, required: true },
  options: { type: Object, required: true },
  cloudInit: { type: Object, required: true },
  diskBus: { type: String, default: 'virtio' },
  nicModel: { type: String, default: 'virtio' },
  cloudInitSupported: { type: Boolean, default: false },
  isAdmin: { type: Boolean, default: false },
  // 壳的 cloud-init spec 裁剪逻辑（模板保存与建机 payload 共用同一份）
  buildCiSpec: { type: Function, required: true }
})

const installMode = defineModel('installMode', { type: String, required: true })
const cloudInitEnabled = defineModel('cloudInitEnabled', { type: Boolean, default: false })

const router = useRouter()

const installModes = [
  { value: 'iso', icon: Monitor, label: '本地安装介质 (ISO)', desc: '从 ISO 镜像安装系统到新建磁盘' },
  { value: 'cloudimage', icon: Cloudy, label: '云镜像 + cloud-init', desc: '基于云镜像 / 模板，支持 cloud-init 初始化' },
  { value: 'clone', icon: CopyDocument, label: '克隆现有 VM', desc: '从现有虚拟机创建链接克隆' }
]

// ── ISO 选择与系统自动识别 ──

// 池→卷两级树：供 ISO 平铺列表扫描卷用（免手填绝对路径）
const volumeTree = computed(() =>
  (props.options.storagePools || [])
    .filter((p) => p.volumes && p.volumes.length)
    .map((p) => ({
      path: p.path,
      label: `${p.name}（${p.volumes.length} 个卷）`,
      children: p.volumes.map((v) => ({
        path: v.path,
        label: `${v.name} · ${fmtSizeBytes(v.capacity)}`,
      })),
    }))
)
// ISO 平铺列表：扫全部激活存储池中的 .iso 卷（不限定 img 池），标注所属池与大小
const isoFlatList = computed(() => {
  const out = []
  for (const p of volumeTree.value) {
    for (const c of p.children.filter((c) => c.path.toLowerCase().endsWith('.iso'))) {
      const nameAndSize = (c.label || '').split(' · ')
      out.push({
        path: c.path,
        name: nameAndSize[0] || c.path.split('/').pop(),
        pool: (p.label || '').split('（')[0],
        sizeText: nameAndSize[1] || ''
      })
    }
  }
  return out
})
// 按 ISO 文件名关键词自动识别操作系统：关键词命中后到 osList 里模糊匹配第一个含该词的系统名
// （osList 是带版本号的完整名单如 "Rocky Linux 9"，没有裸名，须模糊匹配）。识别不出保持空由用户手选。
// ISO 文件名关键词 → osList 精确条目（顺序即优先级，长词在前防短词抢先）。
// 此前用 includes 双向匹配，"win10" 无法命中 "Windows 10"（互不包含）导致识别静默失败。
const ISO_OS_KEYWORDS = [
  ['windows server 2022', 'Windows Server 2022'], ['win2k22', 'Windows Server 2022'],
  ['windows server 2019', 'Windows Server 2019'], ['win2k19', 'Windows Server 2019'],
  ['windows 11', 'Windows 11'], ['win11', 'Windows 11'],
  ['windows 10', 'Windows 10'], ['win10', 'Windows 10'],
  ['ubuntu 24.04', 'Ubuntu 24.04 LTS'], ['ubuntu 22.04', 'Ubuntu 22.04 LTS'],
  ['ubuntu 20.04', 'Ubuntu 20.04 LTS'], ['ubuntu', 'Ubuntu 24.04 LTS'],
  ['rocky', 'Rocky Linux 9'], ['almalinux', 'Rocky Linux 9'], ['alma', 'Rocky Linux 9'],
  ['centos', 'CentOS Stream 9'], ['debian 12', 'Debian 12'], ['debian', 'Debian 12'],
  ['fedora', 'Fedora 40'], ['opensuse', 'SUSE SLES 15'], ['arch', 'Arch Linux'],
  ['windows', 'Windows 10'],
  ['kylin', 'Kylin V10 (银河麒麟)'], ['uos', 'UOS V20 (统信)'], ['deepin', 'Kylin V10 (银河麒麟)'],
  ['alpine', 'Generic Linux']
]
const isoAutoDetected = ref(false)
function detectOsFromIso(path) {
  const file = (path || '').toLowerCase()
  for (const [kw, name] of ISO_OS_KEYWORDS) {
    if (!file.includes(kw)) continue
    const hit = props.options.osList.find((o) => o.name === name)
    if (hit) {
      props.iso.osName = hit.name
      isoAutoDetected.value = true
      return
    }
  }
  isoAutoDetected.value = false
}

function onIsoPick(val) {
  // 平铺 el-select 的 change 参数是路径字符串本身（旧级联才是数组）
  props.iso.isoPath = val || ''
  detectOsFromIso(props.iso.isoPath)
}
// 手动输入路径时实时识别（勾选「手动输入路径」后的输入框）
watch(() => props.iso.isoPath, (v) => { if (props.iso.manual && v) detectOsFromIso(v) })

// 常见 ISO 文件名别名 → osList 精确条目（virsh 风格缩写如 Win10_22H2 / rocky10 无法被 first-word 匹配）
const OS_ALIAS = [
  ['win10', 'Windows 10'], ['win11', 'Windows 11'],
  ['win2k22', 'Windows Server 2022'], ['win2k19', 'Windows Server 2019'],
  ['winserver', 'Windows Server 2022'],
  ['rocky', 'Rocky Linux 9'], ['almalinux', 'Rocky Linux 9'],
  ['centos', 'CentOS Stream 9'], ['ubuntu', 'Ubuntu 24.04 LTS'],
  ['debian', 'Debian 12'], ['openeuler', 'openEuler 22.03'],
  ['kylin', 'Kylin V10 (银河麒麟)'], ['uos', 'UOS V20 (统信)'],
  ['fedora', 'Fedora 40'], ['suse', 'SUSE SLES 15'], ['arch', 'Arch Linux']
]
function autoMatchOs(text) {
  const t = (text || '').toLowerCase()
  // 1) 原逻辑：OS 名首词被文件名包含（如 ubuntu/kylin 全名出现时可直接命中）
  for (const os of props.options.osList) {
    const first = os.name.toLowerCase().split(' ')[0]
    if (first && t.includes(first)) return os
  }
  // 2) 别名兜底：Win10 → Windows 10 等（2026-09 修复：Win10_22H2.iso 此前识别不出）
  for (const [alias, name] of OS_ALIAS) {
    if (t.includes(alias)) {
      const os = props.options.osList.find((o) => o.name === name)
      if (os) return os
    }
  }
  return null
}

// 云镜像列表：镜像库里非 ISO 的登记卷（ISO 是安装介质，走存储池扫描，不属于这里）
const cloudImageList = computed(() =>
  (props.options.cloudImages || []).filter((i) => (i.format || '').toLowerCase() !== 'iso')
)
const cloudImageName = computed(() => {
  const img = (props.options.cloudImages || []).find((i) => i.id === props.cloudImage.imageId)
  return img ? img.name : '所选镜像'
})

// 安装方式切换：cloud-init 面板的展开/收起与启用状态联动
const ciPanels = ref([])
function onModeChange() {
  ciPanels.value = []
  if (installMode.value !== 'cloudimage') {
    cloudInitEnabled.value = false
  } else if (props.cloudImage.imageId) {
    cloudInitEnabled.value = props.cloudInitSupported
    ciPanels.value = props.cloudInitSupported ? ['ci'] : []
  }
}

function onCloudImageChange(id) {
  props.cloudImage.osName = ''
  const img = props.options.cloudImages.find((i) => i.id === id)
  if (img) {
    const os = autoMatchOs(img.name + ' ' + (img.os_version || ''))
    if (os) props.cloudImage.osName = os.name
  }
  cloudInitEnabled.value = props.cloudInitSupported
  ciPanels.value = props.cloudInitSupported ? ['ci'] : []
}

// ── cloud-init 模板（v3 批次 L）：面板展开时懒加载，套用回填 / 存为模板 / 管理页跳转 ──
const ciTemplates = ref([])
const ciTemplateId = ref(null)
const ciTemplatesLoaded = ref(false)
async function ensureCiTemplates(force = false) {
  if (ciTemplatesLoaded.value && !force) return
  try {
    const res = await api.listCloudInitTemplates()
    ciTemplates.value = (res.data && res.data.items) || []
    ciTemplatesLoaded.value = true
  } catch {
    /* 模板列表加载失败静默：不阻塞建机主流程；「存为模板」走 POST 自身会报错 */
  }
}
watch(ciPanels, (v) => {
  if (Array.isArray(v) && v.includes('ci')) ensureCiTemplates()
})
// 套用模板：模板 spec 回填进 cloudInit（清除选择 = 不动当前配置）
function applyCiTemplate(tplId) {
  if (!tplId) return
  const t = ciTemplates.value.find((x) => x.id === tplId)
  if (!t || !t.spec) return
  const s = t.spec
  Object.assign(props.cloudInit, {
    hostname: s.hostname || '',
    user: s.user || '',
    password: s.password || '',
    sshKey: s.ssh_key || '',
    netMode: s.net_mode === 'static' ? 'static' : 'dhcp',
    ip: s.ip || '',
    gateway: s.gateway || '',
    dns: Array.isArray(s.dns) ? s.dns.join(', ') : ''
  })
  ElMessage.success(`已套用模板「${t.name}」，可继续微调`)
}
// 存为模板：把当前 cloud-init 配置保存为可复用模板（hostname 为空不落「虚拟机名」默认值，
// 模板应是通用配置，套用时空主机名自然回落为各台机器自己的名字）
async function saveAsTemplate() {
  let name = ''
  try {
    const r = await ElMessageBox.prompt('把当前 cloud-init 配置保存为模板，以后建机可一键套用', '保存为模板', {
      inputPlaceholder: '模板名，如「教学实验机默认配置」',
      inputPattern: /\S/,
      inputErrorMessage: '模板名不能为空',
      confirmButtonText: '保存',
      cancelButtonText: '取消'
    })
    name = (r.value || '').trim()
  } catch {
    return // 用户取消
  }
  try {
    const res = await api.createCloudInitTemplate({ name, description: '', spec: props.buildCiSpec() })
    ElMessage.success('模板已保存，可在「管理模板」中维护')
    await ensureCiTemplates(true)
    const item = res && res.data
    if (item && item.id) ciTemplateId.value = item.id
  } catch (e) {
    ElMessage.error(errMsg(e, '保存模板失败'))
  }
}
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

.mode-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr)); /* 三张方式卡一行排齐 */
  gap: var(--space-lg);
  width: 100%;
}

/* 窄屏方式卡回落单列，避免三列挤压 */
@media (max-width: 992px) {
  .mode-grid {
    grid-template-columns: 1fr;
  }
}

.mode-card {
  width: 100%;
  height: auto;
  margin: 0;
  padding: var(--space-xl);
  border-radius: var(--radius-md);
  box-sizing: border-box;
}

.mode-card :deep(.el-radio__label) {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--space-xs);
  white-space: normal;
  padding-left: 6px;
}

.mode-card :deep(.el-radio__input) {
  margin-top: 3px;
}

.mode-icon {
  font-size: 1.35rem;
}

.mode-label {
  font-weight: 600;
  color: var(--color-foreground);
}

.mode-desc {
  color: var(--color-muted-foreground);
  font-size: 0.82rem;
}

.mode-card :deep(.el-radio.is-checked) {
  border-color: var(--el-color-primary);
  background: var(--el-color-primary-light-9);
}

.opt-line {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-lg);
  width: 100%;
}

.opt-tags {
  display: inline-flex;
  align-items: center;
  gap: var(--space-sm);
}

.opt-hint {
  color: var(--color-muted-foreground);
  font-size: 0.8rem;
}

.os-hint {
  margin-left: var(--space-lg);
  color: var(--color-muted-foreground);
  font-size: 0.82rem;
}

/* 池→卷级联选择器 + 手动输入开关（ISO / 导入磁盘共用） */
.iso-pick {
  width: 520px;
}

.manual-toggle {
  margin-top: var(--space-sm);
}

.manual-toggle :deep(.el-checkbox__label) {
  font-size: 0.82rem;
  color: var(--el-text-color-secondary);
}

.ci-collapse {
  max-width: 720px;
  border-radius: var(--radius-md);
}

.ci-form {
  max-width: 640px;
}

/* cloud-init 面板顶部的模板工具行：套用下拉 + 存为模板 + 管理模板 */
.ci-toolbar {
  display: flex;
  align-items: center;
  gap: var(--space-sm);
  margin-bottom: var(--space-lg);
}
</style>
