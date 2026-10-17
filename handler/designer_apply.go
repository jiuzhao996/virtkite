// designer_apply.go：架构设计器——落地（全量 apply）与按差异收敛（reconcile）的 HTTP 入口、
// 异步运行态（内存 + tasks 表双写）、goroutine 编排、进度查询与计划加载。逐节点供给
// (net/vm/container) 见 designer_apply_provision.go，下线与重启见 designer_apply_lifecycle.go，
// 漂移对比见 designer_drift.go。
package handler

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
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
	// 与预览同源：前端「一键落地」的弹窗来自 GET /reconcile-plan，这里必须用同一套
	// 算法算动作，否则「预览显示无需动作、确认后却全量重建」（验收文档 R5/R6）。
	//   - 从没成功落地过（无基线）→ 计划内节点全部 added，即首次部署；
	//   - 已有基线 → 只补差异，已就绪的资源不动。
	// 每个分支的 provisionNode 还各自幂等（网络同名跳过、compose up -d 幂等、
	// VM 同名跳过），因此重复点「一键落地」不会重复建机、不产生回收站垃圾。
	_, ops := reconcilePlan(&p, loadLatestSnapshot(id), h.loadReality(), id)
	if len(ops) == 0 {
		Success(c, gin.H{"message": "计划内资源已全部就绪，无需重复落地", "actions": 0})
		return
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
	// 回退：查该计划最近一条落地/收敛任务（payload 含 plan_id）。
	// 不用 SQL LIKE：计划 id 互为子串时（e2e-1 / e2e-10）会串到别的计划；
	// 取最近 50 条再精确解析 plan_id（designer 任务量很小，代价可忽略）。
	var recent []model.Task
	var hit *model.Task
	if err := h.DB.Where("type IN ?", []string{"designer_apply", "designer_reconcile"}).
		Order("id DESC").Limit(50).Find(&recent).Error; err == nil {
		for i := range recent {
			var p struct {
				PlanID string `json:"plan_id"`
			}
			if json.Unmarshal([]byte(recent[i].Payload), &p) == nil && p.PlanID == id {
				hit = &recent[i]
				break
			}
		}
	}
	if hit == nil {
		Success(c, gin.H{"id": id, "status": "idle"})
		return
	}
	t := *hit
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
