---
title: "API 契约"
description: "vmops 对齐 virt-manager + PVE 改造：后端 virt 层 Go 函数签名与 REST 接口契约（Wave 1/Wave 2 唯一事实源）"
tags: [契约, virt-manager, PVE, 后端, 前端]
---

# API 契约（唯一事实源）

> 所有子代理（B1/B2/B3/B4/F1-F4）**严格依据本文件**编码。修改本文件需主 agent 确认。

## 0. 统一响应与错误契约

所有接口统一返回 `{code, message, data}`，其中 `code` 与 HTTP 状态码一致：

| 场景 | 产出函数 | 响应体 |
|---|---|---|
| 成功 | `handler.Success` | `{"code":200,"message":"success","data":{...}}` |
| 业务失败 | `handler.Fail` / `ErrorResponse` / `ErrorWithMessage` | `{"code":<状态码>,"message":"中文原因"}`（**不带 `data` 键**） |
| 中间件拦截 | `middleware.abortJSON` | `{"code":<状态码>,"message":"中文原因","data":null}` |

`middleware/jwt.go` 原先返回 `{"error":"..."}`，与全站契约不一致，现已统一为包内 `abortJSON`。
中间件**不能**复用 `handler.Fail`：`handler` 已依赖 `middleware`（`HashPassword`、`GenerateToken` 等），
反向引用会构成导入环，故在 `middleware` 包内单独实现同格式助手。

### 0.1 鉴权与授权错误文案（逐字，前端与测试依赖）

| 状态码 | message | 触发条件 | 产出位置 |
|---|---|---|---|
| 401 | `未提供认证信息` | 无 `Authorization` 头且无 `?token=` | `AuthMiddleware` |
| 401 | `认证格式错误` | 头部不是 `Bearer <token>` 两段式 | `AuthMiddleware` |
| 401 | `Token 无效或已过期` | 验签失败 / 过期 / **签名算法不是 HS256** | `AuthMiddleware` → `ParseToken` |
| 401 | `用户不存在` | token 内 `user_id` 查不到用户 | `AuthMiddleware` |
| 403 | `账号已被禁用` | `users.is_active = false` | `AuthMiddleware` |
| 403 | `需要管理员权限` | 非 admin 访问 admin 组，或 viewer 发起写操作 | `AdminMiddleware` / `OperatorMiddleware` |
| 403 | `只读角色不能使用 SSH 终端与串口控制台，请使用图形控制台查看` | viewer 访问 `/terminal` 或 `/serial` | `OperatorMiddleware` → `isGuestWriteChannel` |

`ParseToken` 已加 `jwt.WithValidMethods([]string{"HS256"})`：header 中 `alg` 为 `none`、`HS512`
等其他取值时直接判为无效（防算法混淆），统一落到 `Token 无效或已过期`。

### 0.2 唯一例外：websockify token 解析（外部契约，不得改动）

`GET /api/vnc/token/:token` 由 websockify 的 JSONTokenApi 插件调用，响应格式由 websockify 规定，
**不走统一响应**，本轮未改动、后续也不得改（改则 noVNC 链路整体失效）：

- 200：`{"host":"127.0.0.1","port":5900}`
- 404：`{"error":"token 无效或已过期"}`

对照：同文件的 `VNCHandler.RequestToken`（`POST /api/vms/:id/vnc-token`）的 404 已从
`gin.H{"error"}` 改为 `Fail`，返回 `{"code":404,"message":"虚拟机不存在"}`。

### 0.3 路径参数错误码（本轮变更）

新增 `handler/param.go`：

```go
func parseID(raw string) (uint, bool)              // 纯函数，不写响应；供 WS 等自定义错误通道使用
func paramID(c *gin.Context, name string) (uint, bool) // 失败时已写好 400 并 Abort，调用方直接 return
```

**为什么必须先解析**：GORM 的 `BuildCondition` 对「非纯数字字符串且未带占位参数」的内联条件，
会把该字符串当作原始 SQL 片段拼进 `WHERE`。因此 `db.First(&vm, c.Param("id"))` 存在 SQL 注入面，
而 `db.First(&vm, id)`（`id` 为 `uint`）走参数化。原实现共 39 处调用点未解析直传，现已全部改造：
37 处普通 HTTP 处理器改用 `paramID`（`vm.go` 26、`host.go` 4、`image.go` 4、`audit.go` 1、`user.go` 1、`vnc.go` 1），
2 处 WebSocket 处理器改用 `parseID`（`terminal.go`、`serial.go`，WS 已升级无法写 JSON 响应，
错误经 WS 帧 `{"type":"error","msg":"虚拟机 ID 非法"}` 回送）。

**对外行为变化**（合法数字 ID 的状态码全部不变）：

| 请求 | 改造前 | 改造后 |
|---|---|---|
| `GET /api/vms/abc` | 404 `虚拟机不存在`（查询后才失败） | **400 `ID 参数非法`**（进 DB 前拦下） |
| `GET /api/vms/1 OR 1=1` | 条件被拼进 SQL | **400 `ID 参数非法`** |
| `GET /api/vms/0` | 404 | **400 `ID 参数非法`**（`parseID` 拒绝 0） |
| `GET /api/vms/99999` | 404 `虚拟机不存在` | 404 `虚拟机不存在`（不变） |

**尚未统一的三处**（早于本轮已自行解析，无注入面，但文案与 `paramID` 不一致，如实记录）：

| 接口 | 非法 ID 响应 | 解析方式 |
|---|---|---|
| `DELETE /api/users/:id` | 400 `无效的用户ID` | `fmt.Sscanf(id, "%d", ...)` |
| `GET`/`DELETE /api/tasks/:id` | 400 `任务 ID 不合法` | `strconv.ParseUint` |
| `POST /api/sessions/:id/disconnect` | 400 `参数错误` | `strconv.ParseUint` |

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

## 克隆 + 云镜像（B2，service/virt/clone.go + cloudinit.go 新建；P2 更正实现）

```go
// clone.go —— PVE 式 linked clone（父卷在子卷存续期间不可删/移动/改写）
func (v *Virt) CloneVolumeFromVol(poolName, srcVolName, newVolName string) (string, error)
//   实现：StoragePoolLookupByName → StorageVolLookupByName(src) → StorageVolGetInfo(取虚拟容量)
//         → StorageVolGetPath(取父盘绝对路径) → StorageVolCreateXML(pool, childXML, 0)
//   childXML 由 storage.go 的 buildVolumeXMLWithBacking(name, format, unit, capacity, backingPath)
//         经 encoding/xml 序列化：
//             <volume><name>newVolName.qcow2</name><capacity unit="B">与父卷同虚拟容量</capacity>
//             <target><format type="qcow2"/></target>
//             <backingStore><path>父盘绝对路径</path><format type="qcow2"/></backingStore></volume>
//   等价 qemu-img create -f qcow2 -F qcow2 -b <父盘> <子盘>；子卷只存写入差异
func (v *Virt) ListBackingRefs(poolName string) (map[string][]string, error)
//   枚举池内所有卷的 <backingStore><path>，返回「父卷路径 → 依赖它的子卷名列表」
//   （对应逐卷 virsh vol-dumpxml）。查询前若池处于 active 则先 StoragePoolRefresh，
//   保证直接落盘的文件也进入卷列表；池内无任何 backing 引用时返回空 map（非 nil）。
//   用途：删卷前的父盘守卫，见 task-contract.md 的 delete_vm。
func (v *Virt) CloneVMFromSpec(source *DomainSpec, newName string) (string, error)
//   建卷 + BuildDomainXML + DefineDomain；返回新 domain 名
//   spec 派生由包内纯函数完成（不触碰 libvirt，可单测）：
//     systemDiskIndex(source) int      —— 首个 device=='disk' 的下标（跳过 cdrom），无则 -1
//     randomMACAddr() (string, error)  —— 52:54:00:xx:xx:xx（KVM 保留前缀）
//     buildCloneSpec(source, newName, diskIdx, newDiskPath, srcDiskPath) (*DomainSpec, error)
//        —— 深拷贝 Disks 与 Interfaces、重生成 UUID、逐块网卡重生成 MAC、清空 RawXML
```

> **⚠️ 契约更正（P2，务必以本节为准）**
>
> 本节此前记载的实现是 `StorageVolCreateXMLFrom(pool, childXML, src, 0)`，并写着「libvirt 自动在 child
> 上写 backing file」——**该描述是错的，且当时的代码确实如此，即「增量克隆」名不副实**。
> `StorageVolCreateXMLFrom`（等价 `virsh vol-clone`）做的是**全量数据拷贝**，产出的子卷没有 backing
> file。已实测确认：`vol-clone` 出来的卷 `qemu-img info` 里没有 `backing file` 行，
> `virsh vol-dumpxml` 里也没有 `<backingStore>` 节点。
> 现改为 `StorageVolCreateXML` + XML 内显式声明 `<backingStore>`，实测子卷 `qemu-img info` 有
> `backing file:`、`vol-dumpxml` 有 `<backingStore>`、`qemu-img check` 报
> `No errors were found on the image`。
>
> 连带的两条契约变化：① 克隆机的**每块网卡 MAC 都必须重新生成**（libvirt 不会自动改 MAC，XML 里显式
> 给了就照用，沿用源机 MAC 会造成同网段 ARP 冲突）；② 删除虚拟机时必须有父盘守卫（父盘一旦被删，
> backing chain 断裂，所有子机磁盘立刻不可读且无法恢复），见 task-contract.md 的 `delete_vm`。

```go
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

## XML 生成（P1：字符串拼接 → encoding/xml）

`service/virt/` 原有 5 处用 `fmt.Sprintf` 拼 libvirt XML，外部可控字段可闭合标签注入任意节点
（池 `name`/`path`、卷 `name`/`format`、网络 `name`/`gateway` 均可达）。现全部改为标准库
`encoding/xml` 结构体 marshal，元字符由标准库自动转义，从根上免疫 XML 注入：

| 函数 | 文件 | 生成物 | 序列化结构体 |
|---|---|---|---|
| `CreateVolume` | `storage.go` | `<volume>`（unit=G） | `buildVolumeXML` → `volumeXML` |
| `CreateVolumeCustom` | `storage.go` | `<volume>`（自定义 format） | 同上 |
| `CreateDirPool` | `storage.go` | `<pool type="dir">` | `buildDirPoolXML` → `poolXML` |
| `CloneVolumeFromVol` | `clone.go` | `<volume>`（unit=B，与父卷等容量，**含 `<backingStore>`**） | `buildVolumeXMLWithBacking` → `volumeXML` + `volBackingXML` |
| `NetworkXMLFromParams` | `network.go` | `<network>`（NAT 模板） | `networkXML` + `netForwardXML/netBridgeXML/netIPXML/netDhcpXML` |

固定取值改用命名常量（`volUnitGiB`/`volUnitByte`/`volFormatQcow2`/`poolTypeDir`、
`natForwardMode`/`natPortStart`/`natPortEnd`/`natNetmask`/`natDefaultGW`/`bridgeSTP`/`bridgeDelay`）。
`buildVolumeXML(name, format, unit, capacity)` 是 `buildVolumeXMLWithBacking(..., backingPath="")` 的薄封装，
`volumeXML.BackingStore` 为 `*volBackingXML` 且带 `omitempty`，因此非克隆场景不会多出空节点。
`NetworkXMLFromParams` 保持 `string` 返回值（调用方签名不变）：结构体只含字符串与整数字段，
`xml.Marshal` 不会失败，兜底返回空串由 `DefineNetwork` 报「定义网络失败」，绝不产出半截 XML。

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
| POST | `/api/vms/:id/clone` | body `{name, storage_pool, vcpu, memory_mb, network}`；基于源 VM 系统盘做增量克隆（子盘 `<backingStore>` 指向源盘），新机 UUID 与**全部网卡 MAC** 重新生成 |
| POST | `/api/images/:id/clone` | body `{name, storage_pool, vcpu, memory_mb, network, cloud_init?}`；基于模板/云镜像创建 VM（同为增量克隆） |
| POST | `/api/vms/import` | body `{host_id?, names: []}`（`host_id` 缺省取首台宿主机；`names` 为空返回 400 `请选择要导入的虚拟机`）；只写 DB 不改 libvirt。响应 `{imported, skipped, failed, errors[]}`，`errors` 元素逐字为 `"<域名>: 写入数据库失败"`（原始 GORM/SQL 错误只进服务端日志）。**注：该数组前端 `VmList.vue` 目前未消费，见「已知未处理项」** |

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
| PUT | `/api/networks/:name/autostart` | **新增（UX 批次）** body `{autostart: bool}`，对应 `virsh net-autostart on\|off` |
| GET | `/api/dashboard/host-stats` | 主机实时：`{cpu_percent, mem_total_kib, mem_used_kib}`（读 /proc/stat、/proc/meminfo，或用 virt host info） |
| GET | `/api/dashboard/vm-perf` | 各 VM 实时 `[{name, status, cpu_percent, mem_pct}]`（复用 GetDomainStats） |
| GET | `/api/audit` | 已有；审计 action 补 `pause_vm/resume_vm/clone_vm/attach_disk/detach_disk/attach_nic/detach_nic/create_snapshot` 等映射 |

### 用户 / 监控 / 设置 / 会话与任务分页（UX 批次新增）

**已下线的旧接口（勿再调用，路由已删，请求会落到 SPA 兜底返回 HTML）**：
`GET /api/host`（GetHostInfo，被 /api/dashboard/host-stats + host-history 取代）、
`GET /api/vms/:id/detail`（GetVMDetail 聚合接口，被 /spec + /stats + stats-history 取代）。
前端 api/index.js 同步删除死封装 getVMDetail/updateVMSpec/getImage（PUT /vms/:id/spec 后端保留，属文档化能力）。

| Method | Path | 角色 | 说明 |
|---|---|---|---|
| GET/POST | `/api/users` | admin | 用户管理（后端原有，本批接前端 Users.vue）；CreateUser 新增角色白名单（admin/operator/viewer，否则 400「角色不合法，仅支持 admin/operator/viewer」）与密码 ≥6 位校验 |
| PUT/DELETE | `/api/users/:id` | admin | UpdateUser 同样校验角色白名单；DeleteUser 改走 `paramID`（修掉 `fmt.Sscanf` 注入面） |
| GET | `/api/monitor/alerts` | 登录即可 | **新增** 代理 Alertmanager `GET /api/v2/alerts`，原样透传 JSON 数组；地址来自 env `ALERTMANAGER_URL`（默认 `http://127.0.0.1:9093`，compose 内 `http://alertmanager:9093`）；AM 不可达返回 502 |
| GET | `/api/settings` | admin | 快照新增 `writable` 节（三个可写项当前值，未设置为默认值）；`tasks.workers/queue_buffer` 改读真实常量（原硬编码占位） |
| PUT | `/api/settings` | admin | **新增** body 三字段全可选：`default_storage_pool`（1-64 位 `A-Za-z0-9_.-`）、`vnc_token_ttl_min`（1-60）、`vnc_stale_min`（5-1440）；写 `system_settings` 表，消费方实时读取，**保存即生效无需重启**；非法值 400 逐字文案如「VNC token 有效期 必须是 1-60 的整数」 |
| GET | `/api/storage/pools/:name/volume-refs` | operator+ | **新增** 池级卷引用：`{pool, refs: {卷名: {vms[], images[], children[]}}}`（vms=挂载该卷的域名、images=镜像库登记、children=backing 子卷） |
| GET | `/api/sessions` | 已有 | 新增 query：`type`（vnc/ssh/serial）、`vm_name`/`username`（LIKE）、`page`/`page_size`（真分页，`total` 为 Count 真值；旧 `limit` 语义由 page_size 承接） |
| GET | `/api/tasks` | 已有 | 新增 `page`/`page_size` 真分页（`total`=Count 真值）；旧 `limit` 参数兼容（等价 page_size） |

**删卷守卫（UX 批次）**：`DELETE /api/storage/pools/:name/volumes/:vol` 不再裸删。删除前计算该卷引用
（`ListAllDomainDiskSources` 域磁盘挂载 + `images.path` 精确匹配 + `ListBackingRefs` backing 子卷），
任一命中返回 **409**，文案逐字格式：
`卷 <名> 正在使用中，已阻止删除: 仍被虚拟机挂载（<vm1>、<vm2>）…；已登记为镜像库镜像（<img>）…；是增量克隆父盘，仍被 N 个子卷依赖（…）`
——与 `tasks.execDeleteVM` 的 `shouldKeepVol` 三重守卫同一立场。

**系统设置消费点**（改配置即生效，勿回退成硬编码）：
`default_storage_pool` → `tasks.DefaultStoragePoolResolver`（原 `defaultStoragePool` 常量）；
`vnc_token_ttl_min` → `vnc.TTLResolver`（原 token.go 硬编码 5min）；
`vnc_stale_min` → `console.StaleAfterResolver`（原 registry.go 硬编码 60min）。
三个 resolver 均有未接线兜底默认值，测试依赖这一行为。 |

### 控制台三入口与只读角色（P1 变更）

| Method | Path | 角色 | 说明 |
|---|---|---|---|
| POST | `/api/vms/:id/vnc-token` | admin ✓ / viewer ✓ | 响应**新增 `view_only` 布尔字段**，见下 |
| GET | `/api/vms/:id/terminal` | admin ✓ / viewer ✗ **403** | WS→SSH 桥；首帧 `{"type":"auth",host,port,user,password}` |
| GET | `/api/vms/:id/serial` | admin ✓ / viewer ✗ **403** | WS→libvirt 串口桥 |

`POST /api/vms/:id/vnc-token` 成功响应：

```json
{"code":200,"message":"success","data":{
  "token":"<一次性 token>","host":"127.0.0.1","port":5900,
  "view_only":false
}}
```

- `view_only`：`role != "admin"` 即为 `true`（admin=false / viewer=true）。
- 前端据此拼 noVNC URL：`view_only=true` 时追加 `&view_only=1`，并在顶栏显示「只读观看（键鼠已禁用）」标签。
- **必要性**：VNC 协议本身没有只读模式，键鼠输入只能在客户端侧关闭，否则「只读角色」名不副实。

`/terminal` 与 `/serial` 虽为 GET，但建立的是对 guest 的**双向写入**通道
（`/terminal` 是 SSH shell，`/serial` 直连虚拟机串口，多数云镜像上即 root TTY），
与「只读」语义冲突，因此对 viewer 关闭。判定函数为 `middleware/jwt.go` 的 `isGuestWriteChannel`
（按 `c.FullPath()` 后缀匹配），**不能只靠 HTTP 方法判断读写语义**——浏览器 WebSocket 只能发 GET。

### 输入校验（P1 新增，逐字文案）

| 接口 | 字段 | 规则 | 违反时（400） |
|---|---|---|---|
| POST `/api/storage/pools` | `path` | 规范绝对路径：`^/[a-zA-Z0-9_./-]+$`、`filepath.Clean(p)==p`、不含 `..`、无结尾斜杠、不为 `/` | `存储池路径必须是规范的绝对路径（不含 ..、结尾斜杠），且不能是根目录` |
| POST `/api/storage/pools/:name/volumes` | `format` | 白名单 `qcow2` / `raw`；空值仍走默认（qcow2） | `卷格式只支持 qcow2 或 raw` |
| POST `/api/networks` | `gateway` | 合法 IPv4 且为规范写法（`ip.String()==输入`，排除 IPv6 与 `::ffff:` 映射）；空值仍走默认 `192.168.100.1` | `网关必须是合法的 IPv4 地址` |
| POST `/api/hosts`、PUT `/api/hosts/:id` | `ssh_ip` | `net.ParseIP` 通过，或匹配保守主机名白名单 `^[a-zA-Z0-9]([a-zA-Z0-9.-]*[a-zA-Z0-9])?$` 且长度 ≤253 | `SSH 地址格式不合法，只能是 IP 或主机名` |

`ssh_ip` 的校验动机：该值会作为 argv 直接传给 `exec.Command("ping", ..., host.SSHIP)`（`TestHost`），
以连字符开头的串（如 `-f`）会被 ping 当成选项解析（flood ping），必须拦在写库入口。

存储池 `path`、卷 `format`、网络 `gateway` 的校验与 virt 层的 `encoding/xml` 序列化构成**纵深防御**：
handler 侧限制取值域，virt 侧保证元字符转义。

### Web 终端 SSH 目标校验（`handler/terminal.go` 的 `validateSSHTarget`）

首帧 `auth` 的 `host`/`port` 完全来自浏览器，原实现零校验，等于把平台变成跳板机 / 内网端口扫描器 /
口令爆破器。现按「约束由强到弱」收敛，失败原因经 WS 错误帧 `{"type":"error","msg":"<中文>"}` 回送：

| 前置条件 | 规则 | 拒绝文案 |
|---|---|---|
| 任意 | `port` 须在 1-65535 | `端口不合法（须在 1-65535 之间）` |
| `vms.ip` 非空 | `host` 必须与之精确一致 | `只能连接该虚拟机自身地址 <ip>` |
| `vms.ip` 为空 | 必须是 IP 字面量（不接受主机名，防 DNS 解析到公网与 DNS rebinding） | `目标必须是 IP 地址（不支持主机名）` |
| 同上 | 排除环回 / 未指定 / 链路本地（单播与组播）/ 组播 | `该地址不允许作为终端目标（环回 / 链路本地 / 组播）` |
| 同上 | 必须是 RFC1918 私有网段（`net.IP.IsPrivate`） | `只允许连接私有网段地址（10/8、172.16/12、192.168/16）` |

拨号前后均写服务端日志（`目标被拒` / `SSH 拨号` / `SSH 拨号失败`，含 vm、target、user、来源 IP，**不记口令**）。
`HostKeyCallback` 显式为 `ssh.InsecureIgnoreHostKey()`：目标是平台自建的短生命周期 VM，
IP 由 DHCP 动态分配、重建即换主机密钥，维护 known_hosts 不具可操作性；中间人风险由上表的目标白名单收敛。

## 状态字面量（P2 收口为常量）

`vms.status` 与 virt 层状态映射的合法取值只有四个，注意 `shut off` 是**空格**不是下划线：

| 取值 | `model` 常量（业务层） | `virt` 常量（封装层） | libvirt 域状态 |
|---|---|---|---|
| `running` | `model.VMStatusRunning` | `virt.StatusRunning` | `DomainRunning` |
| `shut off` | `model.VMStatusShutOff` | `virt.StatusShutOff` | `DomainShutoff` / `DomainShutdown` |
| `paused` | `model.VMStatusPaused` | `virt.StatusPaused` | `DomainPaused` |
| `error` | `model.VMStatusError` | `virt.StatusError` | `Nostate`/`Blocked`/`Crashed`/`Pmsuspended` 及 default |

- 两处**各自定义**而非共享一份：`virt` 是最底层的 libvirt 封装层，反向 import `model` 会把 GORM 拖进封装层并倒置分层依赖。两边注释交叉引用，改一处必须同步另一处。
- `model.VM.Status` 的 gorm tag 默认值原为 `shut_off`（下划线），与 `StateToPlatform` 的返回值及前端映射都不一致，P2 已改为 `shut off`。gorm tag 内不能引用常量，该字面量须与 `VMStatusShutOff` 手工保持一致。
- 实测数据库中**没有** `shut_off` 存量行（所有写入路径都显式给值，从未落到列默认值）；GORM `AutoMigrate` 能把已有列的 default 改过来，后端下次重启即收敛。
- `handler/vm.go` 的 8 处字面量已改引用常量；`service/tasks/vm_tasks.go` 尚有 5 处未换，见「已知未处理项」。

## 约定与陷阱（子代理必须遵守）

1. 所有 libvirt 调用走 `service/virt`，`getConn()` 开头，错误 `%w` 中文描述注明 virsh 等价命令（AGENTS.md + vmops-libvirt skill）
2. flag 用命名常量；XML 用 encoding/xml
3. 运行中修改：磁盘/网卡 attach/detach 用 `DomainAttachDeviceFlags(dom, xml, LIVE|CONFIG)`（`libvirt.DomainVcpuAffinityLive` 之类在 go-libvirt 对应为 `VIR_DOMAIN_AFFECT_LIVE` 常量，以 `DeviceModifyFlags` 为准，代码注释注明）；SetVcpus/SetMemory 同样 live+config
4. 快照 ListSnapshots 旧签名被前端使用 → B2 改签名，B4 同步改 handler（契约内锁定）
5. 前端文件零重叠：F1=VmDetail.vue（含快照 UI 保留），F2=CreateVmWizard.vue+router+api，F3=Dashboard.vue，F4=AuditList.vue+NetworkList.vue+ImageList.vue+api（部分）
6. **路径参数中的数值主键必须经 `paramID`/`parseID` 解析后再交给 GORM**，禁止直传 `c.Param`（见 0.3 节）
7. **WebSocket 写入必须经 `console.Conn`**，禁止直接对 `*websocket.Conn` 并发写（gorilla/websocket 明确禁止多 goroutine 同时写，违反即 panic 且 gin Recovery 拦不住）
8. **后台 goroutine（worker / 清扫器 / WS 转发）必须自带 `defer recover()`**，gin Recovery 只覆盖 HTTP 请求链
9. **状态字面量用常量**（`model.VMStatus*` / `virt.Status*`），禁止散落 `"running"`/`"shut off"` 字符串（见上节）
10. **增量克隆只能靠卷 XML 的 `<backingStore>`**（`buildVolumeXMLWithBacking`），禁止用 `StorageVolCreateXMLFrom` 冒充——后者是全量拷贝，产出的子卷没有 backing file
11. **删卷前必须过三重守卫**（池外 / 镜像库登记 / 仍被子卷 backing 依赖），见 task-contract.md 的 `delete_vm`

## 已知未处理项（如实记录，勿在文档中宣称已解决）

| 项 | 现状 | 影响面 |
|---|---|---|
| `POST /api/networks/xml`、`PUT /api/networks/:name` | 接受调用方原始 XML 直接 `net-define`，未做结构校验 | admin 可定义任意 libvirt 网络（viewer 已被 403 拦住） |
| `PUT /api/vms/:id/xml` | 同上，接受原始 domain XML | 同上 |
| `GET /metrics` | 公开无鉴权（Prometheus 抓取需要） | 泄漏 VM 名与资源指标，需靠防火墙限制来源 |
| `POST /api/auth/login` | 无失败次数限流 / 验证码 | 可离线爆破弱口令 |
| CORS | `CORS_ORIGINS` 默认 `*` | 生产需收敛为具体来源 |
| Web 终端 `HostKeyCallback` | `InsecureIgnoreHostKey()` | 目标已限定私有网段，残余中间人风险 |
| `golangci-lint` | 本机未安装，`.golangci.yml` 已就位但深度 lint 未执行；且该配置为 v1 schema，装 v2.x 会因字段改名（`linters-settings` → `linters.settings` 等）报错 | 静态检查覆盖不完整（`go build`/`go vet`/`gofmt` 已过） |
| 孤儿卷 | 「先删父机、再删子机」顺序下，被父盘守卫保留的卷会残留为无人引用的孤儿文件，无自动清理入口 | 占存储空间。刻意取舍：宁可留垃圾文件也不能损坏在用磁盘 |
| 状态字面量 | `service/tasks/vm_tasks.go` 仍有 5 处 `"shut off"` 字面量未换成 `model.VMStatusShutOff` | 一致性隐患，当前行为正确 |
| `POST /api/vms/import` 的 `errors` | 后端已按「域名 + 中文原因」返回，但前端 `VmList.vue` 只读 `imported`/`skipped`/`failed` | 单台导入失败时用户看不到具体原因 |
| 多宿主机 | `hosts.libvirt_uri` 已入库、已在设置页展示，但**从未用于建立连接**；`virt.New()` 固定 `libvirt.QEMUSystem`（`qemu:///system`） | 多宿主机纳管目前是空壳，只有运行后端的这台机器真实可管 |
