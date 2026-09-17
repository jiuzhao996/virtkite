package handler

// v2 大升级批次 2：VM 文件管理的「离线挂载」通道。
// 对关机虚拟机，用 guestmount 把系统盘以只读方式挂到本机临时目录，再走普通文件 API 浏览/下载——
// 不依赖 VM 内 SSH 服务（与 vm_files.go 的 SSH 在线通道互补：在线管开机机，离线管关机机）。
//
// 权限现实：libvirt 卷属 root，web 进程用户读不了，因此 guestmount 经 sudo -n 调用
// （宿主机已配置免密 sudo）；磁盘若被运行中 VM 持有会因写锁失败——运行中的 VM 一律拒绝挂载。
//
// 挂载为 --ro 只读：宁可浏览受限，不可损坏磁盘（与删卷守卫同一立场）。

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

const offlineMountBase = "/tmp/vmops-mounts"

type offlineMount struct {
	Mountpoint string
	Filesystem string
	Disk       string
	MountedAt  time.Time
}

var (
	offlineMu     sync.Mutex
	offlineMounts = map[uint]*offlineMount{} // vmID → 挂载信息（进程重启即失，重新挂载即可）
)

// sudoRun 以 sudo -n 执行 guestmount 族命令（免密 sudo；-n 禁止交互挂死）。
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

// offlineResolve 离线通道公共前置：解析 :id → 取 VM → 授权可见性。
func offlineResolve(c *gin.Context, db interface {
	First(dest interface{}, conds ...interface{}) (tx interface{ Error() error })
}) {
	_ = db // 占位（实际实现见 offlineVM）
}

// offlineVM 离线通道取 VM：paramID → DB → vmVisible。未授权与不存在同响应。
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
func (h *VMFilesHandler) OfflineMount(c *gin.Context) {
	vm := h.offlineVM(c)
	if vm == nil {
		return
	}

	state, err := h.Virt.GetDomainState(vm.Name)
	if err == nil && state != virt.StatusShutOff {
		Fail(c, http.StatusBadRequest, "虚拟机运行中，请先关机再挂载磁盘浏览（运行中的磁盘被 qemu 锁定且写操作不安全）")
		return
	}

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
		ErrorWithMessage(c, http.StatusInternalServerError, "检查磁盘文件系统失败（首次运行需构建 libguestfs 环境，耗时较长，可重试）", err)
		return
	}
	device, fstype, err := pickRootFS(fss)
	if err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}

	mp := filepath.Join(offlineMountBase, fmt.Sprint(vm.ID))
	_ = os.MkdirAll(mp, 0755)
	if _, err := sudoRun(3*time.Minute, "guestmount", "-a", disk, "-m", device, "--ro", mp); err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "挂载磁盘失败", err)
		return
	}
	offlineMu.Lock()
	offlineMounts[vm.ID] = &offlineMount{Mountpoint: mp, Filesystem: device + "（" + fstype + "）", Disk: disk, MountedAt: time.Now()}
	offlineMu.Unlock()
	Success(c, gin.H{"mountpoint": mp, "filesystem": device + "（" + fstype + "）", "items": listRoot(mp)})
}

// OfflineList POST /api/vms/:id/files/offline/list  body {path}
func (h *VMFilesHandler) OfflineList(c *gin.Context) {
	vm := h.offlineVM(c)
	if vm == nil {
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Path == "" {
		Fail(c, http.StatusBadRequest, "缺少 path 参数")
		return
	}
	m, p, ok := offlineTarget(c, vm.ID, req.Path, true)
	if !ok {
		return
	}
	// FUSE 挂载点仅挂载者可见，读目录经 sudo -n（解析复用 SSH 在线通道的 ls 解析器）
	out, err := sudoRun(30*time.Second, "ls", "-la", "--time-style=+%s", p)
	if err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "读取目录失败", err)
		return
	}
	Success(c, gin.H{"path": p, "mountpoint": m.Mountpoint, "items": parseLsOutput(out)})
}

// OfflineDownload GET /api/vms/:id/files/offline/download?path=/etc/hostname
// 直接返回文件内容（octet-stream，非统一信封）。
func (h *VMFilesHandler) OfflineDownload(c *gin.Context) {
	vm := h.offlineVM(c)
	if vm == nil {
		return
	}
	p := c.Query("path")
	if p == "" {
		Fail(c, http.StatusBadRequest, "缺少 path 参数")
		return
	}
	m, clean, ok := offlineTarget(c, vm.ID, p, false)
	if !ok {
		return
	}
	// 读文件同样经 sudo（FUSE 权限）；空内容校验 sudoRun 区分不出——cat 失败时 err 已带 stderr
	data, err := sudoRun(60*time.Second, "cat", clean)
	if err != nil && data == "" {
		ErrorWithMessage(c, http.StatusBadRequest, "读取文件失败（目录请用浏览）", err)
		return
	}
	c.Header("Content-Disposition", "attachment; filename="+filepath.Base(clean))
	c.Data(http.StatusOK, "application/octet-stream", []byte(data))
	_ = m
}

// OfflineUnmount POST /api/vms/:id/files/offline/unmount
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
	delete(offlineMounts, vm.ID)
	offlineMu.Unlock()
	if _, err := sudoRun(30*time.Second, "fusermount", "-u", m.Mountpoint); err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "卸载失败", err)
		return
	}
	_ = os.RemoveAll(m.Mountpoint)
	Success(c, gin.H{"message": "已卸载"})
}

// offlineTarget 解析并清洗离线路径：必须在挂载点内（防穿越）。
func offlineTarget(c *gin.Context, vmID uint, reqPath string, needDir bool) (*offlineMount, string, bool) {
	offlineMu.Lock()
	m, ok := offlineMounts[vmID]
	offlineMu.Unlock()
	if !ok {
		Fail(c, http.StatusNotFound, "磁盘未挂载（请先执行挂载）")
		return nil, "", false
	}
	clean := filepath.Clean(filepath.Join(m.Mountpoint, reqPath))
	if clean != m.Mountpoint && !strings.HasPrefix(clean, m.Mountpoint+"/") {
		Fail(c, http.StatusBadRequest, "非法路径")
		return nil, "", false
	}
	if needDir {
		out, err := sudoRun(15*time.Second, "stat", "-c", "%F", clean)
		if err != nil || !strings.HasPrefix(strings.TrimSpace(out), "directory") {
			Fail(c, http.StatusBadRequest, "目录不存在")
			return nil, "", false
		}
	}
	return m, clean, true
}

// listRoot 挂载成功后返回根目录文件名预览。
// guestmount 的 FUSE 挂载默认仅挂载者（root）可见，web 进程直接 ReadDir 会得到空/拒绝，
// 因此离线通道的目录读取统一经 sudo -n。
func listRoot(mp string) []string {
	out, err := sudoRun(30*time.Second, "ls", "-1", mp)
	if err != nil {
		return []string{}
	}
	names := []string{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			names = append(names, line)
		}
	}
	return names
}
