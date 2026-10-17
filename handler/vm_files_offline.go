package handler

// v2 大升级批次 2：VM 文件管理的「离线挂载」通道。
// 对关机虚拟机，用 guestmount 把系统盘以只读方式挂到本机临时目录，再走普通文件 API 浏览/下载——
// 不依赖 VM 内 SSH 服务（与 vm_files.go 的 SSH 在线通道互补：在线管开机机，离线管关机机）。
//
// 权限现实：libvirt 卷属 root，web 进程用户读不了，因此 guestmount 经 sudo -n 调用
// （宿主机已配置免密 sudo）；磁盘若被运行中 VM 持有会因写锁失败——运行中的 VM 一律拒绝挂载。
//
// 挂载为 --ro 只读：宁可浏览受限，不可损坏磁盘（与删卷守卫同一立场）。
//
// 生命周期守卫（见 StartOfflineMountGuard）：offlineMounts 只是进程内存态，进程一旦被
// kill -9 / 崩溃 / 容器重建，宿主上会残留 guestmount 的 FUSE 挂载点并继续占用 VM 磁盘，
// 该 VM 的启动/迁移/删除全线失败且原因不明，只能人工上机 fusermount -u。因此对账发生在
// 两处：① 服务启动时扫描挂载基目录清理上次进程遗留；② 进程收到 SIGINT/SIGTERM 时统一卸载。
//
// 本文件（vm_files_offline.go）管挂载/卸载与状态登记；文件浏览/写入等操作端点见
// vm_files_offline_ops.go，进程生命周期守卫见 vm_files_offline_guard.go。

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/virt"
)

// offlineMountBase 离线挂载点根目录。放 /tmp 是刻意的：进程重启后由启动守卫
// 统一清理残留（见 vm_files_offline_guard.go），不污染持久化数据目录。
const offlineMountBase = "/tmp/vmops-mounts"

// offlineMount 一次离线挂载的进程内登记信息。
//   - Mountpoint：guestmount 的 FUSE 挂载点（卸载时传给 fusermount -u）
//   - Filesystem：展示用文案「设备（文件系统类型）」，前端直接显示
//   - Disk：被挂载的磁盘路径（排障/日志用）
//   - ReadWrite：true=读写挂载。只有 admin 能开，且挂载期间 StartVM 会被守卫拒绝
//   - MountedAt：挂载时间，便于前端展示与排障
type offlineMount struct {
	Mountpoint string
	Filesystem string
	Disk       string
	ReadWrite  bool // true = 读写挂载（仅 admin 可开；挂载期间禁止开机）
	MountedAt  time.Time
}

// offlineMounts 是进程内存态，不落库：它描述的是「本进程当前持有的 FUSE 挂载」，
// 进程重启后宿主上的残留由启动守卫扫描 /tmp/vmops-mounts 清理，无需持久化。
// 所有读写都必须持 offlineMu（HTTP 处理与开机守卫会并发访问）。
var (
	offlineMu     sync.Mutex
	offlineMounts = map[uint]*offlineMount{} // vmID → 挂载信息（进程重启即失，重新挂载即可）
)

// sudoRun 以 sudo -n 执行 guestmount 族命令（免密 sudo；-n 禁止交互挂死）。
// 关键点：-n 让 sudo 在需要口令时立即失败而不是阻塞等待 stdin——Web 请求里
// 没有终端可输入，否则会挂住整个 goroutine 直到超时。
// 输出（stdout+stderr）与错误一起返回：guestmount/guestfish 的失败原因通常在 stderr，
// 拼进错误链后由 handler 的 ErrorWithMessage 记日志，前端只看到中文概述。
func sudoRun(timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sudo", append([]string{"-n"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return "", fmt.Errorf("%s: %w", msg, err)
		}
		return "", fmt.Errorf("sudo %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

// offlineVM 离线通道取 VM：paramID → DB → vmVisible。未授权与不存在同响应。
// 路径里的 :id 必须先经 paramID 参数化（AGENTS.md 后端标准第 8 条），
// 否则非数字字符串会被 GORM 当作原始 SQL 片段拼进 WHERE。
// 不可见时返回 404 而不是 403：不向无权限用户泄露资产存在性（与 vmVisible 的语义一致）。
func (h *VMFilesHandler) offlineVM(c *gin.Context) *model.VM {
	id, ok := paramID(c, "id")
	if !ok {
		return nil
	}
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		Fail(c, http.StatusNotFound, "虚拟机不存在")
		return nil
	}
	if !vmVisible(c, h.DB, vm.ID) {
		Fail(c, http.StatusNotFound, "虚拟机不存在")
		return nil
	}
	return &vm
}

// listFS guestfish（stdin 喂脚本）列出磁盘内文件系统，输出形如 "device: fstype" 每行一条。
// 用 guestfish 而不是直接 mount：libguestfs 在用户态启动一个隔离的 appliance VM 读盘，
// 不会让宿主内核去解析不可信的文件系统镜像。--ro 保证探测阶段也绝不写盘。
// 脚本 "run" 启动 appliance，"list-filesystems" 枚举分区/卷上的文件系统。
func listFS(disk string) ([][2]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sudo", "-n", "guestfish", "--ro", "-a", disk)
	cmd.Stdin = strings.NewReader("run\nlist-filesystems\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}
	var fss [][2]string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, ":") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		fss = append(fss, [2]string{strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])})
	}
	if len(fss) == 0 {
		return nil, fmt.Errorf("磁盘内未发现任何文件系统")
	}
	return fss, nil
}

// pickRootFS 挑根分区：优先 LVM 卷（命名带 vg-lv 形态，通常承载根），其次普通 ext4/xfs。
// 第一轮偏好 LVM/mapper 路径（/dev/mapper/vg-lv 或层级较深的设备路径），因为
// 云镜像/发行版默认安装常把根放在 LVM 上；找不到再退回任意 ext4/xfs。
// 这是启发式而非权威判定——多分区磁盘可能挑错，但离线浏览场景下比「拒绝挂载」可用。
func pickRootFS(fss [][2]string) (device, fstype string, err error) {
	for _, fs := range fss {
		if (fs[1] == "ext4" || fs[1] == "xfs" || fs[1] == "btrfs") &&
			strings.Contains(fs[0], "/dev/") &&
			(strings.Contains(fs[0], "/dev/mapper/") || strings.Count(fs[0], "/") >= 3) {
			return fs[0], fs[1], nil
		}
	}
	for _, fs := range fss {
		if fs[1] == "ext4" || fs[1] == "xfs" {
			return fs[0], fs[1], nil
		}
	}
	return "", "", fmt.Errorf("磁盘内未找到可挂载的 Linux 文件系统")
}

// OfflineMount POST /api/vms/:id/files/offline/mount
// 对关机 VM 自动挑系统盘与根文件系统并只读挂载；重复调用幂等（已挂载直接返回）。
// 权限：能看见该 VM 的用户即可挂载（vmVisible 已在 offlineVM 内校验）；
// 读写挂载额外要求 admin（见下方 rw 分支）。
func (h *VMFilesHandler) OfflineMount(c *gin.Context) {
	vm := h.offlineVM(c)
	if vm == nil {
		return
	}

	// 运行中的 VM 一律拒绝：qemu 已持有磁盘写锁，guestmount 会失败；
	// 即便绕过，双写同一块盘也可能直接损坏文件系统。
	// 查询失败不阻断（err != nil 时继续）：后续 guestmount 若真的冲突会以明确错误返回，
	// 这里只拦截「能确认处于运行中」的情况，避免因 libvirt 短暂抖动误伤。
	state, err := h.Virt.GetDomainState(vm.Name)
	if err == nil && state != virt.StatusShutOff {
		Fail(c, http.StatusConflict, "虚拟机运行中，请先关机再挂载磁盘浏览（运行中的磁盘被 qemu 锁定且写操作不安全）")
		return
	}

	// 幂等：同一 VM 已挂载则直接复用，不重复 guestmount（重复挂载会叠出多层 FUSE）
	offlineMu.Lock()
	if m, ok := offlineMounts[vm.ID]; ok {
		offlineMu.Unlock()
		Success(c, gin.H{"mountpoint": m.Mountpoint, "filesystem": m.Filesystem, "items": listRoot(m.Mountpoint), "message": "已处于挂载状态"})
		return
	}
	offlineMu.Unlock()

	spec, err := h.Virt.GetDomainSpec(vm.Name)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	// 取第一块 device=disk 的盘作为系统盘：平台创建/导入的 VM 都只有一块系统盘，
	// 多盘场景下这里不保证挑中根盘，但至少行为可预期（按 XML 顺序）。
	disk := ""
	for _, d := range spec.Disks {
		if d.Device == "disk" && d.Source != "" {
			disk = d.Source
			break
		}
	}
	if disk == "" {
		Fail(c, http.StatusBadRequest, "虚拟机没有可挂载的系统盘")
		return
	}

	fss, err := listFS(disk)
	if err != nil {
		// guestfish 首次运行要构建/启动 libguestfs appliance，可能耗时数分钟；
		// 文案提前告知「可重试」，避免用户误判为永久故障
		ErrorWithMessage(c, http.StatusInternalServerError, "检查磁盘文件系统失败（首次运行需构建 libguestfs 环境，耗时较长，可重试）", err)
		return
	}
	device, fstype, err := pickRootFS(fss)
	if err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}

	// 读写挂载（?rw=1，仅 admin）：用于离线改模板/系统文件。默认只读——宁可浏览受限，
	// 不可损坏磁盘。读写挂载期间**严禁开机**（qemu 与 FUSE 同时写一块盘 = 损坏），StartVM 有守卫。
	rw := c.Query("rw") == "1" || c.Query("rw") == "true"
	if rw && !roleIsAdmin(c) {
		Fail(c, http.StatusForbidden, "读写挂载仅管理员可用")
		return
	}
	// 挂载点按 VM ID 隔离；MkdirAll 失败也无妨（guestmount 自己会创建），
	// 因此不因目录问题提前失败。
	mp := filepath.Join(offlineMountBase, fmt.Sprint(vm.ID))
	_ = os.MkdirAll(mp, 0755)
	// 参数顺序即 guestmount 语义：-a 磁盘 -m 设备 [--ro] 挂载点
	args := []string{"-a", disk, "-m", device}
	if !rw {
		args = append(args, "--ro")
	}
	args = append(args, mp)
	if _, err := sudoRun(3*time.Minute, append([]string{"guestmount"}, args...)...); err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "挂载磁盘失败", err)
		return
	}
	// 挂载成功后才登记：登记表里的条目必须对应宿主上真实存在的挂载，
	// 否则卸载/开机守卫会对着不存在的挂载点操作。
	offlineMu.Lock()
	offlineMounts[vm.ID] = &offlineMount{Mountpoint: mp, Filesystem: device + "（" + fstype + "）", Disk: disk, ReadWrite: rw, MountedAt: time.Now()}
	offlineMu.Unlock()
	Success(c, gin.H{"mountpoint": mp, "filesystem": device + "（" + fstype + "）", "read_write": rw, "items": listRoot(mp)})
}

// mountedReadWrite 该 VM 当前是否有活动的读写离线挂载（开机守卫用）。
// 只读挂载不阻止开机；读写挂载必须阻止——否则 qemu 与 FUSE 同时写同一块盘会损坏数据。
func mountedReadWrite(vmID uint) bool {
	offlineMu.Lock()
	defer offlineMu.Unlock()
	m, ok := offlineMounts[vmID]
	return ok && m.ReadWrite
}

// OfflineUnmount POST /api/vms/:id/files/offline/unmount
// 卸载该 VM 的离线挂载。先摘登记再执行 fusermount：若卸载失败会把登记回填，
// 保证「登记表存在 ⇔ 宿主上可能仍挂着」这一不变式，进程退出钩子不会漏扫。
func (h *VMFilesHandler) OfflineUnmount(c *gin.Context) {
	vm := h.offlineVM(c)
	if vm == nil {
		return
	}
	offlineMu.Lock()
	m, ok := offlineMounts[vm.ID]
	if !ok {
		offlineMu.Unlock()
		Fail(c, http.StatusNotFound, "该虚拟机没有已挂载的磁盘")
		return
	}
	// 先在锁内摘除：避免与并发的开机守卫/重复卸载请求看到半完成状态
	delete(offlineMounts, vm.ID)
	offlineMu.Unlock()
	// fusermount -u 只需 30s：它是本地卸载，不像 guestmount 需要启动 appliance
	if _, err := sudoRun(30*time.Second, "fusermount", "-u", m.Mountpoint); err != nil {
		// 卸载失败必须回填登记表：宿主上还挂着，进程却以为已卸，退出钩子就再也扫不到它了
		offlineMu.Lock()
		offlineMounts[vm.ID] = m
		offlineMu.Unlock()
		ErrorWithMessage(c, http.StatusInternalServerError, "卸载失败", err)
		return
	}
	// 挂载点目录由 OfflineMount 创建，卸载成功后一并清掉，避免 /tmp 下堆积空目录
	_ = os.RemoveAll(m.Mountpoint)
	Success(c, gin.H{"message": "已卸载"})
}
