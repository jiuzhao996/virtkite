<template>
  <div class="wizard" v-loading="loading">
    <div class="wizard-head">
      <el-button round :icon="ArrowLeft" @click="router.push({ name: 'vms' })">返回</el-button>
      <h2 class="wizard-title">创建虚拟机</h2>
      <span class="wizard-sub">对齐 virt-manager 创建向导 · 支持 ISO / 导入磁盘 / 云镜像 cloud-init / 克隆</span>
    </div>

    <el-steps :active="step" finish-status="success" align-center class="wizard-steps">
      <el-step title="安装方式" />
      <el-step title="计算资源" />
      <el-step title="磁盘与网络" />
      <el-step title="cloud-init" />
      <el-step title="确认创建" />
    </el-steps>

    <el-card shadow="never" class="wizard-body">
      <div v-if="step === 0" class="step-pane">
        <div class="step-head">
          <h3 class="step-title">选择安装方式</h3>
          <p class="step-desc">选择最合适的安装方式，设备型号将随操作系统自动推荐。</p>
        </div>

        <el-radio-group v-model="installMode" class="mode-grid" @change="onModeChange">
          <el-radio v-for="m in installModes" :key="m.value" :value="m.value" border class="mode-card">
            <span class="mode-icon">{{ m.icon }}</span>
            <span class="mode-label">{{ m.label }}</span>
            <span class="mode-desc">{{ m.desc }}</span>
          </el-radio>
        </el-radio-group>

        <el-divider />

        <el-form v-if="installMode === 'iso'" label-width="110px" class="step-form">
          <el-form-item label="操作系统" required>
            <el-select v-model="iso.osName" filterable placeholder="选择操作系统" style="width: 380px">
              <el-option v-for="os in options.osList" :key="os.name" :label="os.name" :value="os.name">
                <div class="opt-line">
                  <span>{{ os.name }}</span>
                  <span class="opt-hint">{{ os.disk_bus }} 磁盘 / {{ os.nic_model }} 网卡</span>
                </div>
              </el-option>
            </el-select>
          </el-form-item>
          <el-form-item label="ISO 路径" required>
            <div class="iso-row">
              <el-input v-model="iso.isoPath" placeholder="/path/to/install.iso" class="iso-path" />
              <el-select v-model="iso.isoImageId" clearable placeholder="或从镜像库选择" class="iso-picker">
                <el-option v-for="img in isoImages" :key="img.id" :label="img.name" :value="img.id">
                  <div class="opt-line">
                    <span>{{ img.name }}</span>
                    <span class="opt-hint">{{ img.path }}</span>
                  </div>
                </el-option>
                <template #empty><span class="opt-hint">镜像库暂无 ISO 镜像</span></template>
              </el-select>
            </div>
          </el-form-item>
          <el-alert type="info" :closable="false" show-icon title="安装介质将挂载为只读光驱，系统安装到新建的系统盘中。" />
        </el-form>

        <el-form v-if="installMode === 'import'" label-width="110px" class="step-form">
          <el-form-item label="磁盘路径" required>
            <el-input v-model="importDisk.source" placeholder="如 /var/lib/libvirt/images/disk.qcow2" style="width: 480px" />
          </el-form-item>
          <el-form-item label="操作系统">
            <el-select v-model="importDisk.osName" clearable filterable placeholder="选择操作系统（仅用于识别设备型号）" style="width: 380px">
              <el-option v-for="os in options.osList" :key="os.name" :label="os.name" :value="os.name" />
            </el-select>
          </el-form-item>
          <el-alert type="info" :closable="false" show-icon title="直接引用现有磁盘启动，不新建卷；操作系统选择仅用于展示与设备型号推荐。" />
        </el-form>

        <el-form v-if="installMode === 'cloudimage'" label-width="110px" class="step-form">
          <el-form-item label="云镜像 / 模板" required>
            <el-select v-model="cloudImage.imageId" filterable placeholder="选择云镜像或模板" style="width: 460px" @change="onCloudImageChange">
              <el-option v-for="img in cloudImageList" :key="img.id" :label="img.name" :value="img.id">
                <div class="opt-line">
                  <span>{{ img.name }}</span>
                  <span class="opt-tags">
                    <el-tag v-if="img.is_template" size="small" type="success" effect="plain">模板</el-tag>
                    <el-tag size="small" type="info" effect="plain">{{ img.format }}</el-tag>
                    <span class="opt-hint">{{ img.os_version || '' }} · {{ img.size_gb }} GB</span>
                  </span>
                </div>
              </el-option>
            </el-select>
          </el-form-item>
          <el-form-item v-if="cloudImage.imageId" label="识别系统">
            <template v-if="cloudImage.osName">
              <el-tag type="info" effect="plain">{{ cloudImage.osName }}</el-tag>
              <span class="os-hint">自动带出设备型号：{{ diskBus }} 磁盘 / {{ nicModel }} 网卡</span>
            </template>
            <span v-else class="os-hint">未匹配到已知系统，将使用默认设备型号（virtio）</span>
          </el-form-item>
          <el-form-item label="cloud-init" required>
            <el-switch v-model="cloudInitEnabled" active-text="启用" />
            <span v-if="cloudImage.osName && cloudInitSupported" class="os-hint">该系统支持 cloud-init</span>
            <span v-else class="os-hint">可手动启用，按需配置初始化</span>
          </el-form-item>

          <el-collapse v-if="cloudImage.imageId && cloudInitEnabled" v-model="ciPanels" class="ci-collapse">
            <el-collapse-item name="ci" title="cloud-init 配置">
              <el-form label-width="110px" class="ci-form">
                <el-form-item label="主机名">
                  <el-input v-model="cloudInit.hostname" :placeholder="'默认：' + (form.name || '虚拟机名')" style="width: 320px" />
                </el-form-item>
                <el-form-item label="用户名">
                  <el-input v-model="cloudInit.user" placeholder="如 ubuntu / root，可选" style="width: 320px" />
                </el-form-item>
                <el-form-item label="密码">
                  <el-input v-model="cloudInit.password" type="password" show-password placeholder="可选" style="width: 320px" />
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
                    <el-input v-model="cloudInit.ip" placeholder="如 192.168.122.10" style="width: 320px" />
                  </el-form-item>
                  <el-form-item label="网关">
                    <el-input v-model="cloudInit.gateway" placeholder="如 192.168.122.1" style="width: 320px" />
                  </el-form-item>
                  <el-form-item label="DNS">
                    <el-input v-model="cloudInit.dns" placeholder="逗号分隔，如 114.114.114.114" style="width: 320px" />
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

      <div v-else-if="step === 1" class="step-pane">
        <div class="step-head">
          <h3 class="step-title">计算资源</h3>
          <p class="step-desc">分配 CPU、内存与系统盘容量。</p>
        </div>
        <el-form label-width="140px" class="step-form">
          <el-form-item v-if="installMode !== 'clone'" label="虚拟机名称" required>
            <el-input v-model="form.name" placeholder="仅字母、数字、_、-" style="width: 320px" />
          </el-form-item>
          <el-form-item v-else label="虚拟机名称">
            <el-input :model-value="form.name" disabled style="width: 320px" />
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
          <el-form-item label="存储池">
            <el-select v-model="form.storagePool" style="width: 320px">
              <el-option v-for="p in options.pools" :key="p" :label="p" :value="p" />
            </el-select>
          </el-form-item>
        </el-form>
      </div>

      <div v-else-if="step === 2" class="step-pane">
        <div class="step-head">
          <h3 class="step-title">磁盘与网络</h3>
          <p class="step-desc">配置磁盘设备与网络接口。</p>
        </div>

        <template v-if="installMode !== 'clone'">
          <div class="section-bar">
            <h4 class="section-title">磁盘设备</h4>
            <el-button size="small" type="primary" plain :icon="Plus" @click="openDiskDialog">添加磁盘</el-button>
          </div>
          <el-table :data="diskRows" stripe border size="small" style="width: 100%">
            <el-table-column label="设备" width="110">
              <template #default="{ row }">
                <el-tag v-if="row.isSystem" type="primary" effect="plain">系统盘</el-tag>
                <el-tag v-else type="info" effect="plain">数据盘</el-tag>
              </template>
            </el-table-column>
            <el-table-column label="类型" width="110">
              <template #default="{ row }">
                <el-tag size="small">{{ kindLabel(row.kind) }}</el-tag>
              </template>
            </el-table-column>
            <el-table-column label="来源" min-width="220" show-overflow-tooltip>
              <template #default="{ row }">{{ diskSourceLabel(row) }}</template>
            </el-table-column>
            <el-table-column label="容量" width="110">
              <template #default="{ row }">{{ diskCapacityLabel(row) }}</template>
            </el-table-column>
            <el-table-column v-if="diskRows.length > 1" label="操作" width="70" align="center">
              <template #default="{ row }">
                <el-button v-if="!row.isSystem" size="small" type="danger" text :icon="Delete" @click="removeDisk(row)" />
              </template>
            </el-table-column>
          </el-table>
        </template>
        <el-alert
          v-else
          type="info"
          :closable="false"
          show-icon
          title="克隆方式下系统磁盘继承自源虚拟机，不支持修改磁盘。"
          style="margin-bottom: var(--space-xl)"
        />

        <el-divider />

        <div class="section-bar">
          <h4 class="section-title">网络接口</h4>
          <el-button size="small" type="primary" plain :icon="Plus" @click="addNic">添加第二网卡</el-button>
        </div>
        <div v-for="(nic, idx) in nics" :key="nic.id" class="nic-row">
          <span class="nic-index">网卡 {{ idx + 1 }}</span>
          <el-select v-model="nic.source" placeholder="选择网络" style="width: 260px">
            <el-option v-for="n in options.networks" :key="n" :label="n" :value="n" />
          </el-select>
          <span class="os-hint">{{ nicModel }} 模型</span>
          <el-button v-if="nics.length > 1" size="small" type="danger" text :icon="Delete" @click="removeNic(idx)" />
        </div>
      </div>

      <div v-else-if="step === 3" class="step-pane">
        <div class="step-head">
          <h3 class="step-title">cloud-init 初始化</h3>
          <p class="step-desc">首次启动时自动完成主机名、用户与网络配置。</p>
        </div>
        <el-alert v-if="installMode === 'clone'" type="info" :closable="false" show-icon title="克隆方式不支持 cloud-init 初始化。" style="margin-bottom: var(--space-xl)" />
        <template v-else>
          <el-form label-width="140px" class="step-form">
            <el-form-item label="启用 cloud-init">
              <el-switch v-model="cloudInitEnabled" active-text="启用" />
              <span class="os-hint">云镜像方式默认启用；其他方式可按需开启</span>
            </el-form-item>
          </el-form>
          <template v-if="cloudInitEnabled">
            <el-form label-width="140px" class="step-form">
              <el-form-item label="主机名">
                <el-input v-model="cloudInit.hostname" :placeholder="'默认：' + (form.name || '虚拟机名')" style="width: 320px" />
              </el-form-item>
              <el-form-item label="用户名">
                <el-input v-model="cloudInit.user" placeholder="如 ubuntu / root，可选" style="width: 320px" />
              </el-form-item>
              <el-form-item label="密码">
                <el-input v-model="cloudInit.password" type="password" show-password placeholder="可选" style="width: 320px" />
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
                  <el-input v-model="cloudInit.ip" placeholder="如 192.168.122.10" style="width: 320px" />
                </el-form-item>
                <el-form-item label="网关">
                  <el-input v-model="cloudInit.gateway" placeholder="如 192.168.122.1" style="width: 320px" />
                </el-form-item>
                <el-form-item label="DNS">
                  <el-input v-model="cloudInit.dns" placeholder="逗号分隔，如 114.114.114.114" style="width: 320px" />
                </el-form-item>
              </template>
            </el-form>
          </template>
        </template>
      </div>

      <div v-else class="step-pane">
        <div class="step-head">
          <h3 class="step-title">确认创建</h3>
          <p class="step-desc">核对配置后点击「创建虚拟机」，可随时返回上一步修改。</p>
        </div>

        <el-card v-if="submitting" shadow="never" class="creating-bar">
          <div class="creating-text">{{ submitText }}，请稍候…</div>
          <el-progress :percentage="createProgress" :stroke-width="8" />
        </el-card>

        <div class="summary-grid">
          <el-card shadow="never" class="summary-col">
            <template #header><span class="col-title">硬件清单</span></template>
            <div class="hw-list">
              <div class="hw-item"><span class="hw-key">vCPU</span><span class="hw-val">{{ form.vcpu }} 核</span></div>
              <div class="hw-item"><span class="hw-key">内存</span><span class="hw-val">{{ form.memoryMb }} MB（{{ (form.memoryMb / 1024).toFixed(1) }} GB）</span></div>
              <div class="hw-item"><span class="hw-key">引导顺序</span><span class="hw-val">{{ bootDevicesLabel }}</span></div>
              <div class="hw-item"><span class="hw-key">cloud-init</span><span class="hw-val">{{ cloudInitEnabled ? '已启用' : '未启用' }}</span></div>
              <el-divider />
              <div v-for="(d, i) in previewDisks" :key="i" class="hw-item">
                <span class="hw-key">{{ d.target }} · {{ d.device }}</span>
                <span class="hw-val hw-source">{{ d.source }}</span>
              </div>
              <el-divider />
              <div v-for="(n, i) in previewNics" :key="i" class="hw-item">
                <span class="hw-key">网卡 {{ i + 1 }}</span>
                <span class="hw-val">{{ n.model }} · {{ n.source }} · {{ n.mac }}</span>
              </div>
            </div>
          </el-card>

          <el-card shadow="never" class="summary-col">
            <template #header><span class="col-title">配置预览（DomainSpec）</span></template>
            <pre class="xml-preview">{{ domainSpecJson }}</pre>
          </el-card>
        </div>

        <el-card shadow="never" class="summary-bottom">
          <div class="summary-item"><span class="hw-key">虚拟机名称</span><span>{{ form.name || '—' }}</span></div>
          <div class="summary-item"><span class="hw-key">存储池</span><span>{{ form.storagePool || '—' }}</span></div>
          <div class="summary-item"><span class="hw-key">网络</span><span>{{ primaryNet || '—' }}</span></div>
          <div class="summary-item"><span class="hw-key">操作系统</span><span>{{ summaryOs }}</span></div>
          <div class="summary-item"><span class="hw-key">总容量</span><span>{{ totalCapacity }} GB</span></div>
        </el-card>
      </div>
    </el-card>

    <div class="wizard-footer">
      <el-button v-if="step > 0" @click="step--">上一步</el-button>
      <div class="footer-right">
        <el-button v-if="step < 4" type="primary" :icon="ArrowRight" @click="next">下一步</el-button>
        <el-button v-else type="primary" :icon="Check" :loading="submitting" :disabled="submitting" @click="submit">{{ submitText }}</el-button>
      </div>
    </div>

    <el-dialog v-model="diskDialog" title="添加磁盘" width="460px">
      <el-form label-width="90px">
        <el-form-item label="磁盘类型">
          <el-radio-group v-model="diskForm.kind">
            <el-radio value="create">新建空白盘</el-radio>
            <el-radio value="source">引用现有路径</el-radio>
            <el-radio value="image">引用云镜像</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item v-if="diskForm.kind === 'create'" label="容量 (GB)" required>
          <el-input-number v-model="diskForm.createGb" :min="1" :max="500" controls-position="right" />
        </el-form-item>
        <el-form-item v-if="diskForm.kind === 'source'" label="磁盘路径" required>
          <el-input v-model="diskForm.source" placeholder="如 /var/lib/libvirt/images/data.qcow2" style="width: 100%" />
        </el-form-item>
        <el-form-item v-if="diskForm.kind === 'image'" label="云镜像" required>
          <el-select v-model="diskForm.imageId" filterable placeholder="选择云镜像" style="width: 100%">
            <el-option v-for="img in cloudImageList" :key="img.id" :label="img.name" :value="img.id" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="diskDialog = false">取消</el-button>
        <el-button type="primary" @click="confirmDisk">确定</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { ArrowLeft, ArrowRight, Check, Plus, Delete } from '@element-plus/icons-vue'
import { api } from '../api'
import { pollTask, extractTaskId, taskErrorMessage } from '../utils/task.js'

const router = useRouter()

const step = ref(0)
const loading = ref(false)
const submitting = ref(false)
// 后台创建任务进度（0-100），驱动按钮文字与汇总页进度条
const createProgress = ref(0)
const submitText = computed(() =>
  !submitting.value ? '创建虚拟机' : createProgress.value > 0 ? `创建中 ${createProgress.value}%` : '创建中...'
)
const installMode = ref('iso')
const ciPanels = ref([])

const options = reactive({
  pools: [],
  storagePools: [],
  networks: [],
  networkInfo: [],
  cloudImages: [],
  osList: []
})
const vms = ref([])

const form = reactive({ name: '', storagePool: '', vcpu: 2, memoryMb: 2048, diskGb: 20 })
const iso = reactive({ osName: '', isoPath: '', isoImageId: null })
const importDisk = reactive({ osName: '', source: '' })
const cloudImage = reactive({ imageId: null, osName: '' })
const cloneVm = reactive({ sourceVmId: null })
const extraDisks = reactive([])
const nics = reactive([{ id: 1, source: '' }])

const cloudInitEnabled = ref(false)
const cloudInit = reactive({ hostname: '', user: '', password: '', sshKey: '', netMode: 'dhcp', ip: '', gateway: '', dns: '' })

const diskDialog = ref(false)
const diskForm = reactive({ kind: 'create', createGb: 20, source: '', imageId: null })

const installModes = [
  { value: 'iso', icon: '🖥', label: '本地安装介质 (ISO)', desc: '从 ISO 镜像安装系统到新建磁盘' },
  { value: 'import', icon: '💿', label: '导入现有磁盘', desc: '直接使用现有磁盘镜像启动' },
  { value: 'cloudimage', icon: '☁', label: '云镜像 + cloud-init', desc: '基于云镜像 / 模板，支持 cloud-init 初始化' },
  { value: 'clone', icon: '📋', label: '克隆现有 VM', desc: '从现有虚拟机创建链接克隆' }
]

const isoImages = computed(() => options.cloudImages.filter((i) => (i.format || '').toLowerCase() === 'iso'))
const cloudImageList = computed(() => options.cloudImages.filter((i) => (i.format || '').toLowerCase() !== 'iso'))

const activeOsName = computed(() => {
  if (installMode.value === 'iso') return iso.osName
  if (installMode.value === 'import') return importDisk.osName
  if (installMode.value === 'cloudimage') return cloudImage.osName
  return ''
})

const selectedOs = computed(() => options.osList.find((o) => o.name === activeOsName.value) || null)
const nicModel = computed(() => (selectedOs.value && selectedOs.value.nic_model) || 'virtio')
const diskBus = computed(() => (selectedOs.value && selectedOs.value.disk_bus) || 'virtio')
const cloudInitSupported = computed(() => installMode.value === 'cloudimage' && !!(selectedOs.value && selectedOs.value.cloud_init))

const cloneSource = computed(() => vms.value.find((v) => v.id === cloneVm.sourceVmId) || null)

const systemDisk = computed(() => {
  if (installMode.value === 'iso') return [{ id: 'sys', kind: 'create', createGb: form.diskGb, isSystem: true }]
  if (installMode.value === 'import') return [{ id: 'sys', kind: 'source', source: importDisk.source, isSystem: true }]
  if (installMode.value === 'cloudimage') {
    const img = options.cloudImages.find((i) => i.id === cloudImage.imageId)
    return [{ id: 'sys', kind: 'image', imageId: cloudImage.imageId, imageName: img ? img.name : '', sizeGb: img ? img.size_gb : 0, isSystem: true }]
  }
  return []
})

const diskRows = computed(() => [...systemDisk.value, ...extraDisks])

const isoPathLabel = computed(() => {
  if (installMode.value !== 'iso') return ''
  if (iso.isoImageId) {
    const img = options.cloudImages.find((i) => i.id === iso.isoImageId)
    return img ? img.path : ''
  }
  return iso.isoPath
})

const previewDisks = computed(() => {
  const list = []
  let vIdx = 0
  let hIdx = 0
  const vd = () => 'vd' + String.fromCharCode(97 + vIdx++)
  const hd = () => 'hd' + String.fromCharCode(97 + hIdx++)
  for (const d of diskRows.value) {
    list.push({
      target: vd(),
      type: 'file',
      device: 'disk',
      driver: d.kind === 'image' ? (findImage(d.imageId) ? findImage(d.imageId).format : 'qcow2') : d.kind === 'source' ? 'auto' : 'qcow2',
      bus: diskBus.value,
      source: diskSourceLabel(d),
      read_only: false
    })
  }
  if (isoPathLabel.value) {
    list.push({ target: hd(), type: 'file', device: 'cdrom', driver: 'raw', bus: 'ide', source: isoPathLabel.value, read_only: true })
  }
  if (cloudInitEnabled.value) {
    list.push({ target: hd(), type: 'file', device: 'cdrom', driver: 'raw', bus: 'ide', source: form.name + '-seed.iso（cloud-init）', read_only: true })
  }
  return list
})

const previewNics = computed(() =>
  nics.map((n) => ({ type: 'network', source: n.source || 'default', model: nicModel.value, mac: '后端自动分配' }))
)

const bootDevices = computed(() => {
  const hasCd = !!isoPathLabel.value || cloudInitEnabled.value
  return hasCd ? ['cdrom', 'hd'] : ['hd']
})
const bootDevicesLabel = computed(() => bootDevices.value.join(' → '))

const primaryNet = computed(() => (nics.length ? nics[0].source || 'default' : 'default'))
const summaryOs = computed(() => {
  if (installMode.value === 'clone') return cloneSource.value ? '克隆：' + cloneSource.value.name : '克隆源未选'
  return activeOsName.value || '未指定'
})

const totalCapacity = computed(() => {
  if (installMode.value === 'clone') return cloneSource.value ? cloneSource.value.disk_gb || 0 : 0
  let gb = 0
  for (const d of diskRows.value) {
    if (d.kind === 'create') gb += d.createGb
    else if (d.kind === 'image') gb += d.sizeGb || 0
  }
  return gb
})

const domainSpecJson = computed(() => {
  if (installMode.value === 'clone') {
    return JSON.stringify(
      {
        name: form.name,
        vcpu: form.vcpu,
        memory_mb: form.memoryMb,
        note: '克隆方式：基于源 VM 磁盘生成链接克隆（linked clone），磁盘由后端继承',
        boot: { devices: ['hd'] },
        disks: [{ source: cloneSource.value ? cloneSource.value.name + ' 系统盘（linked clone）' : '源虚拟机系统盘' }],
        interfaces: previewNics.value,
        network: primaryNet.value,
        graphics: { type: 'vnc', port: -1 },
        autostart: false
      },
      null,
      2
    )
  }
  const spec = {
    name: form.name,
    vcpu: form.vcpu,
    memory_mb: form.memoryMb,
    os_type: 'hvm',
    arch: 'x86_64',
    boot: { devices: bootDevices.value },
    disks: previewDisks.value,
    interfaces: previewNics.value,
    graphics: { type: 'vnc', port: -1 },
    autostart: false
  }
  if (cloudInitEnabled.value) spec.cloud_init = buildCloudInit()
  return JSON.stringify(spec, null, 2)
})

function findImage(id) {
  return options.cloudImages.find((i) => i.id === id) || null
}

// 默认存储池：优先选后端可写的池（路径不在 /var/lib 系统目录下，web 进程可写 seed ISO）
function pickDefaultPool(d) {
  const names = d.pools || []
  const infos = d.storage_pools || []
  const byName = {}
  for (const p of infos) byName[p.name] = p
  const writable = names.filter((n) => {
    const p = byName[n]
    return p && p.path && !p.path.startsWith('/var/lib')
  })
  return writable[0] || names[0] || 'vmops'
}

function kindLabel(k) {
  return { create: '新建卷', source: '引用路径', image: '云镜像' }[k] || k
}

function diskSourceLabel(d) {
  if (d.kind === 'create') return '新建空白卷（' + d.createGb + ' GB，池：' + (form.storagePool || 'vmops') + '）'
  if (d.kind === 'source') return d.source || '—'
  if (d.kind === 'image') return d.imageName || ('云镜像 #' + d.imageId)
  return ''
}

function diskCapacityLabel(d) {
  if (d.kind === 'create') return d.createGb + ' GB'
  if (d.kind === 'image') return (d.sizeGb ? d.sizeGb.toFixed(2) : '—') + ' GB'
  return '—'
}

function autoMatchOs(text) {
  const t = (text || '').toLowerCase()
  for (const os of options.osList) {
    const first = os.name.toLowerCase().split(' ')[0]
    if (first && t.includes(first)) return os
  }
  return null
}

function onModeChange() {
  ciPanels.value = []
  if (installMode.value !== 'cloudimage') {
    cloudInitEnabled.value = false
  } else if (cloudImage.imageId) {
    cloudInitEnabled.value = cloudInitSupported.value
    ciPanels.value = cloudInitSupported.value ? ['ci'] : []
  }
}

function onCloudImageChange(id) {
  cloudImage.osName = ''
  const img = options.cloudImages.find((i) => i.id === id)
  if (img) {
    const os = autoMatchOs(img.name + ' ' + (img.os_version || ''))
    if (os) cloudImage.osName = os.name
  }
  cloudInitEnabled.value = cloudInitSupported.value
  ciPanels.value = cloudInitSupported.value ? ['ci'] : []
}

function addNic() {
  nics.push({ id: Date.now(), source: options.networks.length ? options.networks[0] : '' })
}

function removeNic(idx) {
  if (nics.length > 1) nics.splice(idx, 1)
}

function openDiskDialog() {
  Object.assign(diskForm, { kind: 'create', createGb: 20, source: '', imageId: null })
  diskDialog.value = true
}

function confirmDisk() {
  if (diskForm.kind === 'create' && (!diskForm.createGb || diskForm.createGb < 1)) {
    ElMessage.warning('请填写磁盘容量')
    return
  }
  if (diskForm.kind === 'source' && !diskForm.source) {
    ElMessage.warning('请填写磁盘路径')
    return
  }
  if (diskForm.kind === 'image' && !diskForm.imageId) {
    ElMessage.warning('请选择云镜像')
    return
  }
  const row = { id: Date.now(), isSystem: false }
  if (diskForm.kind === 'create') {
    Object.assign(row, { kind: 'create', createGb: diskForm.createGb })
  } else if (diskForm.kind === 'source') {
    Object.assign(row, { kind: 'source', source: diskForm.source })
  } else {
    const img = findImage(diskForm.imageId)
    Object.assign(row, { kind: 'image', imageId: diskForm.imageId, imageName: img ? img.name : '', sizeGb: img ? img.size_gb : 0 })
  }
  extraDisks.push(row)
  diskDialog.value = false
}

function removeDisk(row) {
  const idx = extraDisks.findIndex((d) => d.id === row.id)
  if (idx > -1) extraDisks.splice(idx, 1)
}

function buildCloudInit() {
  const cfg = {
    hostname: cloudInit.hostname || form.name,
    net_mode: cloudInit.netMode
  }
  if (cloudInit.user) cfg.user = cloudInit.user
  if (cloudInit.password) cfg.password = cloudInit.password
  if (cloudInit.sshKey) cfg.ssh_key = cloudInit.sshKey
  if (cloudInit.netMode === 'static') {
    if (cloudInit.ip) cfg.ip = cloudInit.ip
    if (cloudInit.gateway) cfg.gateway = cloudInit.gateway
    const dnsList = cloudInit.dns.split(/[,，\s]+/).filter(Boolean)
    if (dnsList.length) cfg.dns = dnsList
  }
  return cfg
}

function buildDisks() {
  const arr = []
  for (const d of systemDisk.value) {
    if (d.kind === 'create') arr.push({ create_gb: d.createGb })
    else if (d.kind === 'source') arr.push({ source: d.source })
    else if (d.kind === 'image') arr.push({ source_image_id: d.imageId })
  }
  for (const d of extraDisks) {
    if (d.kind === 'create') arr.push({ create_gb: d.createGb })
    else if (d.kind === 'source') arr.push({ source: d.source })
    else if (d.kind === 'image') arr.push({ source_image_id: d.imageId })
  }
  return arr
}

function buildPayload() {
  const payload = {
    name: form.name,
    storage_pool: form.storagePool,
    vcpu: form.vcpu,
    memory_mb: form.memoryMb
  }
  if (installMode.value === 'iso') {
    payload.disks = buildDisks()
    payload.iso_path = isoPathLabel.value
  } else if (installMode.value === 'import') {
    payload.disks = buildDisks()
  } else if (installMode.value === 'cloudimage') {
    payload.disks = buildDisks()
  }
  payload.interfaces = nics.map((n) => ({ type: 'network', source: n.source || 'default', model: nicModel.value }))
  payload.network = primaryNet.value
  if (cloudInitEnabled.value) payload.cloud_init = buildCloudInit()
  return payload
}

function next() {
  if (step.value === 0) {
    if (installMode.value === 'iso') {
      if (!iso.osName) return ElMessage.warning('请选择操作系统')
      if (!iso.isoPath && !iso.isoImageId) return ElMessage.warning('请填写 ISO 路径或从镜像库选择')
    } else if (installMode.value === 'import') {
      if (!importDisk.source) return ElMessage.warning('请填写磁盘路径')
    } else if (installMode.value === 'cloudimage') {
      if (!cloudImage.imageId) return ElMessage.warning('请选择云镜像')
    } else if (installMode.value === 'clone') {
      if (!cloneVm.sourceVmId) return ElMessage.warning('请选择源虚拟机')
      if (!form.name) return ElMessage.warning('请填写新虚拟机名称')
    }
  }
  if (step.value === 1 && installMode.value !== 'clone' && !form.name) {
    return ElMessage.warning('请填写虚拟机名称')
  }
  if (step.value === 3 && cloudInitEnabled.value && cloudInit.netMode === 'static' && !cloudInit.ip) {
    return ElMessage.warning('静态网络模式请填写 IP 地址')
  }
  step.value++
}

async function submit() {
  submitting.value = true
  createProgress.value = 0
  try {
    const isClone = installMode.value === 'clone'
    const res = isClone
      ? await api.cloneVM(cloneVm.sourceVmId, {
          name: form.name,
          storage_pool: form.storagePool,
          vcpu: form.vcpu,
          memory_mb: form.memoryMb,
          network: primaryNet.value
        })
      : await api.createVM(buildPayload())
    // 后端返回 202 {task_id}，轮询到终态：成功跳转列表，失败留在汇总页展示 error
    await pollTask(extractTaskId(res), {
      onProgress: (t) => {
        createProgress.value = Math.max(0, Math.min(100, Number(t.progress) || 0))
      }
    })
    ElMessage.success(isClone ? '克隆创建成功' : '虚拟机创建成功')
    router.push({ name: 'vms' })
  } catch (e) {
    ElMessage.error(taskErrorMessage(e, '创建失败'))
  } finally {
    submitting.value = false
  }
}

onMounted(async () => {
  loading.value = true
  try {
    const [opt, vmsRes] = await Promise.all([api.vmOptions(), api.listVMs()])
    const d = opt.data || {}
    options.pools = d.pools || []
    options.storagePools = d.storage_pools || []
    options.networks = d.networks || []
    options.networkInfo = d.network_info || []
    options.cloudImages = d.cloud_images || []
    options.osList = d.os_list || []
    form.storagePool = pickDefaultPool(d)
    nics[0].source = options.networks.includes('default') ? 'default' : options.networks[0] || 'default'
    vms.value = (vmsRes.data && vmsRes.data.items) || []
  } catch (e) {
    ElMessage.error((e.response && e.response.data && e.response.data.message) || '加载创建选项失败')
  } finally {
    loading.value = false
  }
})
</script>

<style scoped>
.wizard {
  display: flex;
  flex-direction: column;
  min-height: 100%;
}

.wizard-head {
  display: flex;
  align-items: center;
  gap: var(--space-xl);
  margin-bottom: var(--space-2xl);
}

.wizard-title {
  margin: 0;
  font-size: 1.2rem;
  font-weight: 700;
}

.wizard-sub {
  color: var(--color-muted-foreground);
  font-size: 0.88rem;
}

.wizard-steps {
  margin-bottom: var(--space-2xl);
}

.wizard-body {
  flex: 1;
}

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
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-lg);
  width: 100%;
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

.iso-row {
  display: flex;
  gap: var(--space-lg);
  width: 100%;
}

.iso-path {
  flex: 1;
}

.iso-picker {
  width: 220px;
}

.ci-collapse {
  max-width: 720px;
  border-radius: var(--radius-md);
}

.ci-form {
  max-width: 640px;
}

.section-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--space-lg);
}

.section-title {
  margin: 0;
  font-size: 0.95rem;
  font-weight: 600;
}

.nic-row {
  display: flex;
  align-items: center;
  gap: var(--space-lg);
  margin-bottom: var(--space-lg);
}

.nic-index {
  width: 70px;
  color: var(--color-muted-foreground);
  font-size: 0.88rem;
}

.summary-grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  gap: var(--space-xl);
}

.summary-col {
  min-width: 0;
}

.summary-col :deep(.el-card__header) {
  padding: var(--space-lg) var(--space-xl);
}

.summary-col :deep(.el-card__body) {
  padding: var(--space-lg) var(--space-xl);
}

.col-title {
  font-weight: 600;
  color: var(--color-foreground);
}

.hw-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-lg);
}

.hw-item {
  display: flex;
  align-items: baseline;
  gap: var(--space-lg);
}

.hw-key {
  flex-shrink: 0;
  width: 120px;
  color: var(--color-muted-foreground);
  font-size: 0.85rem;
}

.hw-val {
  color: var(--color-foreground);
  font-size: 0.88rem;
  word-break: break-all;
}

.hw-source {
  font-family: var(--font-mono);
  font-size: 0.8rem;
}

.xml-preview {
  margin: 0;
  padding: var(--space-xl);
  background: #0f172a;
  color: #d8e2ef;
  border-radius: var(--radius-md);
  font-family: var(--font-mono);
  font-size: 0.78rem;
  line-height: 1.55;
  max-height: 460px;
  overflow: auto;
  white-space: pre;
}

.summary-bottom {
  margin-top: var(--space-xl);
}

.creating-bar {
  margin-bottom: var(--space-xl);
}

.creating-text {
  margin-bottom: var(--space-lg);
  font-weight: 600;
  color: var(--color-foreground);
}
.summary-bottom :deep(.el-card__body) {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2xl) var(--space-3xl);
  padding: var(--space-lg) var(--space-xl);
}

.summary-item {
  display: flex;
  align-items: baseline;
  gap: var(--space-lg);
}

.summary-item .hw-key {
  width: auto;
}

.wizard-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: var(--space-2xl);
}
</style>