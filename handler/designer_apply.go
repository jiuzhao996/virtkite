// designer_apply.go：架构设计器——落地（全量 apply）与按差异收敛（reconcile）执行。
// 落地/收敛共用的异步运行态（内存 + tasks 表双写）、goroutine 编排、逐节点供给
// (net/vm/container)、下线与重启、进度查询。漂移对比见 designer_drift.go。
package handler

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/dbx"
	"github.com/jiuzhao/vmops/service/secretbox"
	"github.com/jiuzhao/vmops/service/tasks"
	"github.com/jiuzhao/vmops/service/virt"
)

// ── 落地 / 收敛（M3：容器栈与 VM 实际落地；DE3：按差异收敛）──────────

// dsgApplyState 一次落地/收敛的运行态（内存 + tasks 表双写）。
type dsgApplyState struct {
	Status  string   `json:"status"` // running | success | failed
	Steps   []string `json:"steps"`
	Error   string   `json:"error,omitempty"`
	Mode    string   `json:"mode,omitempty"` // apply（全量落地）| reconcile（按差异收敛）
	Started time.Time
	TaskID  uint // 关联 tasks 表记录（AD1：任务中心可见 + 重启后状态可查）
	// ExpectedSteps 进度粗估分母（操作数×2 + 1），running 时按 len(Steps)/它折算
	ExpectedSteps int
}

var (
	dsgApplyMu   sync.Mutex
	dsgApplyRuns = map[string]*dsgApplyState{}
)

// dsgOp 一个落地/收敛操作单元。Action 用 containerlab 枚举；Node 是期望节点；
// Reason 是「为什么做这个动作」的人读理由（进步骤日志与任务结果）。
type dsgOp struct {
	Action string
	Node   dsgNode
	Reason string
}

// loadPlanForApply 读计划文件并把请求体里的 VM 口令按节点 id overlay 上去（不落盘）。
// 请求体可选（纯容器栈计划可无 body）。失败时已写好响应，返回 ok=false。
func (h *DesignerHandler) loadPlanForApply(c *gin.Context, id string) (dsgPlan, bool) {
	raw, err := os.ReadFile(filepath.Join(designerDir(), id+".json"))
	if err != nil {
		Fail(c, http.StatusNotFound, "计划不存在")
		return dsgPlan{}, false
	}
	var p dsgPlan
	if json.Unmarshal(raw, &p) != nil {
		Fail(c, http.StatusInternalServerError, "计划解析失败")
		return dsgPlan{}, false
	}
	var credBody struct {
		Credentials map[string]struct {
			SSHUser   string `json:"ssh_user"`
			SSHSecret string `json:"ssh_secret"`
		} `json:"credentials"`
	}
	if body, rerr := c.GetRawData(); rerr == nil && len(body) > 0 {
		if json.Unmarshal(body, &credBody) != nil {
			Fail(c, http.StatusBadRequest, "请求体格式非法（应为 {credentials:{节点id:{ssh_user,ssh_secret}}}）")
			return dsgPlan{}, false
		}
	}
	for i := range p.Nodes {
		if cred, ok := credBody.Credentials[p.Nodes[i].ID]; ok {
			if cred.SSHUser != "" {
				p.Nodes[i].SSHUser = cred.SSHUser
			}
			p.Nodes[i].SSHSecret = cred.SSHSecret
		}
	}
	return p, true
}

// startRun 落地/收敛的公共入口：登记运行态（内存 + tasks 表）后起 goroutine 执行 ops。
// 同一计划已有 running 任务时返回 409（apply 与 reconcile 共用一把运行位，互斥）。
// 用户归属在提交时从 HTTP 上下文取定并传进 goroutine，不在共享 handler 上回写字段
// （避免两个计划并发落地时对 h.UserID 的数据竞争）。goroutine 自带 recover（AGENTS 并发规范）。
func (h *DesignerHandler) startRun(c *gin.Context, p dsgPlan, ops []dsgOp, taskType, title, mode string) {
	id := p.ID
	userID, username := taskUserFromContext(c)
	if username == "" {
		username = "designer"
	}
	dsgApplyMu.Lock()
	if st := dsgApplyRuns[id]; st != nil && st.Status == "running" {
		dsgApplyMu.Unlock()
		Fail(c, http.StatusConflict, "该计划正在落地/收敛中")
		return
	}
	st := &dsgApplyState{Status: "running", Mode: mode, Started: time.Now(), ExpectedSteps: len(ops)*2 + 1}
	// AD1：落地同时落 tasks 表（不排队，仅记录）——任务中心可见，服务重启后
	// apply-status 回退查这条记录，进度不再随进程丢失
	if h.DB != nil {
		pl, _ := json.Marshal(map[string]string{"plan_id": id})
		t := model.Task{Type: taskType, Title: title, Status: "running", Payload: string(pl), UserID: userID, Username: username}
		if err := h.DB.Create(&t).Error; err == nil {
			st.TaskID = t.ID
		}
	}
	dsgApplyRuns[id] = st
	dsgApplyMu.Unlock()

	go func(plan dsgPlan, ops []dsgOp, st *dsgApplyState) {
		defer func() {
			if r := recover(); r != nil {
				st.Status = "failed"
				st.Error = "应用协程异常（详见服务日志）"
				log.Printf("[designer] 计划 %s %s panic: %v\n%s", plan.ID, st.Mode, r, debug.Stack())
			}
			// AD1：goroutine 收口时把最终状态同步进 tasks 表（正常/失败/panic 三路都走这）。
			// 不依赖前端轮询触发的懒同步——用户中途关页面也要有终态落库。
			h.syncApplyTask(st)
		}()
		h.runOps(&plan, ops, st, userID, username)
	}(p, ops, st)

	Accepted(c, title+"已启动", gin.H{"id": id, "mode": mode, "actions": len(ops)})
}

// runOps 执行操作序列：先删除（释放资源、为重建腾位）→ 再新增（按依赖排序）→ 最后重启。
//
// 幂等/可重入：任何一步失败即置 failed 并返回，已完成的步骤与内存态保留，前端可重试。
// 重试时收敛会重新 computeDiff——已完成的 added 不再出现在差异里，deleted 的资源现实
// 已无也不再出现，天然幂等；recreated 的「先删后建」中途失败，重试时会看到「现实已无 →
// added」，等价于继续把建做下去。
func (h *DesignerHandler) runOps(plan *dsgPlan, ops []dsgOp, st *dsgApplyState, userID *uint, username string) {
	// 前置守卫：要落地（非删除/重启）的 VM 节点若配了应用/playbook，必须有口令——
	// 两类动作都要用口令（app 加密入 payload / playbook 托管后走凭据通道），否则建出
	// 空口令机器必然失败。fail-fast 比半途失败省资源。
	for _, op := range ops {
		n := op.Node
		if n.Kind == "vm" && op.Action != dsgActionDeleted && op.Action != dsgActionRestarted &&
			(len(n.Apps) > 0 || len(n.Playbooks) > 0) && n.SSHSecret == "" {
			st.Status = "failed"
			st.Error = "VM「" + n.Name + "」配了应用/playbook 但未提供 SSH 口令（落地时需在页面填入）"
			return
		}
	}

	var dels, adds, restarts []dsgOp
	for _, op := range ops {
		switch op.Action {
		case dsgActionDeleted:
			dels = append(dels, op)
		case dsgActionRecreated:
			dels = append(dels, op) // 重建 = 先删
			adds = append(adds, op) // 再建
		case dsgActionAdded:
			adds = append(adds, op)
		case dsgActionRestarted:
			restarts = append(restarts, op)
		}
	}

	// 1) 删除
	for _, op := range dels {
		st.Steps = append(st.Steps, "下线 "+nodeResource(op.Node)+"（"+dsgKindLabel(op.Node.Kind)+"）："+op.Reason)
		if err := h.teardownNode(op, st, userID, username); err != nil {
			st.Status = "failed"
			st.Error = "下线 " + nodeResource(op.Node) + " 失败：" + err.Error()
			return
		}
	}

	// 2) 新增（按依赖排序：net → vm → container）
	addNodes := make([]dsgNode, 0, len(adds))
	for _, op := range adds {
		addNodes = append(addNodes, op.Node)
	}
	for _, n := range orderNodes(addNodes, plan.Links) {
		if err := h.provisionNode(plan, n, st, userID, username); err != nil {
			st.Status = "failed"
			st.Error = dsgKindLabel(n.Kind) + " " + nodeResource(n) + " 落地失败：" + err.Error()
			return
		}
	}

	// 3) 重启
	for _, op := range restarts {
		st.Steps = append(st.Steps, "重启 "+nodeResource(op.Node)+"："+op.Reason)
		if err := h.restartNode(op, st); err != nil {
			st.Status = "failed"
			st.Error = "重启 " + nodeResource(op.Node) + " 失败：" + err.Error()
			return
		}
	}

	st.Status = "success"
	// DE2：成功即覆盖基线——「已落地基线」= 本次落地/收敛时的期望。下次三方对比的
	// 基线一侧即此；手改画布（未落地）不会动它。
	h.saveSnapshot(*plan)
}

// provisionNode 落地单个节点（net/vm/container）。原 Apply 内联循环抽出，供全量落地
// 与按差异收敛共用；本身对已存在的网络幂等（provisionNet 同名跳过）。
func (h *DesignerHandler) provisionNode(p *dsgPlan, n dsgNode, st *dsgApplyState, userID *uint, username string) error {
	switch n.Kind {
	case "container":
		st.Steps = append(st.Steps, "部署栈 "+n.Ref+"（"+n.Name+"）…")
		meta, sraw, err := parseStackMeta(filepath.Join(stacksDir(), n.Ref+".yml"))
		if err != nil {
			return fmt.Errorf("栈不存在：%s", n.Ref)
		}
		target := config.DataPath("stacks", n.Ref)
		if err := os.MkdirAll(target, 0o755); err != nil {
			return fmt.Errorf("创建部署目录失败: %w", err)
		}
		_ = os.WriteFile(filepath.Join(target, "docker-compose.yml"), sraw, 0o644)
		if err := h.Docker.ComposeUpFromFile(target); err != nil {
			return fmt.Errorf("栈 %s 部署失败: %w", n.Ref, err)
		}
		st.Steps = append(st.Steps, "✓ "+n.Ref+" 完成（"+meta.Desc+"）")
	case "vm":
		vmID, err := h.provisionVM(p, n, st, userID, username)
		if err != nil {
			return err
		}
		st.Steps = append(st.Steps, "✓ VM "+n.Name+" 就绪（id="+strconv.FormatUint(uint64(vmID), 10)+"）")
	case "net":
		created, nerr := h.provisionNet(n)
		if nerr != nil {
			return nerr
		}
		if created {
			st.Steps = append(st.Steps, "✓ 网络 "+n.Name+" 已创建（NAT）")
		} else {
			st.Steps = append(st.Steps, "· 网络 "+n.Name+" 已存在，跳过")
		}
	default:
		st.Steps = append(st.Steps, "跳过 "+n.Name+"（未知节点类型）")
	}
	return nil
}

// teardownNode 下线单个节点：VM 走 delete_vm 任务（异步 + 等终态），容器栈 compose down，
// 网络 undefine。资源已不存在视为成功（幂等，供收敛重入）。
func (h *DesignerHandler) teardownNode(op dsgOp, st *dsgApplyState, userID *uint, username string) error {
	n := op.Node
	switch n.Kind {
	case "vm":
		var vm model.VM
		if err := h.DB.Where("name = ?", n.Name).First(&vm).Error; err != nil {
			st.Steps = append(st.Steps, "· VM "+n.Name+" 已不存在，跳过下线")
			return nil
		}
		vmID := vm.ID
		task, err := h.Tasks.Submit("delete_vm", "设计器下线 VM "+vm.Name, map[string]interface{}{"vm_id": vm.ID}, userID, username, vm.Name, &vmID)
		if err != nil {
			return fmt.Errorf("提交删除任务失败: %w", err)
		}
		st.Steps = append(st.Steps, "删除任务已提交（task="+strconv.FormatUint(uint64(task.ID), 10)+"），等待完成…")
		if _, err := h.waitTask(task.ID, 15*time.Minute); err != nil {
			return err
		}
		st.Steps = append(st.Steps, "✓ VM "+vm.Name+" 已删除")
	case "container":
		if h.Docker == nil {
			return fmt.Errorf("容器服务不可用")
		}
		if err := h.Docker.ComposeAction(n.Ref, "down"); err != nil {
			return err
		}
		st.Steps = append(st.Steps, "✓ 栈 "+n.Ref+" 已下线")
	case "net":
		if h.Virt == nil {
			return fmt.Errorf("虚拟化服务不可用")
		}
		if nets, err := h.Virt.ListNetworks(); err == nil {
			found := false
			for _, e := range nets {
				if e.Name == n.Name {
					found = true
					break
				}
			}
			if !found {
				st.Steps = append(st.Steps, "· 网络 "+n.Name+" 已不存在，跳过")
				return nil
			}
		}
		if err := h.Virt.DeleteNetwork(n.Name); err != nil {
			return err
		}
		st.Steps = append(st.Steps, "✓ 网络 "+n.Name+" 已删除")
	}
	return nil
}

// restartNode 重启单个节点：VM 运行中走 reboot，异常态（paused/error）走 destroy+start
// 兜底；容器栈走 compose restart。用于收敛里的 restarted 动作。
func (h *DesignerHandler) restartNode(op dsgOp, st *dsgApplyState) error {
	n := op.Node
	switch n.Kind {
	case "vm":
		var vm model.VM
		if err := h.DB.Where("name = ?", n.Name).First(&vm).Error; err != nil {
			st.Steps = append(st.Steps, "· VM "+n.Name+" 已不存在，跳过重启")
			return nil
		}
		if vm.Status == model.VMStatusRunning {
			if err := h.Virt.RebootDomain(vm.Name); err != nil {
				return err
			}
		} else {
			// paused/error 下 start 会失败，先硬停再起（恢复异常态）
			_ = h.Virt.DestroyDomain(vm.Name)
			if err := h.Virt.StartDomain(vm.Name); err != nil {
				return err
			}
		}
		// 状态回写失败留痕（AGENTS：DB 写不静默吞）——重启已由 libvirt 生效
		dbx.PersistBestEffort(h.DB, "designer 收敛重启 VM 回写状态", func() error {
			return h.DB.Model(&model.VM{}).Where("id = ?", vm.ID).Update("status", model.VMStatusRunning).Error
		})
		st.Steps = append(st.Steps, "✓ VM "+vm.Name+" 已重启")
	case "container":
		if h.Docker == nil {
			return fmt.Errorf("容器服务不可用")
		}
		if err := h.Docker.ComposeAction(n.Ref, "restart"); err != nil {
			return err
		}
		st.Steps = append(st.Steps, "✓ 栈 "+n.Ref+" 已重启")
	}
	return nil
}

// Apply POST /api/designer/plans/:id/apply（admin）——全量落地：把计划内所有节点按依赖
// 顺序落地（net→vm→container）。VM 凭据随请求体一次性携带（不落计划文件）。
// 进度内存态（GET /apply-status 轮询）；goroutine 自带 recover（AGENTS 并发规范）。
func (h *DesignerHandler) Apply(c *gin.Context) {
	if !roleIsAdmin(c) {
		Fail(c, http.StatusForbidden, "计划应用仅管理员可用")
		return
	}
	id := c.Param("id")
	if !dsgIDRe.MatchString(id) {
		Fail(c, http.StatusBadRequest, "计划 ID 非法")
		return
	}
	p, ok := h.loadPlanForApply(c, id)
	if !ok {
		return
	}
	// 全量落地 = 每个节点都当作「需要落地」（provisionNet 对已存在网络幂等跳过）
	ops := make([]dsgOp, 0, len(p.Nodes))
	for _, n := range p.Nodes {
		ops = append(ops, dsgOp{Action: dsgActionAdded, Node: n, Reason: "全量落地"})
	}
	h.startRun(c, p, ops, "designer_apply", "落地架构 "+p.Name, "apply")
}

// Reconcile POST /api/designer/plans/:id/reconcile（admin）——按差异收敛：先算三方差异，
// 只对 added/deleted/recreated/restarted 的资源执行，不动未变化的资源（deploy 即 reconcile，
// 不是每次全量重建）。收敛走与落地同一套异步任务管线与运行位（apply/reconcile 互斥）；
// 失败保留状态可重试。
func (h *DesignerHandler) Reconcile(c *gin.Context) {
	if !roleIsAdmin(c) {
		Fail(c, http.StatusForbidden, "按差异收敛仅管理员可用")
		return
	}
	id := c.Param("id")
	if !dsgIDRe.MatchString(id) {
		Fail(c, http.StatusBadRequest, "计划 ID 非法")
		return
	}
	p, ok := h.loadPlanForApply(c, id)
	if !ok {
		return
	}
	// 用带凭据的计划重算差异（凭据来自请求体，磁盘计划里没有）
	_, ops := reconcilePlan(&p, loadLatestSnapshot(id), h.loadReality(), id)
	if len(ops) == 0 {
		Success(c, gin.H{"message": "已与现实一致，无需收敛", "actions": 0})
		return
	}
	h.startRun(c, p, ops, "designer_reconcile", "收敛架构 "+p.Name, "reconcile")
}

// ReconcilePlan GET /api/designer/plans/:id/reconcile-plan（operator+）——收敛预演：
// 不执行，只返回「先列现状 → 算出的动作集合」（containerlab planApply 的 plan 阶段）。
func (h *DesignerHandler) ReconcilePlan(c *gin.Context) {
	id := c.Param("id")
	if !dsgIDRe.MatchString(id) {
		Fail(c, http.StatusBadRequest, "计划 ID 非法")
		return
	}
	res, _ := h.computeReconcile(id)
	Success(c, res)
}

// provisionNet 网络节点落地（D3）：libvirt 已存在同名网络则跳过（不覆盖用户网络），
// 否则按默认 NAT 模板定义并启动。节点 ref 若形如网段（10.10.0.0/24），取其第一个
// 可用地址作网关（当前模板固定 /24 掩码，不支持更宽网段）。
func (h *DesignerHandler) provisionNet(n dsgNode) (bool, error) {
	if h.Virt == nil {
		return false, fmt.Errorf("虚拟化服务不可用")
	}
	if !validateVMName(n.Name) {
		return false, fmt.Errorf("网络名 %q 非法（字母数字与下划线/连字符）", n.Name)
	}
	if nets, err := h.Virt.ListNetworks(); err == nil {
		for _, e := range nets {
			if e.Name == n.Name {
				return false, nil // 已存在：不覆盖
			}
		}
	}
	xml := virt.NetworkXMLFromParams(n.Name, "", netGatewayFromCIDR(n.Ref))
	if xml == "" {
		return false, fmt.Errorf("生成网络定义失败")
	}
	if err := h.Virt.DefineNetwork(xml); err != nil {
		return false, err
	}
	return true, nil
}

// netGatewayFromCIDR 若是 CIDR（如 10.10.0.0/24）返回其第一个可用地址作网关，否则空串。
func netGatewayFromCIDR(s string) string {
	_, ipnet, err := net.ParseCIDR(strings.TrimSpace(s))
	if err != nil {
		return ""
	}
	ip := ipnet.IP.To4()
	if ip == nil {
		return ""
	}
	next := net.IPv4(ip[0], ip[1], ip[2], ip[3]+1)
	if !ipnet.Contains(next) {
		return ""
	}
	return next.String()
}

// resolveVMNetwork 找与本 VM 相连的 net 节点，返回其节点名（= libvirt 网络名，provisionNet 即按此建网）；
// 无 net 连线时回落 "default"。
func resolveVMNetwork(p *dsgPlan, n dsgNode) string {
	if p == nil {
		return "default"
	}
	netName := make(map[string]string)
	for _, nd := range p.Nodes {
		if nd.Kind == "net" && nd.Name != "" {
			netName[nd.ID] = nd.Name
		}
	}
	for _, l := range p.Links {
		if l.From == n.ID {
			if name, ok := netName[l.To]; ok {
				return name
			}
		}
		if l.To == n.ID {
			if name, ok := netName[l.From]; ok {
				return name
			}
		}
	}
	return "default"
}

// provisionVM VM 节点落地：create_vm 建机（云镜像 + cloud-init + 设计网络 + provision 块）→
// 由 create_vm executor 统一完成「开机 → 等 IP → 托管凭据」→ 本函数再逐个装应用 / 跑 playbook。
// create_vm / app_install / ansible_run 均复用既有 executor（与页面操作同一套链路），本函数只做编排。
// userID/username 由调用方（goroutine 起手时）从 HTTP 上下文取定传入，不再读共享 handler 字段。
func (h *DesignerHandler) provisionVM(p *dsgPlan, n dsgNode, st *dsgApplyState, userID *uint, username string) (uint, error) {
	// 前置校验：云镜像必须存在于镜像库（images 表按 id）。
	// n.Ref 来自计划文件（用户可控的字符串），必须先解析成 uint 再交给 GORM——
	// 直传非纯数字字符串会被 GORM 当原始 SQL 片段拼进 WHERE（注入面，见 AGENTS 第 8 条）。
	imgID, ok := parseID(n.Ref)
	if !ok {
		return 0, fmt.Errorf("云镜像 ID 非法（%s）", n.Ref)
	}
	var img model.Image
	if err := h.DB.First(&img, imgID).Error; err != nil {
		return 0, fmt.Errorf("云镜像不存在（id=%s）", n.Ref)
	}
	pool := n.Pool
	if pool == "" {
		pool = tasks.DefaultStoragePoolResolver()
	}
	vcpu := n.VCPU
	if vcpu <= 0 {
		vcpu = 1
	}
	mem := n.MemoryMB
	if mem <= 0 {
		mem = 1024
	}
	sshUser := n.SSHUser
	if sshUser == "" {
		sshUser = "root"
	}

	// 1) 提交 create_vm（payload 与镜像管理页「基于云镜像创建」同构：source_image_id 引用不拷贝）。
	// 网络接画布上与本 VM 相连的 net 节点（无连线回落 default）；口令以密文进 provision 块，
	// 由 executor 解密后做 cloud-init seed 并托管到 vm_credentials——payload/日志不留明文。
	var host model.Host
	if err := h.DB.First(&host).Error; err != nil {
		return 0, fmt.Errorf("请先登记宿主机")
	}
	ciSpec := &virt.CloudInitSpec{Hostname: n.Name, User: sshUser, Password: n.SSHSecret}
	if verr := validateCloudInitText(ciSpec); verr != nil {
		return 0, fmt.Errorf("VM 节点 %s 的 cloud-init 配置不合法: %w", n.Name, verr)
	}
	netName := resolveVMNetwork(p, n)
	prov := map[string]interface{}{"start": true, "wait_ip": true, "ssh_user": sshUser}
	if n.SSHSecret != "" {
		cipherB64, saltHex, err := secretbox.SealWithMaster(h.VMCred.MasterSecret, n.SSHSecret)
		if err != nil {
			return 0, fmt.Errorf("SSH 口令加密失败: %w", err)
		}
		prov["password_enc"] = cipherB64
		prov["salt"] = saltHex
	}
	createPayload := map[string]interface{}{
		"name":         n.Name,
		"host_id":      host.ID,
		"storage_pool": pool,
		"vcpu":         vcpu,
		"memory_mb":    mem,
		"disks":        []map[string]interface{}{{"source_image_id": img.ID, "create_gb": int(img.SizeGB)}},
		"interfaces":   []map[string]interface{}{{"type": "network", "source": netName, "model": "virtio"}},
		"network":      netName,
		"cloud_init":   ciSpec,
		"provision":    prov,
	}
	task, err := h.Tasks.Submit("create_vm", "设计器创建 VM "+n.Name, createPayload, userID, username, n.Name, nil)
	if err != nil {
		return 0, fmt.Errorf("提交建机任务失败: %w", err)
	}
	st.Steps = append(st.Steps, "建机任务已提交（task="+strconv.FormatUint(uint64(task.ID), 10)+"），等待建机+初始化…")
	// 建机任务已含「等 IP」，窗口放宽到 12 分钟
	result, err := h.waitTask(task.ID, 12*time.Minute)
	if err != nil {
		return 0, err
	}
	// create_vm 成功结果 {vm_id: N}
	var res struct {
		VMID uint `json:"vm_id"`
	}
	_ = json.Unmarshal([]byte(result), &res)
	vmID := res.VMID
	if vmID == 0 {
		return 0, fmt.Errorf("建机任务未返回 vm_id")
	}
	var vm model.VM
	if err := h.DB.First(&vm, vmID).Error; err != nil {
		return 0, fmt.Errorf("读取虚拟机记录失败: %w", err)
	}
	st.Steps = append(st.Steps, "VM 运行中，IP="+vm.IP+"（凭据已自动托管）")
	if vmID == 0 {
		return 0, fmt.Errorf("建机任务成功但未返回 vm_id")
	}

	// 2) app_install 逐个装（口令边界就地加密；凭据不落明文）
	for _, appID := range n.Apps {
		cipherB64, saltHex, err := secretbox.SealWithMaster(h.VMCred.MasterSecret, n.SSHSecret)
		if err != nil {
			return vmID, fmt.Errorf("SSH 口令加密失败: %w", err)
		}
		payload := map[string]interface{}{
			"app_id":       appID,
			"vm_id":        vmID,
			"host":         vm.IP,
			"port":         22,
			"user":         sshUser,
			"password_enc": cipherB64,
			"salt":         saltHex,
		}
		t, err := h.Tasks.Submit("app_install", "设计器安装 "+appID+" 到 "+n.Name, payload, userID, username, n.Name, &vmID)
		if err != nil {
			return vmID, fmt.Errorf("提交应用安装失败（%s）: %w", appID, err)
		}
		st.Steps = append(st.Steps, "安装 "+appID+"…（task="+strconv.FormatUint(uint64(t.ID), 10)+"）")
		if _, err := h.waitTask(t.ID, 15*time.Minute); err != nil {
			return vmID, err
		}
		st.Steps = append(st.Steps, "✓ "+appID+" 安装完成")
	}

	// 5) playbook 逐个跑（P4-S3 编排联动）：复用 ansible_run 任务管线（凭据解析/
	// inventory 生成/RECAP 全在 executor），这里只编排。playbook 跑的是初始化/加固/
	// 优化类动作，与应用安装互补
	for _, pbID := range n.Playbooks {
		if !playbookIDRe.MatchString(pbID) {
			return vmID, fmt.Errorf("playbook ID 非法: %s", pbID)
		}
		if _, perr := os.Stat(config.DataPath("ansible", "playbooks", pbID+".yml")); perr != nil {
			return vmID, fmt.Errorf("playbook 不存在: %s", pbID)
		}
		t, err := h.Tasks.Submit("ansible_run", "设计器 playbook "+pbID+" → "+n.Name,
			map[string]interface{}{"targets": []uint{vmID}, "playbook": pbID}, userID, username, n.Name, &vmID)
		if err != nil {
			return vmID, fmt.Errorf("提交 playbook 执行失败（%s）: %w", pbID, err)
		}
		st.Steps = append(st.Steps, "执行 playbook "+pbID+"…（task="+strconv.FormatUint(uint64(t.ID), 10)+"）")
		if _, err := h.waitTask(t.ID, 25*time.Minute); err != nil {
			return vmID, err
		}
		st.Steps = append(st.Steps, "✓ "+pbID+" 完成")
	}
	return vmID, nil
}

// waitTask 轮询任务至终态：成功返回 result JSON（如 {"vm_id":N}）；失败/超时返回 error。
func (h *DesignerHandler) waitTask(taskID uint, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var t model.Task
		if err := h.DB.First(&t, taskID).Error; err != nil {
			return "", fmt.Errorf("任务记录读取失败: %w", err)
		}
		switch t.Status {
		case model.TaskStatusSuccess:
			return t.Result, nil
		case model.TaskStatusFailed:
			if t.Error != "" {
				return "", fmt.Errorf("%s", t.Error)
			}
			return "", fmt.Errorf("任务失败")
		}
		time.Sleep(3 * time.Second)
	}
	return "", fmt.Errorf("任务超时（%s）", timeout)
}

// syncApplyTask 把内存态同步进 tasks 表（AD1）。progress 按「步骤行数」粗估：
// 操作数×2 为满分母（每操作大约「开始+完成」两行），封顶 95 留终态写 100。
func (h *DesignerHandler) syncApplyTask(st *dsgApplyState) {
	if h.DB == nil || st.TaskID == 0 {
		return
	}
	vals := map[string]interface{}{
		"status": st.Status,
		"result": strings.Join(st.Steps, "\n"),
	}
	if st.Status == "success" {
		vals["progress"] = 100
	} else if st.Status == "failed" {
		vals["error"] = st.Error
	} else if st.ExpectedSteps > 0 {
		// 粗估进度封顶 95：终态 100 由成功分支写
		pct := len(st.Steps) * 95 / st.ExpectedSteps
		if pct > 95 {
			pct = 95
		}
		vals["progress"] = pct
	}
	if err := h.DB.Model(&model.Task{}).Where("id = ?", st.TaskID).Updates(vals).Error; err != nil {
		log.Printf("[designer] 同步落地任务 %d 失败: %v", st.TaskID, err)
	}
}

// ApplyStatus GET /api/designer/plans/:id/apply-status。
// 内存命中优先并懒同步进 tasks 表（轮询天然节流）；内存未命中（服务重启）回退
// 查 tasks 表最近一条该计划的 designer_apply / designer_reconcile 记录——重启后
// 状态与步骤不再丢失（落地与收敛共用同一状态端点）。
func (h *DesignerHandler) ApplyStatus(c *gin.Context) {
	id := c.Param("id")
	dsgApplyMu.Lock()
	st := dsgApplyRuns[id]
	dsgApplyMu.Unlock()
	if st != nil {
		h.syncApplyTask(st)
		Success(c, gin.H{"id": id, "status": st.Status, "steps": st.Steps, "error": st.Error, "mode": st.Mode})
		return
	}
	// 回退：查该计划最近一条落地/收敛任务（payload 含 plan_id）
	var t model.Task
	q := h.DB.Where("type IN ? AND payload LIKE ?", []string{"designer_apply", "designer_reconcile"}, `%"plan_id":"`+id+`"%`).
		Order("id DESC").First(&t)
	if q.Error != nil {
		Success(c, gin.H{"id": id, "status": "idle"})
		return
	}
	steps := []string{}
	if t.Result != "" {
		steps = strings.Split(t.Result, "\n")
	}
	// 任务中心视角的 cancelled/超时映射为 failed 语义（有 error 文案）
	status := t.Status
	errMsg := t.Error
	if status == "cancelled" {
		status = "failed"
		errMsg = errMsg + "（任务被取消）"
	}
	Success(c, gin.H{"id": id, "status": status, "steps": steps, "error": errMsg, "task_id": t.ID})
}
