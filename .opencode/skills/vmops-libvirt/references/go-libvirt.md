# digitalocean/go-libvirt 常用 API 速查

连接：`libvirt.ConnectToURI(u *url.URL)`（`libvirt.QEMUSystem` 即 `qemu:///system`）。

## 域（Domain）

| 操作 | API | 备注 |
|------|-----|------|
| 定义 | `DomainDefineXML(xml string)` | 返回 Domain |
| 启动 | `DomainCreate(dom)` | |
| 优雅关机 | `DomainShutdown(dom)` | 发 ACPI 信号 |
| 重启 | `DomainReboot(dom, libvirt.DomainRebootDefault)` | 需运行中 |
| 强关 | `DomainDestroy(dom)` | 立即断电 |
| 删除定义 | `DomainUndefineFlags(dom, flags)` | 需先 destroy；flags 可组合 |
| 查状态 | `DomainGetState(dom, 0) (state int32, reason int32, err)` | 需转 `libvirt.DomainState` |
| 取 XML | `DomainGetXMLDesc(dom, 0)` | |
| 按名查找 | `DomainLookupByName(name) (Domain, error)` | |
| 列出全部 | `ConnectListAllDomains(1, flags)` | flags: Active\|Inactive |
| 打开串口 | `DomainOpenConsoleBidirectional(dom, devOpt, in, out, flags)` | flags: `DomainConsoleForce`；dev 用 `libvirt.OptString{}` |

域状态：`DomainNostate` `DomainRunning` `DomainBlocked` `DomainPaused` `DomainShutdown` `DomainShutoff` `DomainCrashed` `DomainPmsuspended`。

## 存储池（StoragePool）

| 操作 | API |
|------|-----|
| 按名查找 | `StoragePoolLookupByName(name)` |
| 列表 | `ConnectListAllStoragePools(1, flags)`（Active\|Inactive） |
| 定义 | `StoragePoolDefineXML(xml, 0)` |
| 启动/停止 | `StoragePoolCreate(pool, 0)` / `StoragePoolDestroy(pool)` |
| 删除定义 | `StoragePoolUndefine(pool)` |
| 自动启动 | `StoragePoolSetAutostart(pool, 1)` |
| 信息 | `StoragePoolGetInfo(pool) (state, capacity, allocation, available, err)` |
| 活性/持久 | `StoragePoolIsActive(pool)` / `StoragePoolIsPersistent(pool)`（返回 1 为真） |
| XML | `StoragePoolGetXMLDesc(pool, 0)` |
| 卷列表 | `StoragePoolListVolumes(pool, 0)` |

## 存储卷（StorageVol）

| 操作 | API |
|------|-----|
| 创建 | `StorageVolCreateXML(pool, xml, 0)` | 卷 XML 用 `<volume><name/><capacity unit="G"/><target><format type="qcow2"/></target></volume>` |
| 按名查找 | `StorageVolLookupByName(pool, name)` |
| 删除 | `StorageVolDelete(vol, libvirt.StorageVolDeleteNormal)` |
| 路径 | `StorageVolGetPath(vol)` |
| 信息 | `StorageVolGetInfo(vol) (type, capacity, allocation, err)` |

## 快照（DomainSnapshot）

| 操作 | API |
|------|-----|
| 列表 | `DomainSnapshotNum(dom, 0)` + `DomainSnapshotListNames(dom, num, 0)` |
| 创建 | `DomainSnapshotCreateXML(dom, xml, 0)` | 快照 XML 含 `<name>` 与 `<description>` |
| 查找 | `DomainSnapshotLookupByName(dom, name, 0)` |
| 删除 | `DomainSnapshotDelete(snap, libvirt.DomainSnapshotDeleteChildren)` |
| 回滚 | `DomainRevertToSnapshot(snap, 0)` |

## 网络（Network）

| 操作 | API |
|------|-----|
| 列表 | `ConnectListAllNetworks(1, flags)`（Active\|Inactive） |
| 按名查找 | `NetworkLookupByName(name)` |

## 常用 flag 常量

- `ConnectListDomainsActive` / `ConnectListDomainsInactive`
- `ConnectListStoragePoolsActive` / `ConnectListStoragePoolsInactive`
- `DomainUndefineSnapshotsMetadata` / `DomainUndefineManagedSave`
- `StorageVolDeleteNormal`
- `DomainRebootDefault`
- `DomainConsoleForce`（uint32）

## 连接生命周期（service/virt/virt.go 现有实现，勿改模式）

- `New()` 惰性连接，`uri` 固定 `libvirt.QEMUSystem`。
- `Connect()` 带 `sync.Mutex`，已连接直接返回，断线需 `Reset()` 后重建。
- `getConn()` 是业务方法唯一入口；每次调用用 `ConnectGetVersion()` 探活，失败即 `Reset()` 重建连接，自动恢复，无需外部手动 Reset。

## 陷阱

- 域在运行中修改大部分配置需先关机（见 `UpdateDomainXML` 注释）。
- 删除卷时卷不存在视为已删除，不报错（`DeleteVolume` 的写法）。
- `ConnectListAllDomains` 第一参数是 max（传 1 表示全部）。
- 状态取到后必须 `StateToPlatform` 再落库/返回前端。