// designer_reconcile.go：架构设计器的收敛预演（差异 → 动作集合）与落地拓扑排序。
package handler

import (
	"sort"
	"time"
)

// ReconcileAction 一条收敛动作（收敛预演的列表项 + 执行依据）。
type ReconcileAction struct {
	Action string `json:"action"` // added | deleted | recreated | restarted
	Node   string `json:"node"`   // 资源名
	Name   string `json:"name,omitempty"`
	Kind   string `json:"kind"` // vm | container | net
	Side   string `json:"side"` // pending | external
	Reason string `json:"reason"`
}

// ReconcileResult 收敛计划：先列现状 → 算出需要 added/deleted/recreated/restarted 的集合。
type ReconcileResult struct {
	PlanID    string            `json:"plan_id"`
	HasSnap   bool              `json:"has_snap"` // false=从未落地，收敛退化为全量落地
	Actions   []ReconcileAction `json:"actions"`
	Counts    map[string]int    `json:"counts"` // 动作 → 条数
	CheckedAt time.Time         `json:"checked_at"`
}

// reconcilePlan 由三方对比算出收敛动作集合与可执行 ops。
//   - baseline 为 nil（从未落地）：退化为全量落地，所有节点视作 added；
//   - 否则逐条取 computeDiff 里 action != none 的项。VMID 已在 computeDiff 里带出。
func reconcilePlan(plan, baseline *dsgPlan, reality dsgReality, planID string) (ReconcileResult, []dsgOp) {
	res := ReconcileResult{PlanID: planID, HasSnap: baseline != nil, Actions: []ReconcileAction{}, Counts: map[string]int{}, CheckedAt: time.Now()}
	ops := []dsgOp{}
	if plan == nil {
		return res, ops
	}
	if baseline == nil {
		for _, n := range orderNodes(plan.Nodes, plan.Links) {
			reason := "首次落地：创建" + dsgKindLabel(n.Kind)
			ops = append(ops, dsgOp{Action: dsgActionAdded, Node: n, Reason: reason})
			res.Actions = append(res.Actions, ReconcileAction{Action: dsgActionAdded, Node: nodeResource(n), Name: n.Name, Kind: n.Kind, Side: dsgSidePending, Reason: reason})
			res.Counts[dsgActionAdded]++
		}
		return res, ops
	}
	for _, d := range computeDiff(plan, baseline, reality) {
		if d.Item.Action == "" || d.Item.Action == dsgActionNone {
			continue
		}
		ops = append(ops, dsgOp{Action: d.Item.Action, Node: d.Node, Reason: d.Item.Reason})
		res.Actions = append(res.Actions, ReconcileAction{Action: d.Item.Action, Node: d.Item.Node, Name: d.Item.Name, Kind: d.Item.Resource, Side: d.Item.Side, Reason: d.Item.Reason})
		res.Counts[d.Item.Action]++
	}
	return res, ops
}

// computeReconcile 从磁盘计划读期望并算出收敛集合（收敛预演用）。
func (h *DesignerHandler) computeReconcile(planID string) (ReconcileResult, []dsgOp) {
	return reconcilePlan(loadPlanFile(planID), loadLatestSnapshot(planID), h.loadReality(), planID)
}

// orderNodes 落地顺序：按 plan.Links 做拓扑排序（to 依赖 from），入度 0 中按 kind 优先级
// net(0) → vm(1) → container(2) 挑选；有环或链接不足时退化为 kind 优先级稳定排序。
// 目标：net 先建（VM/容器要接入），vm 先于依赖它的 container。
func orderNodes(nodes []dsgNode, links []dsgLink) []dsgNode {
	priority := func(kind string) int {
		switch kind {
		case "net":
			return 0
		case "vm":
			return 1
		case "container":
			return 2
		default:
			return 3
		}
	}
	ids := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		ids[n.ID] = true
	}
	// dep[to] = 依赖的 from 集合；indeg[to] = 未满足的依赖数
	dep := make(map[string][]string)
	indeg := make(map[string]int, len(nodes))
	for _, n := range nodes {
		indeg[n.ID] = 0
	}
	for _, l := range links {
		if l.From == l.To || !ids[l.From] || !ids[l.To] {
			continue
		}
		dep[l.To] = append(dep[l.To], l.From)
		indeg[l.To]++
	}
	remaining := append([]dsgNode(nil), nodes...)
	out := make([]dsgNode, 0, len(nodes))
	for len(remaining) > 0 {
		best := -1
		for i, n := range remaining {
			if indeg[n.ID] == 0 && (best == -1 || priority(n.Kind) < priority(remaining[best].Kind)) {
				best = i
			}
		}
		if best == -1 {
			// 有环：剩余节点退化为 kind 优先级稳定排序后收尾
			rest := append([]dsgNode(nil), remaining...)
			sort.SliceStable(rest, func(i, j int) bool { return priority(rest[i].Kind) < priority(rest[j].Kind) })
			out = append(out, rest...)
			break
		}
		chosen := remaining[best]
		out = append(out, chosen)
		remaining = append(remaining[:best], remaining[best+1:]...)
		for _, n := range remaining {
			for _, f := range dep[n.ID] {
				if f == chosen.ID {
					indeg[n.ID]--
				}
			}
		}
	}
	return out
}
