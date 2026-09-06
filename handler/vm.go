package handler

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/console"
	"github.com/jiuzhao/vmops/service/tasks"
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

// VMHandler 虚拟机处理器
type VMHandler struct {
	DB       *gorm.DB
	Virt     *virt.Virt
	Tasks    *tasks.Manager
	Sessions *console.Registry
}

// NewVMHandler 创建虚拟机处理器
func NewVMHandler(db *gorm.DB, taskMgr *tasks.Manager, sessions *console.Registry) *VMHandler {
	return &VMHandler{DB: db, Virt: virt.New(), Tasks: taskMgr, Sessions: sessions}
}

// taskUserFromContext 从 gin 上下文安全取 user_id/username（取不到传 nil/""）。
func taskUserFromContext(c *gin.Context) (*uint, string) {
	var userID *uint
	username := ""
	if v, ok := c.Get("user_id"); ok && v != nil {
		if id, ok := v.(uint); ok {
			copied := id
			userID = &copied
		}
	}
	if v, ok := c.Get("username"); ok && v != nil {
		if s, ok := v.(string); ok {
			username = s
		}
	}
	return userID, username
}

// submitTaskGuard 校验任务管理器已注入。
func (h *VMHandler) submitTaskGuard(c *gin.Context) bool {
	if h.Tasks == nil {
		Fail(c, http.StatusInternalServerError, "任务系统未初始化")
		return false
	}
	return true
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

// ListVMs 获取虚拟机列表（含运行中 VM 的实时性能，合并 vm-perf，列表页一次请求即可渲染指标）。
func (h *VMHandler) ListVMs(c *gin.Context) {
	// 从数据库查询虚拟机
	var vms []model.VM
	if err := h.DB.Preload("Host").Find(&vms).Error; err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "查询虚拟机失败", err)
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

	// 实时性能：仅 running 采样，key 为 VM id（与 /dashboard/vm-perf 同口径，供列表页合并请求）
	perf := make(map[uint]gin.H, len(vms))
	for _, vm := range vms {
		if vm.Status != model.VMStatusRunning {
			continue
		}
		if st, err := h.Virt.GetDomainStats(vm.Name); err == nil && st != nil {
			memPct := 0.0
			if st.GuestTotalKiB > 0 {
				memPct = float64(st.GuestUsedKiB) / float64(st.GuestTotalKiB) * 100
			} else if st.MemTotalKiB > 0 {
				memPct = float64(st.MemUsedKiB) / float64(st.MemTotalKiB) * 100
			}
			perf[vm.ID] = gin.H{"cpu_percent": st.CpuPercent, "mem_pct": memPct}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "success",
		"data": gin.H{
			"total": len(vms),
			"items": vms,
			"perf":  perf,
		},
	})
}

// GetVM 获取虚拟机详情
func (h *VMHandler) GetVM(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}

	var vm model.VM
	if err := h.DB.Preload("Host").First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
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

// CreateVM 创建虚拟机（异步：校验基础参数后 Submit create_vm，后台执行 provision）。
// 对应 virsh vol-create-as + virsh define，耗时逻辑已搬运至 service/tasks executor。
// HTTP 202 返回 {task_id}，前端轮询 GET /api/tasks/:id。
func (h *VMHandler) CreateVM(c *gin.Context) {
	if !h.submitTaskGuard(c) {
		return
	}
	// 原样透传请求体：先读原始字节，再分别做校验与 payload 透传。
	body, err := c.GetRawData()
	if err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}
	var req struct {
		Name        string                   `json:"name"`
		HostID      uint                     `json:"host_id"`
		StoragePool string                   `json:"storage_pool"`
		VCPU        int                      `json:"vcpu"`
		MemoryMB    int                      `json:"memory_mb"`
		Disks       []map[string]interface{} `json:"disks"`
		Interfaces  []virt.InterfaceSpec     `json:"interfaces"`
		Network     string                   `json:"network"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}
	if req.Name == "" {
		Fail(c, http.StatusBadRequest, "虚拟机名称不能为空")
		return
	}
	// 校验名称合法性
	if !validateVMName(req.Name) {
		Fail(c, http.StatusBadRequest, "虚拟机名称只允许字母、数字、下划线和连字符")
		return
	}
	// 轻量校验宿主机存在性：未指定时确认平台已登记首台
	if req.HostID != 0 {
		var host model.Host
		if err := h.DB.First(&host, req.HostID).Error; err != nil {
			ErrorWithMessage(c, http.StatusBadRequest, "宿主机不存在", err)
			return
		}
	} else {
		if _, err := h.firstHost(); err != nil {
			ErrorWithMessage(c, http.StatusBadRequest, "请先在宿主机管理中登记宿主机", err)
			return
		}
	}
	payload := map[string]interface{}{}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &payload); err != nil {
			ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
			return
		}
	}
	if payload == nil {
		payload = map[string]interface{}{}
	}
	userID, username := taskUserFromContext(c)
	task, err := h.Tasks.Submit("create_vm", "创建虚拟机 "+req.Name, payload, userID, username, req.Name, nil)
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "提交任务失败", err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{
		"code":    http.StatusAccepted,
		"message": "任务已提交",
		"data":    gin.H{"task_id": task.ID},
	})
}

// GetVMSpec 返回虚拟机完整配置（DB 记录 + DomainSpec，spec 含 raw_xml 回显）。
func (h *VMHandler) GetVMSpec(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	var vm model.VM
	if err := h.DB.Preload("Host").First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}
	// 同步 libvirt 状态
	if state, err := h.Virt.GetDomainState(vm.Name); err == nil && state != "" {
		vm.Status = state
		h.DB.Model(&vm).Update("status", state)
	}

	spec, err := h.Virt.GetDomainSpec(vm.Name)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	Success(c, gin.H{"vm": vm, "spec": spec})
}

// UpdateVMSpec 整体重 define 虚拟机配置（对应 virsh edit 后 define）。
// 请求体为完整 DomainSpec（raw_xml 忽略）；VM 运行中禁止修改，须先关机。
// 同步回写 DB 的 vcpu / memory_mb / disk_gb（首个磁盘容量近似）/ mac_address（首个网卡）。
func (h *VMHandler) UpdateVMSpec(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}

	var spec virt.DomainSpec
	if err := c.ShouldBindJSON(&spec); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}

	// 运行时禁止整体重定义，提示先关机
	if state, err := h.Virt.GetDomainState(vm.Name); err == nil && state == virt.StatusRunning {
		Fail(c, http.StatusBadRequest, "虚拟机运行中，请先关机后再修改配置")
		return
	}

	// 名称/UUID 以 DB 为准，防止定义错位；raw_xml 由 BuildDomainXML 重建，忽略回显原文
	spec.Name = vm.Name
	if spec.UUID == "" {
		spec.UUID = vm.UUID
	}
	spec.CloudInit = nil
	spec.RawXML = ""

	xmlstr, err := virt.BuildDomainXML(&spec)
	if err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "虚拟机配置不合法", err)
		return
	}
	if err := h.Virt.UpdateDomainXML(vm.Name, xmlstr); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	// 同步 DB 摘要字段
	updates := map[string]interface{}{"vcpu": spec.VCPU, "memory_mb": spec.MemoryMB}
	if len(spec.Disks) > 0 {
		if gb := h.Virt.DiskSizeGB(spec.Disks[0].Source); gb > 0 {
			updates["disk_gb"] = gb
		}
	}
	if len(spec.Interfaces) > 0 && spec.Interfaces[0].MAC != "" {
		updates["mac_address"] = spec.Interfaces[0].MAC
	}
	h.DB.Model(&vm).Updates(updates)

	Success(c, gin.H{"vm": vm.Name, "message": "配置已更新"})
}

// PauseVM 暂停虚拟机（对应 virsh suspend）。
func (h *VMHandler) PauseVM(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}
	if err := h.Virt.PauseDomain(vm.Name); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	h.DB.Model(&vm).Update("status", model.VMStatusPaused)
	Success(c, gin.H{"vm": vm.Name, "message": "虚拟机已暂停"})
}

// ResumeVM 恢复已暂停的虚拟机（对应 virsh resume）。
func (h *VMHandler) ResumeVM(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}
	if err := h.Virt.ResumeDomain(vm.Name); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	h.DB.Model(&vm).Update("status", model.VMStatusRunning)
	Success(c, gin.H{"vm": vm.Name, "message": "虚拟机已恢复"})
}

// AttachDisk 热插拔磁盘（对应 virsh attach-device，运行中生效并落配置）。
// body: {disk: DiskSpec}；disk.Target 为空时按 bus 自动分配（NextDiskTarget）。
func (h *VMHandler) AttachDisk(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}

	var req struct {
		Disk virt.DiskSpec `json:"disk"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Disk.Source == "" {
		ErrorWithMessage(c, http.StatusBadRequest, "磁盘参数错误（需提供 source）", err)
		return
	}
	if req.Disk.Bus == "" {
		req.Disk.Bus = "virtio"
	}
	if req.Disk.Device == "" {
		req.Disk.Device = "disk"
	}
	if req.Disk.Target == "" {
		spec, err := h.Virt.GetDomainSpec(vm.Name)
		if err != nil {
			ErrorResponse(c, http.StatusInternalServerError, err)
			return
		}
		req.Disk.Target = virt.NextDiskTarget(spec, req.Disk.Bus)
	}

	if err := h.Virt.AttachDisk(vm.Name, req.Disk); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"vm": vm.Name, "disk": req.Disk})
}

// DetachDisk 移除磁盘（对应 virsh detach-device，按 target dev 匹配）。
func (h *VMHandler) DetachDisk(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	target := c.Param("target")
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}
	if err := h.Virt.DetachDisk(vm.Name, target); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"vm": vm.Name, "target": target})
}

// AttachInterface 添加网卡（对应 virsh attach-interface，运行中生效并落配置）。
// body: {interface: InterfaceSpec}；mac 为空自动生成。
func (h *VMHandler) AttachInterface(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}

	var req struct {
		Interface virt.InterfaceSpec `json:"interface"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}
	if req.Interface.Type == "" {
		req.Interface.Type = "network"
	}
	if req.Interface.Source == "" {
		req.Interface.Source = "default"
	}
	if req.Interface.MAC == "" {
		mac, err := randomMAC()
		if err != nil {
			ErrorWithMessage(c, http.StatusInternalServerError, "生成网卡 MAC 失败", err)
			return
		}
		req.Interface.MAC = mac
	}

	if err := h.Virt.AttachInterface(vm.Name, req.Interface); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"vm": vm.Name, "interface": req.Interface})
}

// DetachInterface 移除网卡（对应 virsh detach-interface，按 MAC 地址匹配）。
func (h *VMHandler) DetachInterface(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	mac := c.Param("mac")
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}
	if err := h.Virt.DetachInterface(vm.Name, mac); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"vm": vm.Name, "mac": mac})
}

// SetVcpu 调整 CPU 核数（对应 virsh setvcpus），同步 DB。
// 停机态通过重 define 修改持久配置（setvcpus CONFIG 无法超 <vcpu> 上限）；
// 运行态走 live API（仅可调至启动时最大核数以内，超出提示关机）。
func (h *VMHandler) SetVcpu(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}

	var req struct {
		VCPU int `json:"vcpu"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.VCPU <= 0 {
		ErrorWithMessage(c, http.StatusBadRequest, "vCPU 数量必须大于 0", err)
		return
	}

	// 停机态：读取 spec → 改 vcpu → 重建 XML → 重 define
	if state, err := h.Virt.GetDomainState(vm.Name); err == nil && state != virt.StatusRunning {
		spec, err := h.Virt.GetDomainSpec(vm.Name)
		if err != nil {
			ErrorResponse(c, http.StatusInternalServerError, err)
			return
		}
		spec.VCPU = req.VCPU
		spec.RawXML = ""
		xmlstr, err := virt.BuildDomainXML(spec)
		if err != nil {
			ErrorWithMessage(c, http.StatusBadRequest, "虚拟机配置不合法", err)
			return
		}
		if err := h.Virt.UpdateDomainXML(vm.Name, xmlstr); err != nil {
			ErrorResponse(c, http.StatusInternalServerError, err)
			return
		}
	} else {
		// 运行态：live+config 热调（超出启动时最大核数由 libvirt 报错，翻译提示）
		if err := h.Virt.SetVcpus(vm.Name, req.VCPU); err != nil {
			ErrorResponse(c, http.StatusInternalServerError, err)
			return
		}
	}
	h.DB.Model(&vm).Update("vcpu", req.VCPU)
	Success(c, gin.H{"vm": vm.Name, "vcpu": req.VCPU})
}

// SetMemory 调整内存（对应 virsh setmem），同步 DB。
// 停机态通过重 define 修改持久配置；运行态走 live API（仅可调至启动时最大内存以内）。
func (h *VMHandler) SetMemory(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}

	var req struct {
		MemoryMB int `json:"memory_mb"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.MemoryMB <= 0 {
		ErrorWithMessage(c, http.StatusBadRequest, "内存大小必须大于 0", err)
		return
	}

	if state, err := h.Virt.GetDomainState(vm.Name); err == nil && state != virt.StatusRunning {
		spec, err := h.Virt.GetDomainSpec(vm.Name)
		if err != nil {
			ErrorResponse(c, http.StatusInternalServerError, err)
			return
		}
		spec.MemoryMB = req.MemoryMB
		spec.RawXML = ""
		xmlstr, err := virt.BuildDomainXML(spec)
		if err != nil {
			ErrorWithMessage(c, http.StatusBadRequest, "虚拟机配置不合法", err)
			return
		}
		if err := h.Virt.UpdateDomainXML(vm.Name, xmlstr); err != nil {
			ErrorResponse(c, http.StatusInternalServerError, err)
			return
		}
	} else {
		if err := h.Virt.SetMemory(vm.Name, req.MemoryMB); err != nil {
			ErrorResponse(c, http.StatusInternalServerError, err)
			return
		}
	}
	h.DB.Model(&vm).Update("memory_mb", req.MemoryMB)
	Success(c, gin.H{"vm": vm.Name, "memory_mb": req.MemoryMB})
}

// SetAutostart 设置开机自启（对应 virsh autostart）。
func (h *VMHandler) SetAutostart(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}

	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}
	if err := h.Virt.SetAutostart(vm.Name, req.Enabled); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"vm": vm.Name, "autostart": req.Enabled})
}

// SetBoot 修改引导顺序（body {devices: []}，如 ["cdrom","hd"]）。停机状态下整体重 define。
func (h *VMHandler) SetBoot(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}

	var req struct {
		Devices []string `json:"devices"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Devices) == 0 {
		ErrorWithMessage(c, http.StatusBadRequest, "引导设备列表不能为空", err)
		return
	}

	spec, err := h.Virt.GetDomainSpec(vm.Name)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	spec.Boot.Devices = req.Devices
	spec.RawXML = ""

	xmlstr, err := virt.BuildDomainXML(spec)
	if err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "引导配置不合法", err)
		return
	}
	if err := h.Virt.UpdateDomainXML(vm.Name, xmlstr); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"vm": vm.Name, "devices": req.Devices})
}

// GetVMStats 返回虚拟机实时性能统计（服务端差分计算 CPU/IO 速率）。
func (h *VMHandler) GetVMStats(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}
	stats, err := h.Virt.GetDomainStats(vm.Name)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, stats)
}

// CloneVM 克隆虚拟机（异步：校验后 Submit clone_vm，后台执行 vol-clone + define）。
// HTTP 202 返回 {task_id}，前端轮询 GET /api/tasks/:id。
func (h *VMHandler) CloneVM(c *gin.Context) {
	if !h.submitTaskGuard(c) {
		return
	}
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	var src model.VM
	if err := h.DB.Preload("Host").First(&src, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}
	var req struct {
		Name        string `json:"name" binding:"required"`
		StoragePool string `json:"storage_pool"`
		VCPU        int    `json:"vcpu"`
		MemoryMB    int    `json:"memory_mb"`
		Network     string `json:"network"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}
	if !validateVMName(req.Name) {
		Fail(c, http.StatusBadRequest, "虚拟机名称只允许字母、数字、下划线和连字符")
		return
	}
	payload := map[string]interface{}{
		"source_id":    src.ID,
		"name":         req.Name,
		"storage_pool": req.StoragePool,
		"vcpu":         req.VCPU,
		"memory_mb":    req.MemoryMB,
		"network":      req.Network,
	}
	userID, username := taskUserFromContext(c)
	srcID := src.ID
	task, err := h.Tasks.Submit("clone_vm", "克隆虚拟机 "+req.Name, payload, userID, username, req.Name, &srcID)
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "提交任务失败", err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{
		"code":    http.StatusAccepted,
		"message": "任务已提交",
		"data":    gin.H{"task_id": task.ID},
	})
}

// StartVM 启动虚拟机
func (h *VMHandler) StartVM(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}

	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}

	// 调用 libvirt 启动
	if err := h.Virt.StartDomain(vm.Name); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	// 更新状态
	h.DB.Model(&vm).Update("status", model.VMStatusRunning)

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "虚拟机已启动",
	})
}

// StopVM 停止虚拟机（异步：Submit stop_vm，后台执行优雅关机轮询，根治 15s 超时）。
// HTTP 202 返回 {task_id}，前端轮询 GET /api/tasks/:id。
func (h *VMHandler) StopVM(c *gin.Context) {
	if !h.submitTaskGuard(c) {
		return
	}
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}
	payload := map[string]interface{}{"vm_id": vm.ID}
	userID, username := taskUserFromContext(c)
	vmID := vm.ID
	task, err := h.Tasks.Submit("stop_vm", "停止虚拟机 "+vm.Name, payload, userID, username, vm.Name, &vmID)
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "提交任务失败", err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{
		"code":    http.StatusAccepted,
		"message": "任务已提交",
		"data":    gin.H{"task_id": task.ID},
	})
}

// RestartVM 重启虚拟机
func (h *VMHandler) RestartVM(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}

	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}

	// 调用 libvirt 重启
	if err := h.Virt.RebootDomain(vm.Name); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "虚拟机已重启",
	})
}

// DeleteVM 删除虚拟机（异步：Submit delete_vm，后台执行 undefine + 卷清理 + 软删除）。
// HTTP 202 返回 {task_id}，前端轮询 GET /api/tasks/:id。
func (h *VMHandler) DeleteVM(c *gin.Context) {
	if !h.submitTaskGuard(c) {
		return
	}
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}
	payload := map[string]interface{}{"vm_id": vm.ID}
	userID, username := taskUserFromContext(c)
	vmID := vm.ID
	task, err := h.Tasks.Submit("delete_vm", "删除虚拟机 "+vm.Name, payload, userID, username, vm.Name, &vmID)
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "提交任务失败", err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{
		"code":    http.StatusAccepted,
		"message": "任务已提交",
		"data":    gin.H{"task_id": task.ID},
	})
}

// GetVMXML 获取虚拟机 XML 定义
func (h *VMHandler) GetVMXML(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}

	xml, err := h.Virt.GetDomainXML(vm.Name)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	Success(c, gin.H{"name": vm.Name, "xml": xml})
}

// UpdateVMXML 更新虚拟机 XML 定义（高级功能）
func (h *VMHandler) UpdateVMXML(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}

	var req struct {
		XML string `json:"xml" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}

	if err := h.Virt.UpdateDomainXML(vm.Name, req.XML); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	Success(c, gin.H{"name": vm.Name, "message": "XML 已更新"})
}

// ListSnapshots 获取虚拟机快照列表（返回 SnapshotInfo 详情数组，含 description/creation_time/state）。
func (h *VMHandler) ListSnapshots(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}

	snaps, err := h.Virt.ListSnapshots(vm.Name)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	Success(c, snaps)
}

// CreateSnapshot 创建虚拟机快照（body: {name, description?}）。
func (h *VMHandler) CreateSnapshot(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}

	var req struct {
		Name        string `json:"name" binding:"required"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}
	if !validateVMName(req.Name) {
		Fail(c, http.StatusBadRequest, "快照名称只允许字母、数字、下划线和连字符")
		return
	}

	if err := h.Virt.CreateSnapshot(vm.Name, req.Name, req.Description); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	Success(c, gin.H{"vm": vm.Name, "snapshot": req.Name})
}

// DeleteSnapshot 删除虚拟机快照
func (h *VMHandler) DeleteSnapshot(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	snapName := c.Param("snap")
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}

	if err := h.Virt.DeleteSnapshot(vm.Name, snapName); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	Success(c, gin.H{"vm": vm.Name, "snapshot": snapName})
}

// RevertSnapshot 回滚虚拟机到指定快照
func (h *VMHandler) RevertSnapshot(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	snapName := c.Param("snap")
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}

	if err := h.Virt.RevertSnapshot(vm.Name, snapName); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	Success(c, gin.H{"vm": vm.Name, "snapshot": snapName})
}

// ScanImportVMs 扫描宿主机上未被平台纳管的存量域（virsh 已定义、DB 无记录的 VM）。
// 返回全部候选域及其硬件摘要，前端据此勾选导入；libvirt 侧不做任何改动。
func (h *VMHandler) ScanImportVMs(c *gin.Context) {
	host, err := h.firstHost()
	if err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "请先在宿主机管理中登记宿主机", err)
		return
	}

	details, err := h.Virt.ListDomainsWithDetail()
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "扫描宿主机虚拟机失败", err)
		return
	}

	uuids, err := h.trackedUUIDs()
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "查询已纳管虚拟机失败", err)
		return
	}

	items := make([]virt.DomainDetail, 0, len(details))
	managed := 0
	for i := range details {
		d := &details[i]
		if uuids[d.UUID] {
			d.Managed = true
			managed++
		}
		d.DiskGB = h.Virt.DiskSizeGB(d.DiskPath)
		items = append(items, *d)
	}

	Success(c, gin.H{
		"host_id":   host.ID,
		"host_name": host.Name,
		"total":     len(items),
		"managed":   managed,
		"unmanaged": len(items) - managed,
		"items":     items,
	})
}

// ImportVMs 将选中的存量域纳入平台纳管：仅在 DB 写入记录，不修改 libvirt 侧定义。
// 已纳管（UUID 已存在）的域自动跳过；导入后状态与 libvirt 实时对齐。
func (h *VMHandler) ImportVMs(c *gin.Context) {
	var req struct {
		HostID uint     `json:"host_id"`
		Names  []string `json:"names"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Names) == 0 {
		ErrorWithMessage(c, http.StatusBadRequest, "请选择要导入的虚拟机", err)
		return
	}

	var host model.Host
	if req.HostID != 0 {
		err := h.DB.First(&host, req.HostID).Error
		if err != nil {
			ErrorWithMessage(c, http.StatusBadRequest, "宿主机不存在", err)
			return
		}
	} else {
		hst, err := h.firstHost()
		if err != nil {
			ErrorWithMessage(c, http.StatusBadRequest, "请先在宿主机管理中登记宿主机", err)
			return
		}
		host = *hst
	}

	details, err := h.Virt.ListDomainsWithDetail()
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "扫描宿主机虚拟机失败", err)
		return
	}
	byName := make(map[string]virt.DomainDetail, len(details))
	for _, d := range details {
		byName[d.Name] = d
	}

	poolPaths, _ := h.poolPathMap()
	inserted, skipped, failed := 0, 0, 0
	var errs []string
	for _, name := range req.Names {
		d, ok := byName[name]
		if !ok {
			failed++
			errs = append(errs, fmt.Sprintf("%s: 域不存在", name))
			continue
		}

		var cnt int64
		h.DB.Unscoped().Model(&model.VM{}).Where("uuid = ?", d.UUID).Count(&cnt)
		if cnt > 0 {
			skipped++
			continue
		}

		pool := ""
		for p, path := range poolPaths {
			if d.DiskPath != "" && strings.HasPrefix(d.DiskPath, path+"/") {
				pool = p
				break
			}
		}

		vm := model.VM{
			UUID:        d.UUID,
			Name:        d.Name,
			HostID:      host.ID,
			Template:    "imported",
			StoragePool: pool,
			VCPU:        d.VCPU,
			MemoryMB:    d.MemoryMB,
			DiskGB:      d.DiskGB,
			MACAddress:  d.MAC,
			OSType:      d.OSType,
			Status:      d.State,
		}
		if err := h.DB.Create(&vm).Error; err != nil {
			failed++
			// 完整错误（含 GORM/SQL 原文）只进服务端日志，响应里只给中文原因 + 域名，
			// 避免把表结构、约束名等内部细节泄漏到前端（见 AGENTS.md 后端标准第 3 条）
			LogError(c, fmt.Errorf("导入存量虚拟机 %s 写入数据库失败: %w", name, err))
			errs = append(errs, fmt.Sprintf("%s: 写入数据库失败", name))
			continue
		}
		inserted++
	}

	Success(c, gin.H{
		"imported": inserted,
		"skipped":  skipped,
		"failed":   failed,
		"errors":   errs,
	})
}

// firstHost 返回平台登记的首台宿主机（默认纳管目标）。
func (h *VMHandler) firstHost() (*model.Host, error) {
	var host model.Host
	if err := h.DB.Order("id ASC").First(&host).Error; err != nil {
		return nil, err
	}
	return &host, nil
}

// trackedUUIDs 返回 DB 中已纳管（含软删除）的全部 VM UUID 集合。
func (h *VMHandler) trackedUUIDs() (map[string]bool, error) {
	var uuids []string
	if err := h.DB.Unscoped().Model(&model.VM{}).Pluck("uuid", &uuids).Error; err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(uuids))
	for _, u := range uuids {
		set[u] = true
	}
	return set, nil
}

// VMCreateOptions 创建虚拟机向导所需的静态选项（对应契约 GET /api/vms/options）。
type VMCreateOptions struct {
	Pools        []string           `json:"pools"`
	Networks     []string           `json:"networks"`
	CloudImages  []model.Image      `json:"cloud_images"`
	OSList       []virt.OSItem      `json:"os_list"`
	StoragePools []virt.PoolInfo    `json:"storage_pools,omitempty"`
	NetInfo      []virt.NetworkInfo `json:"network_info,omitempty"`
}

// GetVMOptions 返回创建虚拟机向导的选项（存储池、网络、云镜像、OS 列表）。
// 供前端创建向导选择使用（对应 virsh 环境的资源枚举）。
func (h *VMHandler) GetVMOptions(c *gin.Context) {
	// 存储池（名称 + 详情）
	pools, err := h.Virt.ListPools()
	if err != nil {
		pools = []string{}
	}
	poolInfos, _ := h.Virt.ListPoolInfos()

	// 网络（名称 + 详情）
	networks := []string{}
	netInfos := []virt.NetworkInfo{}
	if nws, err := h.Virt.ListNetworks(); err == nil {
		netInfos = nws
		for _, n := range nws {
			networks = append(networks, n.Name)
		}
	}

	// 云镜像：镜像管理中标记为模板的（含普通镜像，便于导入安装盘）
	var images []model.Image
	if err := h.DB.Order("created_at desc").Find(&images).Error; err != nil {
		images = []model.Image{}
	}

	Success(c, gin.H{
		"pools":         pools,
		"storage_pools": poolInfos,
		"networks":      networks,
		"network_info":  netInfos,
		"cloud_images":  images,
		"os_list":       virt.OSList,
	})
}

// poolPathMap 返回 存储池名 → 目标路径 的映射（供磁盘归属推断）。
func (h *VMHandler) poolPathMap() (map[string]string, error) {
	names, err := h.Virt.ListPools()
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(names))
	for _, name := range names {
		if path, err := h.Virt.GetPoolPath(name); err == nil {
			m[name] = path
		}
	}
	return m, nil
}
