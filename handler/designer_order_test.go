package handler

import "testing"

// TestOrderNodes 覆盖落地依赖排序：连线拓扑优先，其次 kind 优先级（net→vm→container），有环退化。
func TestOrderNodes(t *testing.T) {
	cases := []struct {
		name  string
		nodes []dsgNode
		links []dsgLink
		want  []string // 期望的 id 顺序
	}{
		{
			name:  "连线表达依赖：net 先于 vm",
			nodes: []dsgNode{{ID: "v", Kind: "vm"}, {ID: "n", Kind: "net"}},
			links: []dsgLink{{From: "n", To: "v"}},
			want:  []string{"n", "v"},
		},
		{
			name:  "无连线按 kind 优先级 net→vm→container",
			nodes: []dsgNode{{ID: "c", Kind: "container"}, {ID: "v", Kind: "vm"}, {ID: "n", Kind: "net"}},
			links: nil,
			want:  []string{"n", "v", "c"},
		},
		{
			name:  "vm 依赖 container：按连线先 container（连线优先于 kind）",
			nodes: []dsgNode{{ID: "v", Kind: "vm"}, {ID: "c", Kind: "container"}},
			links: []dsgLink{{From: "c", To: "v"}},
			want:  []string{"c", "v"},
		},
		{
			name:  "有环退化为 kind 优先级稳定排序（不 panic、不丢节点）",
			nodes: []dsgNode{{ID: "a", Kind: "vm"}, {ID: "b", Kind: "vm"}},
			links: []dsgLink{{From: "a", To: "b"}, {From: "b", To: "a"}},
			want:  []string{"a", "b"},
		},
		{
			name:  "自环与悬空链接被忽略",
			nodes: []dsgNode{{ID: "a", Kind: "vm"}},
			links: []dsgLink{{From: "a", To: "a"}, {From: "a", To: "ghost"}},
			want:  []string{"a"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := orderNodes(tc.nodes, tc.links)
			if len(got) != len(tc.want) {
				t.Fatalf("节点数不符 got=%d want=%d（got=%v）", len(got), len(tc.want), idsOf(got))
			}
			for i, id := range tc.want {
				if got[i].ID != id {
					t.Errorf("第 %d 位 got=%s want=%s（全序 %v）", i, got[i].ID, id, idsOf(got))
				}
			}
		})
	}
}

func idsOf(ns []dsgNode) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = n.ID
	}
	return out
}

// TestResolveVMNetwork 覆盖 VM↔net 连线解析：相连取网络名，无连线回落 default。
func TestResolveVMNetwork(t *testing.T) {
	plan := &dsgPlan{
		Nodes: []dsgNode{
			{ID: "v1", Kind: "vm", Name: "app"},
			{ID: "n1", Kind: "net", Name: "frontnet"},
			{ID: "v2", Kind: "vm", Name: "lonely"},
		},
		Links: []dsgLink{{From: "n1", To: "v1"}},
	}
	if got := resolveVMNetwork(plan, plan.Nodes[0]); got != "frontnet" {
		t.Errorf("相连 VM 应接入设计网络 frontnet，got=%q", got)
	}
	if got := resolveVMNetwork(plan, plan.Nodes[2]); got != "default" {
		t.Errorf("无连线应回落 default，got=%q", got)
	}
	if got := resolveVMNetwork(nil, plan.Nodes[0]); got != "default" {
		t.Errorf("nil 计划应回落 default，got=%q", got)
	}
	// 反向连线（vm→net）也应解析到
	rev := &dsgPlan{Nodes: plan.Nodes, Links: []dsgLink{{From: "v1", To: "n1"}}}
	if got := resolveVMNetwork(rev, rev.Nodes[0]); got != "frontnet" {
		t.Errorf("反向连线应解析到 frontnet，got=%q", got)
	}
}
