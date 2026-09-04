---
name: vmops-libvirt
description: 在 vmops 项目中操作 libvirt 时的编码规范与模式。项目使用 digitalocean/go-libvirt 通过 service/virt 包封装 KVM 能力，当涉及虚拟机生命周期、存储池/卷、网络、快照、控制台、域 XML 等 libvirt 相关代码时使用。本技能固化 service/virt 的既有写法，新增或修改 virt 层代码必须遵循。
---

# vmops 项目 libvirt 编码规范

本项目通过 `service/virt` 包统一封装 `digitalocean/go-libvirt`（底层 unix socket 直连 `qemu:///system`，无 CGO）。**所有 libvirt 调用必须走 `service/virt` 封装层，禁止在 handler/service 里直接 new `libvirt.Libvirt`。**

## 核心约定（必须遵守）

1. **连接获取**：方法开头一律 `l, err := v.getConn()`，拿到 `*libvirt.Libvirt`。禁止直接 `ConnectToURI`。
2. **错误包装（必须用 %w，不要用 %v）**：virt 层属于内部模块，所有 libvirt 错误用 `fmt.Errorf("中文描述: %w", err)` 保留错误链，handler 层才能用 `errors.Is`/`errors.As` 判断错误类型。`%v` 只在 handler（HTTP 边界）把内部错误翻译成用户友好文案时使用。描述里注明对应的 `virsh` 等价命令（如"对应 virsh destroy"）。区分：
   - 资源不存在 → `fmt.Errorf("虚拟机 %s 不存在: %w", name, err)`
   - 状态不符合 → 明确提示（如"未运行，无法连接控制台"）
   - 禁止把原始 libvirt 错误原样返回给前端，handler 层需翻译为用户友好信息
3. **按名查找**：域操作先 `l.DomainLookupByName(name)`；存储池先 `l.StoragePoolLookupByName(name)`；卷先 `l.StorageVolLookupByName(pool, name)`；快照先 `l.DomainSnapshotLookupByName(dom, snapName, 0)`。
4. **flag 用命名常量**：如 `libvirt.DomainUndefineSnapshotsMetadata`、`libvirt.ConnectListDomainsActive`、`libvirt.StorageVolDeleteNormal`、`libvirt.DomainRebootDefault`、`libvirt.DomainConsoleForce`，禁止硬编码数字。
5. **状态映射**：返回平台状态必须经 `StateToPlatform(state)` 转换，只允许 `running / shut off / paused / error` 四种，与 DB `vms.status` 取值保持一致。
6. **XML 处理**：用标准库 `encoding/xml` + 匿名 struct 解析/生成（参考 `GetVNCInfo`、`getPoolInfo` 的写法）。不引第三方 XML 库。
7. **注释语言**：方法与字段注释用中文，注明 virsh 等价操作。
8. **存储卷命名**：`CreateVolume` 的 `diskName` 不含扩展名，函数内部自动补 `.qcow2`。

## 方法模板（照此写新方法）

```go
// XxxXxx 描述（对应 virsh xxx）。
func (v *Virt) XxxXxx(name string) error {
	l, err := v.getConn()
	if err != nil {
		return err
	}
	dom, err := l.DomainLookupByName(name)
	if err != nil {
		return fmt.Errorf("虚拟机 %s 不存在: %w", name, err)
	}

	flags := libvirt.SomeFlag
	if err := l.SomeCall(dom, flags); err != nil {
		return fmt.Errorf("操作失败: %w", err)
	}
	return nil
}
```

## handler 边界错误响应（必须遵守）

handler 层是系统边界，禁止把内部错误原样返回前端。统一走 `handler` 包提供的助手：

- `ErrorResponse(c, status, err)` — 完整错误写日志，前端只收到从 `"中文: <细节>"` 提取的友好消息
- `ErrorWithMessage(c, status, "中文文案", err)` — 调用方已给定用户友好文案时用；err 只进日志
- **禁止** `c.JSON(status, gin.H{"error": err.Error()})` 或 `"detail": err.Error()`，这是已修复的泄漏模式
- 完整错误经 `LogError` 写入 `[handler] 请求失败 path=... err=...` 日志，供排查
- 唯一例外：交互式控制台 WS 消息（`terminal.go`/`serial.go` 的 SSH/串口错误）保留细节，运维排错需要

新增/修改 handler 错误分支时，参照 `handler/response.go` 的 `friendlyMessage`（提取首个冒号前的中文段，无中文回退"操作失败"）。

## 状态字段与 DB 一致性

- `StateToPlatform` 中 `DomainShutdown` 也归为 `"shut off"`（见 `service/virt/state.go:18`）。
- 新增状态场景时必须经 `StateToPlatform`，前端状态色/文案依赖这四值。

## 现有文件职责速查

| 文件 | 职责 |
|------|------|
| `virt.go` | 连接生命周期：`New`/`Connect`（惰性+锁）/`Reset`/`getConn`（每次探活，断线自动重建） |
| `domain.go` | 域生命周期：define/start/shutdown/reboot/destroy/undefine/状态/XML |
| `state.go` | `StateToPlatform` 状态枚举→平台字符串 |
| `storage.go` | 存储池/卷：create/delete/list/info、目录池 |
| `network.go` | 网络 list/info |
| `snapshot.go` | 快照 list/create/delete/revert |
| `console.go` | 串口控制台 `DomainOpenConsoleBidirectional` |
| `import.go` | 存量域导入（只读 DB 记录，不改 libvirt 侧） |

## 参考

- 关键 API 速查见 `references/go-libvirt.md`。
- 修改前先读对应文件了解既有写法，保持风格一致。