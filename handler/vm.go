package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/console"
	"github.com/jiuzhao/vmops/service/dbx"
	"github.com/jiuzhao/vmops/service/tasks"
	"github.com/jiuzhao/vmops/service/virt"
	"github.com/jiuzhao/vmops/service/vmlock"
	"gorm.io/gorm"
)

// vmNameRegex 虚拟机名称只允许字母、数字、下划线和连字符
var vmNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// validateVMName 校验虚拟机名称合法性
func validateVMName(name string) bool {
	return vmNameRegex.MatchString(name)
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

// findVM 按 path 主键查 VM：paramID 解析 + 404 响应一体。
// 返回 false 时已写好「虚拟机不存在」响应，调用方直接 return。
// 抽出目的：该 7 行样板在 20+ 个 handler 里逐字重复（冗余清理批次收敛）。
// 注意：GetVM/GetVMSpec/CloneVM 需要 Preload("Host")，仍保持手写查询，不走本方法。
// 可见性：非 admin 需持有有效授权（借鉴堡垒机 4A，授权决定可见性），未授权与不存在同响应。
func (h *VMHandler) findVM(c *gin.Context) (model.VM, bool) {
	id, ok := paramID(c, "id")
	if !ok {
		return model.VM{}, false
	}
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return model.VM{}, false
	}
	if !vmVisible(c, h.DB, vm.ID) {
		Fail(c, http.StatusNotFound, "虚拟机不存在")
		return model.VM{}, false
	}
	return vm, true
}

// ListVMs 获取虚拟机列表（含运行中 VM 的实时性能，合并 vm-perf，列表页一次请求即可渲染指标）。
func (h *VMHandler) ListVMs(c *gin.Context) {
	// 惰性回填 vms.ip（DHCP 租约 → 按 MAC 匹配；内部 30s 节流），查询前执行保证本次响应拿到新 IP
	h.syncVMIPs()

	// 从数据库查询虚拟机
	var vms []model.VM
	if err := h.DB.Preload("Host").Find(&vms).Error; err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "查询虚拟机失败", err)
		return
	}

	if vms == nil {
		vms = []model.VM{}
	}

	// 授权决定可见性（借鉴堡垒机 4A）：非 admin 只见自己持有有效授权的 VM（查无此项，非置灰）
	if !roleIsAdmin(c) {
		if uid, _ := taskUserFromContext(c); uid != nil {
			allowed := grantedVMIDs(h.DB, *uid)
			filtered := make([]model.VM, 0, len(allowed))
			for _, vm := range vms {
				if allowed[vm.ID] {
					filtered = append(filtered, vm)
				}
			}
			vms = filtered
		} else {
			vms = []model.VM{}
		}
	}

	// 同步 libvirt 状态：一次 RPC 拉取所有域状态，避免逐个查询
	stateMap, err := h.Virt.GetAllDomainStates()
	if err == nil && stateMap != nil {
		for i := range vms {
			// 只回写有变化的行：列表是热路径，逐行无条件 UPDATE 是 N+1 写（全量审计 P1）
			if s, ok := stateMap[vms[i].Name]; ok && s != vms[i].Status {
				vms[i].Status = s
				if err := h.DB.Model(&vms[i]).Update("status", s).Error; err != nil {
					LogError(c, err)
				}
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

	Success(c, gin.H{
		"total": len(vms),
		"items": vms,
		"perf":  perf,
	})
}

// GetVM 获取虚拟机详情
func (h *VMHandler) GetVM(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}

	// 惰性回填 vms.ip（与 ListVMs 同一节流），详情页与 SSH 白名单用到的 IP 才不会长期过期
	h.syncVMIPs()

	var vm model.VM
	if err := h.DB.Preload("Host").First(&vm, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}
	if !vmVisible(c, h.DB, vm.ID) {
		Fail(c, http.StatusNotFound, "虚拟机不存在")
		return
	}

	// 同步 libvirt 状态（读接口的尽力而为回写，失败重试并留痕，不阻断查询）
	if state, err := h.Virt.GetDomainState(vm.Name); err == nil && state != "" {
		vm.Status = state
		dbx.PersistBestEffort(h.DB, "GetVM 同步状态", func() error {
			return h.DB.Model(&vm).Update("status", state).Error
		})
	}

	Success(c, vm)
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
	Accepted(c, "任务已提交", gin.H{"task_id": task.ID})
}

// PauseVM 暂停虚拟机（对应 virsh suspend）。
func (h *VMHandler) PauseVM(c *gin.Context) {
	vm, ok := h.findVM(c)
	if !ok {
		return
	}
	if !h.guardVMIdle(c, vm.ID) {
		return
	}
	release, ok := h.lockVM(c, vm.ID)
	if !ok {
		return
	}
	defer release()
	if err := h.Virt.PauseDomain(vm.Name); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	dbx.PersistBestEffort(h.DB, "pause_vm 回写状态", func() error {
		return h.DB.Model(&vm).Update("status", model.VMStatusPaused).Error
	})
	Success(c, gin.H{"vm": vm.Name, "message": "虚拟机已暂停"})
}

// ResumeVM 恢复已暂停的虚拟机（对应 virsh resume）。
func (h *VMHandler) ResumeVM(c *gin.Context) {
	vm, ok := h.findVM(c)
	if !ok {
		return
	}
	if !h.guardVMIdle(c, vm.ID) {
		return
	}
	release, ok := h.lockVM(c, vm.ID)
	if !ok {
		return
	}
	defer release()
	if err := h.Virt.ResumeDomain(vm.Name); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	dbx.PersistBestEffort(h.DB, "resume_vm 回写状态", func() error {
		return h.DB.Model(&vm).Update("status", model.VMStatusRunning).Error
	})
	Success(c, gin.H{"vm": vm.Name, "message": "虚拟机已恢复"})
}

// SetVcpu 调整 CPU 核数（对应 virsh setvcpus），同步 DB。
// 停机态通过重 define 修改持久配置（setvcpus CONFIG 无法超 <vcpu> 上限）；
// 运行态走 live API（仅可调至启动时最大核数以内，超出提示关机）。
func (h *VMHandler) SetVcpu(c *gin.Context) {
	vm, ok := h.findVM(c)
	if !ok {
		return
	}
	if !h.guardVMIdle(c, vm.ID) {
		return
	}
	release, ok := h.lockVM(c, vm.ID)
	if !ok {
		return
	}
	defer release()

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
	// libvirt 侧已生效，DB 回写失败 = 持久配置漂移（vms.v_cpu 才是克隆/容量统计/备份还原的依据）：
	// 不能只 LogError 让前端看到纯成功，否则运维无从知道要补一次同步。
	// 列名必须写结构体字段对应的 v_cpu（不是字段名 vcpu），写错会每次都回写失败且同样被静默吞掉。
	if err := h.DB.Model(&vm).Update("v_cpu", req.VCPU).Error; err != nil {
		LogError(c, fmt.Errorf("[配置漂移-需同步] 虚拟机 %s 的 vCPU 已在 libvirt 调整为 %d，但回写数据库失败: %w",
			vm.Name, req.VCPU, err))
		Created(c, "vCPU 已在虚拟机上生效，但记录到数据库失败，请重试或联系管理员同步",
			gin.H{"vm": vm.Name, "vcpu": req.VCPU, "db_synced": false})
		return
	}
	Success(c, gin.H{"vm": vm.Name, "vcpu": req.VCPU, "db_synced": true})
}

// SetMemory 调整内存（对应 virsh setmem），同步 DB。
// 停机态通过重 define 修改持久配置；运行态走 live API（仅可调至启动时最大内存以内）。
func (h *VMHandler) SetMemory(c *gin.Context) {
	vm, ok := h.findVM(c)
	if !ok {
		return
	}
	if !h.guardVMIdle(c, vm.ID) {
		return
	}
	release, ok := h.lockVM(c, vm.ID)
	if !ok {
		return
	}
	defer release()

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
	// 同 SetVcpu：回写失败必须显式暴露为「部分成功」，见该函数注释。
	if err := h.DB.Model(&vm).Update("memory_mb", req.MemoryMB).Error; err != nil {
		LogError(c, fmt.Errorf("[配置漂移-需同步] 虚拟机 %s 的内存已在 libvirt 调整为 %dMB，但回写数据库失败: %w",
			vm.Name, req.MemoryMB, err))
		Created(c, "内存已在虚拟机上生效，但记录到数据库失败，请重试或联系管理员同步",
			gin.H{"vm": vm.Name, "memory_mb": req.MemoryMB, "db_synced": false})
		return
	}
	Success(c, gin.H{"vm": vm.Name, "memory_mb": req.MemoryMB, "db_synced": true})
}

// SetAutostart 设置开机自启（对应 virsh autostart）。
func (h *VMHandler) SetAutostart(c *gin.Context) {
	vm, ok := h.findVM(c)
	if !ok {
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

// GetVMStats 返回虚拟机实时性能统计（服务端差分计算 CPU/IO 速率）。
func (h *VMHandler) GetVMStats(c *gin.Context) {
	vm, ok := h.findVM(c)
	if !ok {
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
	if !vmVisible(c, h.DB, src.ID) {
		Fail(c, http.StatusNotFound, "虚拟机不存在")
		return
	}
	// 克隆期间禁止源机被并发改动（双开提交会克隆出两份脏卷）
	release, ok := h.lockVM(c, src.ID)
	if !ok {
		return
	}
	defer release()
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
	Accepted(c, "任务已提交", gin.H{"task_id": task.ID})
}

// StartVM 启动虚拟机
func (h *VMHandler) StartVM(c *gin.Context) {
	vm, ok := h.findVM(c)
	if !ok {
		return
	}

	if !h.guardVMIdle(c, vm.ID) {
		return
	}
	release, ok := h.lockVM(c, vm.ID)
	if !ok {
		return
	}
	defer release()

	// 调用 libvirt 启动（后台可能正跑 delete_vm/stop_vm，上面已先挡一层）
	if err := h.Virt.StartDomain(vm.Name); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	// 更新状态（写失败重试并留痕：状态漂移会让仪表盘统计与批量判断失真）
	dbx.PersistBestEffort(h.DB, "start_vm 回写状态", func() error {
		return h.DB.Model(&vm).Update("status", model.VMStatusRunning).Error
	})

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
	vm, ok := h.findVM(c)
	if !ok {
		return
	}
	release, ok := h.lockVM(c, vm.ID)
	if !ok {
		return
	}
	defer release()
	payload := map[string]interface{}{"vm_id": vm.ID}
	userID, username := taskUserFromContext(c)
	vmID := vm.ID
	task, err := h.Tasks.Submit("stop_vm", "停止虚拟机 "+vm.Name, payload, userID, username, vm.Name, &vmID)
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "提交任务失败", err)
		return
	}
	Accepted(c, "任务已提交", gin.H{"task_id": task.ID})
}

// lockVM 获取该虚拟机的进程内操作互斥锁（非阻塞）。
// 失败已写好 409 响应，调用方直接 return；成功必须 defer release()。
//
// 与 guardVMIdle 互补而非替代：锁拦住「同一瞬间并发进来的两个请求」（双开、误触、
// 脚本重放最常见的形态），guardVMIdle 拦住「HTTP 已返回但后台任务仍在跑」的时间窗。
func (h *VMHandler) lockVM(c *gin.Context, vmID uint) (func(), bool) {
	release, ok := vmlock.Try(vmID)
	if !ok {
		Fail(c, http.StatusConflict, "该虚拟机有操作正在进行，请稍后再试")
		return nil, false
	}
	return release, true
}

// guardVMIdle 校验该 VM 当前没有未终结的异步任务（pending/running），返回 true 表示可以下发。
//
// restart 是同步下发 libvirt reboot 的写操作：若此刻后台正跑 delete_vm/clone/stop_vm，
// 会对正在 undefine/迁移的域发 reboot，产生不可预期的域状态与脏卷，因此必须先挡住。
// 查询失败一律 fail-closed（拒绝下发）：DB 抖动时宁可让用户再点一次，也不能并发写域。
func (h *VMHandler) guardVMIdle(c *gin.Context, vmID uint) bool {
	var n int64
	err := h.DB.Model(&model.Task{}).
		Where("vm_id = ? AND status IN ?", vmID, []string{model.TaskStatusPending, model.TaskStatusRunning}).
		Count(&n).Error
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "检查虚拟机任务状态失败", err)
		return false
	}
	if n > 0 {
		Fail(c, http.StatusConflict, "该虚拟机有任务正在执行，请稍后再试")
		return false
	}
	return true
}

// RestartVM 重启虚拟机（对应 virsh reboot，需已运行）。
// 与 StopVM/DeleteVM 保持一致：下发前过 submitTaskGuard + guardVMIdle；
// 成功后按既有写法回写 vms.status——不回写会让状态机与 libvirt 实际状态不一致，
// 仪表盘统计与批量操作判断全部失真。
func (h *VMHandler) RestartVM(c *gin.Context) {
	if !h.submitTaskGuard(c) {
		return
	}
	vm, ok := h.findVM(c)
	if !ok {
		return
	}
	if !h.guardVMIdle(c, vm.ID) {
		return
	}
	release, ok := h.lockVM(c, vm.ID)
	if !ok {
		return
	}
	defer release()

	// 调用 libvirt 重启
	if err := h.Virt.RebootDomain(vm.Name); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	// 回写状态：reboot 成功后域处于 running（常量值带空格，勿写字面量）
	// libvirt 已重启成功，DB 回写失败不翻成失败响应，只留痕（与 ListVMs 状态回写同口径）
	if err := h.DB.Model(&vm).Update("status", model.VMStatusRunning).Error; err != nil {
		LogError(c, err)
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
	vm, ok := h.findVM(c)
	if !ok {
		return
	}
	release, ok := h.lockVM(c, vm.ID)
	if !ok {
		return
	}
	defer release()
	payload := map[string]interface{}{"vm_id": vm.ID}
	userID, username := taskUserFromContext(c)
	vmID := vm.ID
	task, err := h.Tasks.Submit("delete_vm", "删除虚拟机 "+vm.Name, payload, userID, username, vm.Name, &vmID)
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "提交任务失败", err)
		return
	}
	Accepted(c, "任务已提交", gin.H{"task_id": task.ID})
}
