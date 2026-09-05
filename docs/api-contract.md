---
title: "API 契约"
description: "vmops 对齐 virt-manager + PVE 改造：后端 virt 层 Go 函数签名与 REST 接口契约（Wave 1/Wave 2 唯一事实源）"
tags: [契约, virt-manager, PVE, 后端, 前端]
---

# API 契约（唯一事实源）

> 所有子代理（B1/B2/B3/B4/F1-F4）**严格依据本文件**编码。修改本文件需主 agent 确认。

## 核心模型：DomainSpec（service/virt/spec.go，B1 产出）

```go
// DiskSpec 磁盘设备
type DiskSpec struct {
    Type     string `json:"type"`          // file / block
    Device   string `json:"device"`        // disk / cdrom / floppy
    Driver   string `json:"driver"`        // qcow2 / raw / iso
    Bus      string `json:"bus"`           // virtio / ide / sata / scsi
    Source   string `json:"source"`        // 文件路径
    Target   string `json:"target"`        // vda / hda / sda（自动分配）
    ReadOnly bool   `json:"read_only"`
    BackingFile string `json:"backing_file,omitempty"` // 增量克隆父盘（展示用）
}

// InterfaceSpec 网卡
type InterfaceSpec struct {
    Type   string `json:"type"`   // network / bridge / direct
    Source string `json:"source"` // 网络名或桥名
    MAC    string `json:"mac"`
    Model  string `json:"model"`  // virtio / e1000 / rtl8139
}

// GraphicsSpec 显示（当前仅 VNC）
type GraphicsSpec struct {
    Type string `json:"type"` // vnc
    Port int    `json:"port"`
}

// BootSpec 引导顺序（hd / cdrom / network）
type BootSpec struct {
    Devices []string `json:"devices"`
}

// CloudInitSpec cloud-init 配置（创建 VM 时可选）
type CloudInitSpec struct {
    Hostname string   `json:"hostname,omitempty"`
    User     string   `json:"user,omitempty"`
    Password string   `json:"password,omitempty"`
    SSHKey   string   `json:"ssh_key,omitempty"`
    NetMode  string   `json:"net_mode,omitempty"` // dhcp / static
    IP       string   `json:"ip,omitempty"`
    Gateway  string   `json:"gateway,omitempty"`
    DNS      []string `json:"dns,omitempty"`
}

// DomainSpec 域完整定义
type DomainSpec struct {
    Name       string           `json:"name"`
    UUID       string           `json:"uuid"`
    VCPU       int              `json:"vcpu"`
    MemoryMB   int              `json:"memory_mb"`
    OSType     string           `json:"os_type"`
    Arch       string           `json:"arch"`
    Machine    string           `json:"machine"`
    Boot       BootSpec         `json:"boot"`
    Disks      []DiskSpec       `json:"disks"`
    Interfaces []InterfaceSpec  `json:"interfaces"`
    Graphics   GraphicsSpec     `json:"graphics"`
    Autostart  bool             `json:"autostart"`
    CloudInit  *CloudInitSpec   `json:"cloud_init,omitempty"`
    RawXML     string           `json:"raw_xml,omitempty"` // dumpxml 原文（编辑回显）
}
```

**B1 必须产出以下函数（签名固定，B4/F2 依赖）**：

```go
// spec.go
func (v *Virt) GetDomainSpec(name string) (*DomainSpec, error)   // dumpxml → Parse
func ParseDomainXML(xml string) (*DomainSpec, error)             // 纯函数
func BuildDomainXML(spec *DomainSpec) (string, error)            // 纯函数，含 graphics/features/acpi
func NextDiskTarget(spec *DomainSpec, bus string) string         // 自动分配 vda/sda/hda 序号

// device.go
func (v *Virt) AttachDisk(domain string, d DiskSpec) error        // DomainAttachDevice
func (v *Virt) DetachDisk(domain, target string) error            // DomainDetachDevice
func (v *Virt) AttachInterface(domain string, i InterfaceSpec) error
func (v *Virt) DetachInterface(domain, mac string) error
func (v *Virt) SetVcpus(domain string, n int) error               // live+config flags
func (v *Virt) SetMemory(domain string, mb int) error             // live+config flags

// domain2.go
func (v *Virt) PauseDomain(domain string) error                   // DomainSuspend
func (v *Virt) ResumeDomain(domain string) error                  // DomainResume
func (v *Virt) SetAutostart(domain string, enabled bool) error    // DomainSetAutostart
func (v *Virt) GetAutostart(domain string) (bool, error)
```

## 快照增强（B2，service/virt/snapshot.go 改造）

```go
type SnapshotInfo struct {
    Name         string `json:"name"`
    Description  string `json:"description"`
    CreationTime int64  `json:"creation_time"` // unix 秒
    State        string `json:"state"`         // 平台状态映射
}
func (v *Virt) ListSnapshots(domainName string) ([]SnapshotInfo, error)          // 签名改为返回详情数组
func (v *Virt) CreateSnapshot(domainName, snapName, description string) error    // 新增 description 参数
// 内部：逐个 DomainSnapshotGetXMLDesc 解析 <name>/<description>/<creationTime>/<state>
```

## 克隆 + 云镜像（B2，service/virt/clone.go + cloudinit.go 新建）

```go
// clone.go —— PVE 式 linked clone（父卷在子卷存续期间不可删）
func (v *Virt) CloneVolumeFromVol(poolName, srcVolName, newVolName string) (string, error)
//   实现：StoragePoolLookupByName → StorageVolLookupByName(src) → StorageVolCreateXMLFrom(pool, childXML, src, 0)
//   childXML: <volume><name>newVolName.qcow2</name><capacity unit="G">与父卷同虚拟容量</capacity>
//             <target><format type='qcow2'/></target></volume>
//   libvirt 自动在 child 上写 backing file（StorageVolGetPath 返回路径）
func (v *Virt) CloneVMFromSpec(source *DomainSpec, newName string) (string, error) // 建卷 + BuildDomainXML + DefineDomain；返回新 domain 名

// cloudinit.go —— seed ISO 生成（纯 Go iso9660，禁止调系统工具）
func GenerateSeedISO(cfg *CloudInitSpec) ([]byte, error)
//   iso9660 根目录含：user-data / meta-data / network-config
//   meta-data: instance-id: vmops-<hostname>\nlocal-hostname: <hostname>
//   user-data: #cloud-config + user/password(可选)/ssh_authorized_keys(可选)/hostname
//   network-config: 非必需，dhcp 默认；static 时写 v1 格式
```

## 性能统计（B3，service/virt/stats.go 新建）

```go
type VmStats struct {
    CpuPercent   float64 `json:"cpu_percent"`     // 服务端差分计算
    MemUsedKiB   uint64  `json:"mem_used_kib"`
    MemTotalKiB  uint64  `json:"mem_total_kib"`
    GuestUsedKiB uint64  `json:"guest_used_kib"`  // balloon 口径
    GuestTotalKiB uint64 `json:"guest_total_kib"`
    DiskReadBps  uint64  `json:"disk_read_bps"`
    DiskWriteBps uint64  `json:"disk_write_bps"`
    NetRxBps     uint64  `json:"net_rx_bps"`
    NetTxBps     uint64  `json:"net_tx_bps"`
}
func (v *Virt) GetDomainStats(name string) (*VmStats, error)
// 内部维护 per-domain 滚动缓存（上次 cputime/blockbytes/ifbytes + 时间戳，互斥锁保护），
// 首次调用返回 0 速率；CPU% = Δcputime/(hostCpu*Δt)。hostCpu 用 runtime.NumCPU()。
// 来源：DomainGetInfo + DomainMemoryStats(balloon) + DomainBlockStats(首个磁盘 target)
//        + DomainInterfaceStats(首个网卡 mac)
```

## 网络重建（B3，service/virt/network.go 扩展，不破坏既有函数）

```go
func (v *Virt) UpdateNetwork(name, xml string) error        // 停→net-undefine→net-define→启（编辑用）
// NetworkInfo 增加 Autostart bool `json:"autostart"` 字段（DomainSetAutostart 既有）
// DHCP 范围解析：getNetworkInfo 中解析 <dhcp><range start end>
```

## REST 接口（B4 落地，前端依赖）

统一响应 `{code, message, data}`，错误走 `ErrorResponse/ErrorWithMessage`（AGENTS.md 强制）。

### VM 详情 / 硬件管理
| Method | Path | 说明 |
|---|---|---|
| GET | `/api/vms` | 列表 `{total, items, perf}`（perf 按 VM id 聚合实时 CPU/内存，列表页单请求渲染，无需再调 vm-perf） |
| GET | `/api/vms/:id/spec` | 返回 `{vm, spec}`（spec 含 raw_xml） |
| PUT | `/api/vms/:id/spec` | 整体重 define（body 为完整 DomainSpec，运行时提示关机） |
| POST | `/api/vms/:id/pause` | 暂停 |
| POST | `/api/vms/:id/resume` | 恢复 |
| POST | `/api/vms/:id/devices/disks` | body `{disk: DiskSpec}`，热插拔 |
| DELETE | `/api/vms/:id/devices/disks/:target` | 移除磁盘 |
| POST | `/api/vms/:id/devices/interfaces` | body `{interface: InterfaceSpec}` |
| DELETE | `/api/vms/:id/devices/interfaces/:mac` | 移除网卡 |
| PUT | `/api/vms/:id/cpu` | body `{vcpu}` |
| PUT | `/api/vms/:id/memory` | body `{memory_mb}` |
| PUT | `/api/vms/:id/autostart` | body `{enabled}` |
| PUT | `/api/vms/:id/boot` | body `{devices: []}` |
| GET | `/api/vms/:id/stats` | 性能页轮询，返回 VmStats |

### 创建 / 向导 / 克隆
| Method | Path | 说明 |
|---|---|---|
| GET | `/api/vms/options` | 向导选项：`{pools[], networks[], cloud_images[], os_list[]}` |
| POST | `/api/vms` | **升级**：body `{name, storage_pool, vcpu, memory_mb, disks[], interfaces[], cloud_init?, source_image_id?, source_vm_id?}`；disk 可 `{create_gb}`（新建）或 `{source}`（引用现有卷/镜像）；兼容旧 `iso_path` |
| POST | `/api/vms/:id/clone` | body `{name, storage_pool, vcpu, memory_mb, network}`；基于源 VM 磁盘 linked clone |
| POST | `/api/images/:id/clone` | body `{name, storage_pool, vcpu, memory_mb, network, cloud_init?}`；基于模板/云镜像创建 VM |

### 快照（改）
| Method | Path | 说明 |
|---|---|---|
| GET | `/api/vms/:id/snapshots` | 返回 `SnapshotInfo[]`（含 description/creation_time/state） |
| POST | `/api/vms/:id/snapshots` | body `{name, description?}` |

### 镜像 / 存储池
| Method | Path | 说明 |
|---|---|---|
| POST | `/api/images/upload` | 增加 form 字段 `pool`（默认 `img`）；上传为池卷（StorageVolCreateXML 或文件方式落到池路径）+ DB 记录 |
| PUT | `/api/images/:id/template` | body `{is_template}` 标记模板 |
| GET | `/api/images` | `?is_template=true` 已有 |
| GET | `/api/storage/pools` | 已有（创建向导用） |

### 网络 / 仪表盘 / 审计
| Method | Path | 说明 |
|---|---|---|
| PUT | `/api/networks/:name` | body `{xml}` 编辑网络 |
| GET | `/api/networks` | 已有，NetworkInfo 增 `autostart` |
| GET | `/api/dashboard/host-stats` | 主机实时：`{cpu_percent, mem_total_kib, mem_used_kib}`（读 /proc/stat、/proc/meminfo，或用 virt host info） |
| GET | `/api/dashboard/vm-perf` | 各 VM 实时 `[{name, status, cpu_percent, mem_pct}]`（复用 GetDomainStats） |
| GET | `/api/audit` | 已有；审计 action 补 `pause_vm/resume_vm/clone_vm/attach_disk/detach_disk/attach_nic/detach_nic/create_snapshot` 等映射 |

## 约定与陷阱（子代理必须遵守）

1. 所有 libvirt 调用走 `service/virt`，`getConn()` 开头，错误 `%w` 中文描述注明 virsh 等价命令（AGENTS.md + vmops-libvirt skill）
2. flag 用命名常量；XML 用 encoding/xml
3. 运行中修改：磁盘/网卡 attach/detach 用 `DomainAttachDeviceFlags(dom, xml, LIVE|CONFIG)`（`libvirt.DomainVcpuAffinityLive` 之类在 go-libvirt 对应为 `VIR_DOMAIN_AFFECT_LIVE` 常量，以 `DeviceModifyFlags` 为准，代码注释注明）；SetVcpus/SetMemory 同样 live+config
4. 快照 ListSnapshots 旧签名被前端使用 → B2 改签名，B4 同步改 handler（契约内锁定）
5. 前端文件零重叠：F1=VmDetail.vue（含快照 UI 保留），F2=CreateVmWizard.vue+router+api，F3=Dashboard.vue，F4=AuditList.vue+NetworkList.vue+ImageList.vue+api（部分）