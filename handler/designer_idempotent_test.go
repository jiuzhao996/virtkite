package handler

// 设计器落地幂等性回归测试（2026-10-18）。
//
// 背景：「一键落地」此前把所有节点一律当作 added，重复点第二次会：
//   1) VM 同名建机 → libvirt define 失败 → executor 软删刚建的 DB 行 → 回收站多一条垃圾；
//   2) 预览弹窗来自 reconcile-plan（显示「无需动作」），确认后却全量重建，言行不一。
// 修复：Apply 改用与预览同源的 reconcilePlan 算法，且 provisionVM 对同名 VM 跳过。

import (
	"testing"

	"github.com/jiuzhao/vmops/model"
)

func TestReconcilePlanNoBaselineAllAdded(t *testing.T) {
	plan := &dsgPlan{
		ID:   "p1",
		Name: "测试计划",
		Nodes: []dsgNode{
			{ID: "c", Kind: "container", Ref: "web-stack", Name: "栈"},
			{ID: "n", Kind: "net", Name: "frontnet"},
			{ID: "v", Kind: "vm", Name: "app"},
		},
	}
	reality := dsgReality{
		VMs:    map[string]model.VM{},
		Stacks: map[string]string{},
		Nets:   map[string]bool{},
	}
	// 无基线（从未成功落地）→ 全部 added
	res, ops := reconcilePlan(plan, nil, reality, "p1")
	if len(ops) != len(plan.Nodes) {
		t.Fatalf("首次落地应产出全部节点：got=%d want=%d", len(ops), len(plan.Nodes))
	}
	for _, op := range ops {
		if op.Action != dsgActionAdded {
			t.Errorf("首次落地动作应全为 added，实际 %s（%s）", op.Action, nodeResource(op.Node))
		}
	}
	if res.Counts[dsgActionAdded] != len(plan.Nodes) {
		t.Errorf("统计计数不符：%v", res.Counts)
	}
}

func TestReconcilePlanWithBaselineSkipsUnchanged(t *testing.T) {
	nodes := []dsgNode{
		{ID: "v", Kind: "vm", Name: "app", VCPU: 2, MemoryMB: 2048},
		{ID: "n", Kind: "net", Name: "frontnet"},
	}
	baseline := &dsgPlan{ID: "p1", Nodes: nodes, Links: nil}
	// 现实与基线完全一致、规格一致 → 无需任何动作（这是本次修复的核心断言）
	reality := dsgReality{
		VMs:    map[string]model.VM{"app": {Name: "app", VCPU: 2, MemoryMB: 2048, Status: model.VMStatusRunning}},
		Stacks: map[string]string{},
		Nets:   map[string]bool{"frontnet": true},
	}
	_, ops := reconcilePlan(&dsgPlan{ID: "p1", Nodes: nodes}, baseline, reality, "p1")
	if len(ops) != 0 {
		t.Fatalf("现实与基线一致时不应产出动作，实际 %d 个：%+v", len(ops), ops)
	}
}

func TestReconcilePlanSameNameVMSkippedByApply(t *testing.T) {
	// 已就绪的 VM 在同名重复落地时必须被跳过，不该进 ops（否则会重复建机）。
	// provisionVM 的前置检查由此断言间接覆盖：ops 为空 = 不会走到建机分支。
	nodes := []dsgNode{{ID: "v", Kind: "vm", Name: "app", VCPU: 2, MemoryMB: 2048}}
	baseline := &dsgPlan{ID: "p1", Nodes: nodes}
	reality := dsgReality{
		VMs:    map[string]model.VM{"app": {Name: "app", VCPU: 2, MemoryMB: 2048, Status: model.VMStatusRunning}},
		Stacks: map[string]string{},
		Nets:   map[string]bool{},
	}
	_, ops := reconcilePlan(&dsgPlan{ID: "p1", Nodes: nodes}, baseline, reality, "p1")
	if len(ops) != 0 {
		t.Fatalf("同规格 VM 已就绪时不应有动作（重复落地幂等），实际 %d 个", len(ops))
	}
}

func TestSpecDriftDetectsMismatch(t *testing.T) {
	// 规格不符 → recreated（破坏性），确认 specDrift 仍能识别真实漂移
	got, reason, ok := specDrift(
		dsgNode{Kind: "vm", Name: "app", VCPU: 2, MemoryMB: 2048},
		&dsgSlot{vm: model.VM{Name: "app", VCPU: 4, MemoryMB: 2048, Status: model.VMStatusRunning}},
	)
	if !ok || got != dsgActionRecreated {
		t.Fatalf("规格不符应判 recreated，实际 ok=%v action=%q", ok, got)
	}
	if reason == "" {
		t.Error("重建动作必须给出原因文案")
	}
}

func TestSpecDriftShutOffIsNotDrift(t *testing.T) {
	// 用户主动关机是合法态，不算漂移（不能触发无谓的重建/重启）
	got, _, ok := specDrift(
		dsgNode{Kind: "vm", Name: "app", VCPU: 2, MemoryMB: 2048},
		&dsgSlot{vm: model.VM{Name: "app", VCPU: 2, MemoryMB: 2048, Status: model.VMStatusShutOff}},
	)
	if ok {
		t.Fatalf("关机态不应判漂移，实际 action=%q", got)
	}
}
