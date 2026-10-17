// designer_drift.go：架构设计器——漂移三方对比（计划/基线/现实）与漂移汇总巡检。
// 快照基线读写见 designer_snapshot.go，收敛动作预演与落地排序见 designer_reconcile.go，
// 真正的落地/收敛执行见 designer_apply.go。
package handler

import (
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
)

// 收敛动作枚举（对齐 containerlab 的 reconcile 动作集）。每个差异项都要带「为什么是
// 这个动作」的理由，而不是只给动作名——理由才是用户判断要不要收敛的依据。
const (
	dsgActionAdded     = "added"     // 期望有、现实无：创建
	dsgActionDeleted   = "deleted"   // 计划已移除、现实仍在（本计划创建）：下线
	dsgActionRecreated = "recreated" // 现实存在但规格与期望不符：重建（破坏性）
	dsgActionRestarted = "restarted" // 现实存在、规格相符但运行态异常：重启
	dsgActionNone      = "none"      // 无需动作（一致，或现实资源非本计划管理）
)

// 漂移侧别：区分「画布改了还没落地」与「现实被外部改了」——这两种漂移的处理方式
// 完全不同，必须在数据里分清，前端也要分组展示。
const (
	dsgSidePending  = "pending"  // 计划 vs 基线：用户改了画布但还没落地，收敛动作=落地
	dsgSideExternal = "external" // 基线 vs 现实：现实被外部改动，收敛动作=修复现实
)

// DriftItem 单条差异。三方对比的产物：
//   - Kind：差异分类（missing 计划/基线有现实无 | extra 现实有期望无 | mismatch 存在但规格/态不符）
//   - Action：收敛动作（containerlab 枚举），Reason 说明为什么是该动作
//   - Side：pending（画布改动未落地）| external（现实被外部改动）
type DriftItem struct {
	Kind     string `json:"kind"`               // missing | extra | mismatch
	Node     string `json:"node"`               // 资源名（VM 名 / 栈 id / 网络名）
	Name     string `json:"name,omitempty"`     // 展示名
	Resource string `json:"resource,omitempty"` // vm | container | net
	Action   string `json:"action"`             // added | deleted | recreated | restarted | none
	Reason   string `json:"reason"`             // 为什么是这个动作
	Side     string `json:"side"`               // pending | external
	Detail   string `json:"detail"`             // 人读描述
}

// DriftResult 一个计划的三方对比结果。
type DriftResult struct {
	PlanID    string      `json:"plan_id"`
	HasSnap   bool        `json:"has_snap"` // false=从未落地，无从谈漂移
	Drifted   bool        `json:"drifted"`
	Pending   int         `json:"pending"`  // 未落地改动条数（画布改了没落地）
	External  int         `json:"external"` // 外部漂移条数（现实被外部改了）
	Items     []DriftItem `json:"items"`
	CheckedAt time.Time   `json:"checked_at"`
}

// dsgDiff 单条三方对比结果：Item 给前端展示，Node 供收敛执行时定位要操作的资源。
type dsgDiff struct {
	Item DriftItem
	Node dsgNode // 要操作的计划/基线节点（deleted 用基线节点，其余用计划节点）
}

// dsgSlot 三方对比的逐资源槽位：同一资源键在 计划/基线/现实 三侧的存在与规格。
type dsgSlot struct {
	kind        string
	res         string
	plan        *dsgNode
	base        *dsgNode
	real        bool
	vm          model.VM
	stackStatus string
}

// dsgReality 平台现实（DB + docker + libvirt）——三方对比的「实际」一侧。
type dsgReality struct {
	VMs    map[string]model.VM // VM 名 → 记录
	Stacks map[string]string   // compose 项目名 → 状态串
	Nets   map[string]bool     // libvirt 网络名
}

// loadReality 采集平台现实。任一侧不可用（DB/Docker/Virt 为 nil 或调用失败）按空处理，
// 与旧口径一致：拿不到的现实不臆造，只报「现实为空」。
func (h *DesignerHandler) loadReality() dsgReality {
	r := dsgReality{VMs: map[string]model.VM{}, Stacks: map[string]string{}, Nets: map[string]bool{}}
	if h.DB != nil {
		var vms []model.VM
		if err := h.DB.Find(&vms).Error; err == nil {
			for _, v := range vms {
				r.VMs[v.Name] = v
			}
		}
	}
	if h.Docker != nil {
		if projects, err := h.Docker.ComposeList(); err == nil {
			for _, pr := range projects {
				r.Stacks[pr.Name] = pr.Status
			}
		}
	}
	if h.Virt != nil {
		if nets, err := h.Virt.ListNetworks(); err == nil {
			for _, n := range nets {
				r.Nets[n.Name] = true
			}
		}
	}
	return r
}

// nodeResource 节点在现实里的资源名：VM/网络用 Name，容器栈用 Ref（= compose 项目名）。
func nodeResource(n dsgNode) string {
	if n.Kind == "container" {
		return n.Ref
	}
	return n.Name
}

// nodeKey 资源唯一键：kind + 资源名（同名不同类型不混淆）。
func nodeKey(kind, res string) string { return kind + "\x00" + res }

// dsgKindLabel 资源类型的中文名（拼理由文案用）。
func dsgKindLabel(kind string) string {
	switch kind {
	case "vm":
		return "虚拟机"
	case "container":
		return "容器栈"
	case "net":
		return "网络"
	default:
		return "资源"
	}
}

// specDrift 判断「现实已存在且计划也期望」的资源是否发生规格/运行态漂移。
// 只对能拿到确凿规格的资源判定（VM 的 vcpu/内存、容器栈的运行态），拿不准不报，
// 避免把「用户主动关机」这类正常态误报成需要重启。
func specDrift(want dsgNode, s *dsgSlot) (action, reason string, ok bool) {
	switch want.Kind {
	case "vm":
		vcpu := want.VCPU
		if vcpu <= 0 {
			vcpu = 1
		}
		mem := want.MemoryMB
		if mem <= 0 {
			mem = 1024
		}
		if vcpu != s.vm.VCPU || mem != s.vm.MemoryMB {
			return dsgActionRecreated, fmt.Sprintf("规格与期望不符（期望 %dC/%dMB，现实 %dC/%dMB）：需重建（破坏性，会删除并重建该虚拟机）", vcpu, mem, s.vm.VCPU, s.vm.MemoryMB), true
		}
		// 仅 paused/error 视为需重启的异常态；shut off 是合法态（用户可能主动关机），不报
		if s.vm.Status == model.VMStatusPaused || s.vm.Status == model.VMStatusError {
			return dsgActionRestarted, "虚拟机处于异常状态（" + s.vm.Status + "）：需重启恢复", true
		}
	case "container":
		if s.stackStatus != "" && !strings.Contains(s.stackStatus, "running") {
			return dsgActionRestarted, "容器栈未在运行（" + s.stackStatus + "）：需重启", true
		}
	}
	return "", "", false
}

// mkDiff 组装一条差异项（Node 由调用方按场景补齐）。
func mkDiff(s *dsgSlot, action, side, kind, reason string) *dsgDiff {
	return &dsgDiff{
		Item: DriftItem{
			Kind: kind, Node: s.res, Name: s.res, Resource: s.kind,
			Action: action, Reason: reason, Side: side, Detail: reason,
		},
	}
}

// computeDiff 三方对比：JSON 计划(期望) / 基线(上次期望) / 现实(DB+libvirt)。
//
// 为什么三方：只看「计划 vs 现实」会把两种成因混为一谈——
//   - 画布新增/删除但还没落地（side=pending）：收敛动作是「落地」；
//   - 现实被外部改了（side=external）：收敛动作是「修复现实」。
//
// 二者对用户的操作完全不同，故逐项标注 Side。
//
// plan 为 nil（计划文件缺失）时按 plan=baseline 处理——没有画布时「期望」即上次落地的
// 基线，这样纯外部漂移仍能检出，也兼容无画布的历史快照（旧测试即此路径）。
func computeDiff(plan, baseline *dsgPlan, reality dsgReality) []dsgDiff {
	if baseline == nil {
		return nil
	}
	if plan == nil {
		plan = baseline
	}
	slots := map[string]*dsgSlot{}
	get := func(kind, res string) *dsgSlot {
		k := nodeKey(kind, res)
		s := slots[k]
		if s == nil {
			s = &dsgSlot{kind: kind, res: res}
			slots[k] = s
		}
		return s
	}
	for i := range plan.Nodes {
		n := plan.Nodes[i]
		nn := n
		get(n.Kind, nodeResource(n)).plan = &nn
	}
	for i := range baseline.Nodes {
		n := baseline.Nodes[i]
		nn := n
		get(n.Kind, nodeResource(n)).base = &nn
	}
	for name, vm := range reality.VMs {
		s := get("vm", name)
		s.real = true
		s.vm = vm
	}
	for name, status := range reality.Stacks {
		s := get("container", name)
		s.real = true
		s.stackStatus = status
	}
	for name := range reality.Nets {
		get("net", name).real = true
	}

	keys := make([]string, 0, len(slots))
	for k := range slots {
		keys = append(keys, k)
	}
	sort.Strings(keys) // 稳定输出顺序（map 迭代无序）

	out := []dsgDiff{}
	for _, k := range keys {
		s := slots[k]
		p, b := s.plan, s.base
		var d *dsgDiff
		switch {
		case p != nil && !s.real:
			// 期望有、现实无
			if b == nil {
				d = mkDiff(s, dsgActionAdded, dsgSidePending, "missing", "画布新增，尚未落地：将创建"+dsgKindLabel(s.kind))
			} else {
				d = mkDiff(s, dsgActionAdded, dsgSideExternal, "missing", "已落地资源在现实中不存在（被外部删除）：需重建")
			}
			d.Node = *p
		case p != nil && s.real:
			// 期望有、现实也有
			if b == nil {
				d = mkDiff(s, dsgActionNone, dsgSidePending, "mismatch", "画布新增但现实已有同名资源：落地将复用/跳过（VM 同名会失败）")
				d.Node = *p
			} else if act, reason, ok := specDrift(*p, s); ok {
				d = mkDiff(s, act, dsgSideExternal, "mismatch", reason)
				d.Node = *p
			}
		case p == nil && b != nil && s.real:
			// 画布已移除该节点，但现实仍在：本计划创建的资源，收敛=下线
			d = mkDiff(s, dsgActionDeleted, dsgSidePending, "extra", "画布已移除该节点，现实仍在：收敛将下线"+dsgKindLabel(s.kind))
			d.Node = *b
		case p == nil && b == nil && s.real:
			// 现实有、计划/基线都没有：非本计划管理，收敛忽略（不做无差别删除）
			d = mkDiff(s, dsgActionNone, dsgSideExternal, "extra", "现实存在但非本计划创建/管理：收敛忽略（可用「从现状导入」纳入画布）")
		}
		// 其余分支（p==nil && b!=nil && !s.real：画布已删且现实已无）净效果已一致，不报
		if d != nil {
			out = append(out, *d)
		}
	}
	return out
}

// ComputeDrift 三方对比入口：读计划(期望)、基线(上次期望)、现实，逐资源产出差异项。
// h.DB/h.Docker/h.Virt 为空时按空现实处理。
func (h *DesignerHandler) ComputeDrift(planID string) DriftResult {
	res := DriftResult{PlanID: planID, HasSnap: false, Items: []DriftItem{}, CheckedAt: time.Now()}
	baseline := loadLatestSnapshot(planID)
	if baseline == nil {
		return res
	}
	res.HasSnap = true
	plan := loadPlanFile(planID) // 期望；无画布时为 nil（按 plan=baseline 处理）
	for _, d := range computeDiff(plan, baseline, h.loadReality()) {
		res.Items = append(res.Items, d.Item)
		switch d.Item.Side {
		case dsgSidePending:
			res.Pending++
		case dsgSideExternal:
			res.External++
		}
	}
	res.Drifted = len(res.Items) > 0
	return res
}

// DriftSummary 遍历全部快照逐计划对比，汇总给人读摘要（DE2 的 cron 巡检入口）。
// 只巡检有 latest 快照的计划（从未落地谈不上漂移）。
func (h *DesignerHandler) DriftSummary() (checked int, drifted int, summary string) {
	entries, err := os.ReadDir(snapshotDir())
	if err != nil {
		return 0, 0, "（暂无快照：还没有成功落地过任何计划）"
	}
	ids := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".latest.json") {
			ids[strings.TrimSuffix(name, ".latest.json")] = true
		}
	}
	var b strings.Builder
	for id := range ids {
		checked++
		d := h.ComputeDrift(id)
		if !d.Drifted {
			continue
		}
		drifted++
		b.WriteString("计划 " + id + "：" + strconv.Itoa(len(d.Items)) + " 处漂移\n")
		for i, it := range d.Items {
			if i >= 5 {
				b.WriteString("  …（其余 " + strconv.Itoa(len(d.Items)-5) + " 处省略）\n")
				break
			}
			b.WriteString("  - [" + it.Kind + "] " + it.Node + "：" + it.Detail + "\n")
		}
	}
	if drifted == 0 {
		return checked, 0, "全部 " + strconv.Itoa(checked) + " 个已落地计划与现实一致"
	}
	return checked, drifted, b.String()
}

// Drift GET /api/designer/plans/:id/drift——计划与现实的漂移对比（DE2）。
func (h *DesignerHandler) Drift(c *gin.Context) {
	id := c.Param("id")
	if !dsgIDRe.MatchString(id) {
		Fail(c, http.StatusBadRequest, "计划 ID 非法")
		return
	}
	Success(c, h.ComputeDrift(id))
}
