package handler

// vm.go —— VM 查询类接口与共享 helper：列表 / 详情 / 实时性能，以及被 vm_* 各文件
// 复用的样板（findVM、lockVM、guardVMIdle、taskUserFromContext、submitTaskGuard、
// validateVMName）与处理器构造。生命周期异步入口见 vm_lifecycle.go（创建/克隆/删除）、
// 电源操作见 vm_power.go、规格调整见 vm_spec.go；设备/授权/XML/快照等各自成文件
// （vm_devices.go / vm_grant.go / vm_xml.go / vm_snapshot.go 等）。

import (
	"net/http"
	"regexp"
	"strconv"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/console"
	"github.com/jiuzhao/vmops/service/dbx"
	"github.com/jiuzhao/vmops/service/tasks"
	"github.com/jiuzhao/vmops/service/virt"
	"github.com/jiuzhao/vmops/service/vmlock"
	"github.com/jiuzhao/vmops/service/wsticket"
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
	// Tickets WS 一次性票据库（与 AuthMiddleware 校验侧同一实例，由 routes.go 装配）
	Tickets *wsticket.Store
}

// NewVMHandler 创建虚拟机处理器
func NewVMHandler(db *gorm.DB, taskMgr *tasks.Manager, sessions *console.Registry) *VMHandler {
	return &VMHandler{DB: db, Virt: virt.New(), Tasks: taskMgr, Sessions: sessions}
}

// MintWSTicket POST /api/vms/:id/ws-ticket —— 为 VM 终端/串口 WS 签发一次性短时票据。
//
// 为什么必须挂在 vms 组且为 POST：票据签发端点继承所在组的 RBAC。OperatorMiddleware 对
// viewer 只在「POST 且非 vnc-token」时 403；若做成 GET，viewer 会命中 GET 放行分支拿到
// terminal 票据，从而绕过「viewer 不得使用 SSH 终端/串口」的封锁。
func (h *VMHandler) MintWSTicket(c *gin.Context) {
	vmID, ok := paramID(c, "id")
	if !ok {
		return // paramID 已写 400
	}
	var vm model.VM
	if err := h.DB.First(&vm, vmID).Error; err != nil {
		Fail(c, http.StatusNotFound, "虚拟机不存在")
		return
	}
	// 授权决定可见性：非 admin 未持有效授权与不存在同响应
	if !vmVisible(c, h.DB, vm.ID) {
		Fail(c, http.StatusNotFound, "虚拟机不存在")
		return
	}
	if h.Tickets == nil {
		Fail(c, http.StatusServiceUnavailable, "连接凭证服务未就绪")
		return
	}
	tk, err := h.Tickets.Issue(c.GetUint("user_id"), wsticket.VMResource(strconv.FormatUint(uint64(vm.ID), 10)))
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "签发连接凭证失败", err)
		return
	}
	Success(c, gin.H{"ticket": tk, "expires_in": int(wsticket.TTL.Seconds())})
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

	// 实时性能：仅 running 采样，key 为 VM id（与 /dashboard/vm-perf 同口径，供列表页合并请求）。
	// 逐域一次 RPC，串行执行时 N 台机就是 N 个 RTT 叠加，列表页轮询明显变慢；
	// go-libvirt 连接支持并发多路 RPC（内部按序列号多路复用），这里并发采样，
	// map 写入互斥。单机采样失败静默跳过（保持原行为：缺该项前端显示 —）。
	perf := make(map[uint]gin.H, len(vms))
	var perfMu sync.Mutex
	var wg sync.WaitGroup
	for _, vm := range vms {
		if vm.Status != model.VMStatusRunning {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, err := h.Virt.GetDomainStats(vm.Name)
			if err != nil || st == nil {
				return
			}
			memPct := 0.0
			if st.GuestTotalKiB > 0 {
				memPct = float64(st.GuestUsedKiB) / float64(st.GuestTotalKiB) * 100
			} else if st.MemTotalKiB > 0 {
				memPct = float64(st.MemUsedKiB) / float64(st.MemTotalKiB) * 100
			}
			perfMu.Lock()
			perf[vm.ID] = gin.H{"cpu_percent": st.CpuPercent, "mem_pct": memPct}
			perfMu.Unlock()
		}()
	}
	wg.Wait()

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

// lockVM 获取该虚拟机的进程内操作互斥锁（非阻塞）。
// 失败已写好 409 响应，调用方直接 return；成功必须 defer release()。
//
// 与 guardVMIdle 互补而非替代：锁拦住「同一瞬间并发进来的两个请求」（双开、误触、
// 脚本重放最常见的形态），guardVMIdle 拦住「HTTP 已返回但后台任务仍在跑」的时间窗。
func (h *VMHandler) lockVM(c *gin.Context, vmID uint) (func(), bool) {
	return lockVM(c, vmID)
}

// guardVMIdle 校验该 VM 当前没有未终结的异步任务（pending/running），返回 true 表示可以下发。
//
// restart 是同步下发 libvirt reboot 的写操作：若此刻后台正跑 delete_vm/clone/stop_vm，
// 会对正在 undefine/迁移的域发 reboot，产生不可预期的域状态与脏卷，因此必须先挡住。
// 查询失败一律 fail-closed（拒绝下发）：DB 抖动时宁可让用户再点一次，也不能并发写域。
func (h *VMHandler) guardVMIdle(c *gin.Context, vmID uint) bool {
	return guardVMIdle(c, h.DB, vmID)
}

// lockVM 包级版：供非 VMHandler 承载的生命周期入口（导出/回收站恢复等）共用同一把
// 进程内互斥与同一套 409 文案。语义与 VMHandler 方法版完全一致。
func lockVM(c *gin.Context, vmID uint) (func(), bool) {
	release, ok := vmlock.Try(vmID)
	if !ok {
		Fail(c, http.StatusConflict, "该虚拟机有操作正在进行，请稍后再试")
		return nil, false
	}
	return release, true
}

// guardVMIdle 包级版：校验该 VM 无 pending/running 异步任务，失败响应与 fail-closed
// 语义同 VMHandler 方法版。
func guardVMIdle(c *gin.Context, db *gorm.DB, vmID uint) bool {
	var n int64
	err := db.Model(&model.Task{}).
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
