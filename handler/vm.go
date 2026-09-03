package handler

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/virt"
	"gorm.io/gorm"
)

// vmNameRegex 虚拟机名称只允许字母、数字、下划线和连字符
var vmNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// validateVMName 校验虚拟机名称合法性
func validateVMName(name string) bool {
	return vmNameRegex.MatchString(name)
}

// randomMAC 生成一个 52:54:00:xx:xx:xx 格式的 KVM 默认 MAC 地址
func randomMAC() (string, error) {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("52:54:00:%02x:%02x:%02x", b[0], b[1], b[2]), nil
}

// randomUUID 生成一个符合 RFC 4122 的 v4 UUID 字符串
func randomUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]),
		hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]),
	), nil
}

// domainXMLTmpl libvirt domain 定义模板（支持可选 ISO 光驱安装）
var domainXMLTmpl = template.Must(template.New("domain").Parse(`<domain type='kvm'>
  <name>{{.Name}}</name>
  <uuid>{{.UUID}}</uuid>
  <memory unit='KiB'>{{.MemoryKiB}}</memory>
  <vcpu placement='static'>{{.VCPU}}</vcpu>
  <os>
    <type arch='x86_64' machine='pc'>hvm</type>
    <boot dev='hd'/>
  </os>
  <features>
    <acpi/>
    <apic/>
  </features>
  <devices>
    <disk type='file' device='disk'>
      <driver name='qemu' type='qcow2'/>
      <source file='{{.DiskPath}}'/>
      <target dev='vda' bus='virtio'/>
    </disk>
    {{if .ISOPath}}
    <disk type='file' device='cdrom'>
      <driver name='qemu' type='raw'/>
      <source file='{{.ISOPath}}'/>
      <target dev='hda' bus='ide'/>
      <readonly/>
    </disk>
    {{end}}
    <interface type='network'>
      <mac address='{{.MAC}}'/>
      <source network='default'/>
      <model type='virtio'/>
    </interface>
    <graphics type='vnc' port='-1' autoport='yes'/>
  </devices>
</domain>
`))

// buildDomainXML 生成 libvirt domain XML
func buildDomainXML(name, uuid, mac, diskPath, isoPath string, memoryKiB, vcpu int) (string, error) {
	var buf bytes.Buffer
	err := domainXMLTmpl.Execute(&buf, map[string]interface{}{
		"Name":      name,
		"UUID":      uuid,
		"MAC":       mac,
		"DiskPath":  diskPath,
		"ISOPath":   isoPath,
		"MemoryKiB": memoryKiB,
		"VCPU":      vcpu,
	})
	if err != nil {
		return "", err
	}
	return buf.String(), nil
}

// VMHandler 虚拟机处理器
type VMHandler struct {
	DB   *gorm.DB
	Virt *virt.Virt
}

// NewVMHandler 创建虚拟机处理器
func NewVMHandler(db *gorm.DB) *VMHandler {
	return &VMHandler{DB: db, Virt: virt.New()}
}

// VMInfo virsh返回的虚拟机信息
type VMInfo struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
}

// HostInfo 宿主机信息
type HostInfo struct {
	Hostname string `json:"hostname"`
	Kernel   string `json:"kernel"`
	CPUs     string `json:"cpus"`
	MemTotal string `json:"memTotal"`
	MemUsed  string `json:"memUsed"`
	Uptime   string `json:"uptime"`
}

// ListVMs 获取虚拟机列表
func (h *VMHandler) ListVMs(c *gin.Context) {
	// 从数据库查询虚拟机
	var vms []model.VM
	if err := h.DB.Preload("Host").Find(&vms).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询虚拟机失败"})
		return
	}

	// 同步 libvirt 状态：一次 RPC 拉取所有域状态，避免逐个查询
	stateMap, err := h.Virt.GetAllDomainStates()
	if err == nil && stateMap != nil {
		for i := range vms {
			if s, ok := stateMap[vms[i].Name]; ok {
				vms[i].Status = s
				h.DB.Model(&vms[i]).Update("status", s)
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "success",
		"data": gin.H{
			"total": len(vms),
			"items": vms,
		},
	})
}

// GetVM 获取虚拟机详情
func (h *VMHandler) GetVM(c *gin.Context) {
	id := c.Param("id")

	var vm model.VM
	if err := h.DB.Preload("Host").First(&vm, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "虚拟机不存在"})
		return
	}

	// 同步 libvirt 状态
	if state, err := h.Virt.GetDomainState(vm.Name); err == nil && state != "" {
		vm.Status = state
		h.DB.Model(&vm).Update("status", state)
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "success",
		"data":    vm,
	})
}

// CreateVM 创建虚拟机
func (h *VMHandler) CreateVM(c *gin.Context) {
	var req struct {
		Name        string `json:"name" binding:"required"`
		HostID      uint   `json:"host_id" binding:"required"`
		Template    string `json:"template"`
		StoragePool string `json:"storage_pool"`
		ISOPath     string `json:"iso_path"`
		VCPU        int    `json:"vcpu"`
		MemoryMB    int    `json:"memory_mb"`
		DiskGB      int    `json:"disk_gb"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}

	// 校验名称合法性
	if !validateVMName(req.Name) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "虚拟机名称只允许字母、数字、下划线和连字符"})
		return
	}

	// 检查宿主机是否存在
	var host model.Host
	if err := h.DB.First(&host, req.HostID).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "宿主机不存在"})
		return
	}

	// 设置默认值
	if req.StoragePool == "" {
		req.StoragePool = "vmops"
	}
	if req.VCPU == 0 {
		req.VCPU = 1
	}
	if req.MemoryMB == 0 {
		req.MemoryMB = 1024
	}
	if req.DiskGB == 0 {
		req.DiskGB = 20
	}

	// 生成 UUID 与 MAC，供 libvirt 与 DB 记录使用
	uuid, err := randomUUID()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "生成虚拟机 UUID 失败"})
		return
	}
	mac, err := randomMAC()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "生成虚拟机 MAC 失败"})
		return
	}

	// 创建虚拟机记录
	vm := model.VM{
		UUID:       uuid,
		Name:       req.Name,
		HostID:     req.HostID,
		Template:   req.Template,
		StoragePool: req.StoragePool,
		VCPU:       req.VCPU,
		MemoryMB:   req.MemoryMB,
		DiskGB:     req.DiskGB,
		MACAddress: mac,
		Status:     "shut off",
	}

	if err := h.DB.Create(&vm).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建虚拟机失败"})
		return
	}

	// 真正在 KVM 宿主机上落地：libvirt 存储池建卷 + 定义 domain
	if err := h.provisionVM(req.Name, uuid, mac, req.StoragePool, req.DiskGB, req.MemoryMB, req.VCPU, req.ISOPath); err != nil {
		// 回滚 DB 记录，避免残留脏数据
		h.DB.Delete(&vm)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建虚拟机失败", "detail": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "创建成功",
		"data":    vm,
	})
}

// provisionVM 在 KVM 宿主机上创建存储卷并定义（但不自动启动）虚拟机。
// 建盘通过 libvirt 存储池/存储卷 API（对应 virsh vol-create-as），无需 qemu-img。
func (h *VMHandler) provisionVM(name, uuid, mac, pool string, diskGB, memoryMB, vcpu int, isoPath string) error {
	// 1. 在指定存储池创建 qcow2 存储卷（对应 virsh vol-create-as --pool xxx --name xxx --capacity xG --format qcow2）
	if _, err := h.Virt.CreateVolume(pool, name, diskGB); err != nil {
		return fmt.Errorf("创建存储卷失败: %v", err)
	}

	// 2. 查询卷路径（卷路径来自存储池 target path）
	poolPath, err := h.Virt.GetPoolPath(pool)
	if err != nil {
		return fmt.Errorf("获取存储池路径失败: %v", err)
	}
	diskPath := filepath.Join(poolPath, name+".qcow2")

	// 3. 生成 domain XML（可选 ISO 光驱）
	xml, err := buildDomainXML(name, uuid, mac, diskPath, isoPath, memoryMB*1024, vcpu)
	if err != nil {
		return fmt.Errorf("生成 domain XML 失败: %v", err)
	}

	// 4. 定义 domain（对应 virsh define，保持 shut off 不自动启动）
	if err := h.Virt.DefineDomain(xml); err != nil {
		return fmt.Errorf("定义虚拟机失败: %v", err)
	}

	return nil
}

// StartVM 启动虚拟机
func (h *VMHandler) StartVM(c *gin.Context) {
	id := c.Param("id")

	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "虚拟机不存在"})
		return
	}

	// 调用 libvirt 启动
	if err := h.Virt.StartDomain(vm.Name); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 更新状态
	h.DB.Model(&vm).Update("status", "running")

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "虚拟机已启动",
	})
}

// StopVM 停止虚拟机
func (h *VMHandler) StopVM(c *gin.Context) {
	id := c.Param("id")

	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "虚拟机不存在"})
		return
	}

	// 调用 libvirt 关机：先优雅关机，轮询等待其真正关闭，超时后强制断电。
	shutdownErr := h.Virt.ShutdownDomain(vm.Name)
	if shutdownErr != nil {
		// 优雅关机调用失败（如域不存在），直接强制
		if ferr := h.Virt.DestroyDomain(vm.Name); ferr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": ferr.Error()})
			return
		}
	} else {
		// 轮询等待优雅关机生效（最多 ~15s），超时仍运行则强制
		shutOff := false
		for i := 0; i < 15; i++ {
			time.Sleep(1 * time.Second)
			state, err := h.Virt.GetDomainState(vm.Name)
			if err == nil && state == "shut off" {
				shutOff = true
				break
			}
		}
		if !shutOff {
			_ = h.Virt.DestroyDomain(vm.Name)
		}
	}

	// 更新状态
	h.DB.Model(&vm).Update("status", "shut off")

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "虚拟机已停止",
	})
}

// RestartVM 重启虚拟机
func (h *VMHandler) RestartVM(c *gin.Context) {
	id := c.Param("id")

	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "虚拟机不存在"})
		return
	}

	// 调用 libvirt 重启
	if err := h.Virt.RebootDomain(vm.Name); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "虚拟机已重启",
	})
}

// DeleteVM 删除虚拟机
func (h *VMHandler) DeleteVM(c *gin.Context) {
	id := c.Param("id")

	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "虚拟机不存在"})
		return
	}

	// 1. 调用 libvirt 删除域定义
	if err := h.Virt.UndefineDomain(vm.Name); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 2. 删除对应存储卷（对应 virsh vol-delete）
	pool := vm.StoragePool
	if pool == "" {
		pool = "vmops"
	}
	if err := h.Virt.DeleteVolume(pool, vm.Name+".qcow2"); err != nil {
		// 卷删除失败不阻断，域已删
		_ = err
	}

	// 3. 软删除数据库记录
	h.DB.Delete(&vm)

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "虚拟机已删除",
	})
}

// GetVMXML 获取虚拟机 XML 定义
func (h *VMHandler) GetVMXML(c *gin.Context) {
	id := c.Param("id")
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "虚拟机不存在"})
		return
	}

	xml, err := h.Virt.GetDomainXML(vm.Name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	Success(c, gin.H{"name": vm.Name, "xml": xml})
}

// UpdateVMXML 更新虚拟机 XML 定义（高级功能）
func (h *VMHandler) UpdateVMXML(c *gin.Context) {
	id := c.Param("id")
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "虚拟机不存在"})
		return
	}

	var req struct {
		XML string `json:"xml" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}

	if err := h.Virt.UpdateDomainXML(vm.Name, req.XML); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	Success(c, gin.H{"name": vm.Name, "message": "XML 已更新"})
}

// ListSnapshots 获取虚拟机快照列表
func (h *VMHandler) ListSnapshots(c *gin.Context) {
	id := c.Param("id")
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "虚拟机不存在"})
		return
	}

	snaps, err := h.Virt.ListSnapshots(vm.Name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	Success(c, snaps)
}

// CreateSnapshot 创建虚拟机快照
func (h *VMHandler) CreateSnapshot(c *gin.Context) {
	id := c.Param("id")
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "虚拟机不存在"})
		return
	}

	var req struct {
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if !validateVMName(req.Name) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "快照名称只允许字母、数字、下划线和连字符"})
		return
	}

	if err := h.Virt.CreateSnapshot(vm.Name, req.Name); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	Success(c, gin.H{"vm": vm.Name, "snapshot": req.Name})
}

// DeleteSnapshot 删除虚拟机快照
func (h *VMHandler) DeleteSnapshot(c *gin.Context) {
	id := c.Param("id")
	snapName := c.Param("snap")
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "虚拟机不存在"})
		return
	}

	if err := h.Virt.DeleteSnapshot(vm.Name, snapName); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	Success(c, gin.H{"vm": vm.Name, "snapshot": snapName})
}

// RevertSnapshot 回滚虚拟机到指定快照
func (h *VMHandler) RevertSnapshot(c *gin.Context) {
	id := c.Param("id")
	snapName := c.Param("snap")
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "虚拟机不存在"})
		return
	}

	if err := h.Virt.RevertSnapshot(vm.Name, snapName); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	Success(c, gin.H{"vm": vm.Name, "snapshot": snapName})
}

// GetHostInfo 获取宿主机信息
func (h *VMHandler) GetHostInfo(c *gin.Context) {
	hostname, err := exec.Command("hostname").Output()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "获取主机名失败", "detail": err.Error()})
		return
	}
	kernel, err := exec.Command("uname", "-r").Output()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "获取内核版本失败", "detail": err.Error()})
		return
	}
	cpus, err := exec.Command("nproc").Output()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "获取 CPU 数量失败", "detail": err.Error()})
		return
	}
	free, err := exec.Command("free", "-h").Output()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "获取内存信息失败", "detail": err.Error()})
		return
	}
	uptime, err := exec.Command("uptime", "-p").Output()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "获取运行时长失败", "detail": err.Error()})
		return
	}

	lines := strings.Split(string(free), "\n")
	var memTotal, memUsed string
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "Mem:") {
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				memTotal = fields[1]
				memUsed = fields[2]
			}
		}
	}

	c.JSON(200, HostInfo{
		Hostname: strings.TrimSpace(string(hostname)),
		Kernel:   strings.TrimSpace(string(kernel)),
		CPUs:     strings.TrimSpace(string(cpus)),
		MemTotal: memTotal,
		MemUsed:  memUsed,
		Uptime:   strings.TrimSpace(string(uptime)),
	})
}

// randomMACStr 供 provisionVM 内部生成临时 MAC。