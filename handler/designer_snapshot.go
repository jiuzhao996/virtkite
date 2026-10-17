// designer_snapshot.go：架构设计器的基线快照与计划文件读写（data/designer/ 下的 JSON 落盘与读取）。
package handler

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/jiuzhao/vmops/config"
)

// ── 快照 = 已落地基线（DE2）────────────────────────────────

// snapshotDir 快照目录：data/designer/snapshots/。<plan>.latest.json 是当前基线
// （上次成功落地/收敛时的期望——由落地器写，前端点「保存」不写）；<plan>-<unixts>.json
// 为历史存档（不可变，保留追溯）。
//
// 为什么基线必须与「期望」同源：三方对比要拿基线直接与 JSON 计划、与平台现实逐节点
// 比较。基线存的就是一份 dsgPlan（nodes+links），与计划文件结构完全一致，才能零转换 diff。
func snapshotDir() string { return config.DataPath("designer", "snapshots") }

// saveSnapshot 落地/收敛成功后覆盖基线：latest（当前基线）+ 带时间戳历史档。
// 只在 Apply/Reconcile 成功路径调用——这就是「快照语义 = 已落地基线」的落点。
//
// 写盘前强制剥离 VM 口令：Apply/Reconcile 的 plan 里带着请求体 overlay 进来的明文口令，
// 直接 Marshal 会把明文落到快照文件（与计划文件写盘前剥离同一口径的兜底）。
// 用副本再剥离，避免改动调用方共享的 Nodes 切片底层数组。
func (h *DesignerHandler) saveSnapshot(p dsgPlan) {
	snap := p
	snap.DeployedAt = time.Now().Format(time.RFC3339)
	snap.Nodes = make([]dsgNode, len(p.Nodes))
	copy(snap.Nodes, p.Nodes)
	for i := range snap.Nodes {
		snap.Nodes[i].SSHSecret = ""
	}
	raw, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		log.Printf("[designer] 快照序列化失败 plan=%s err=%v", p.ID, err)
		return
	}
	if err := os.MkdirAll(snapshotDir(), 0o755); err != nil {
		log.Printf("[designer] 快照目录创建失败 plan=%s err=%v", p.ID, err)
		return
	}
	ts := time.Now().Unix()
	hist := filepath.Join(snapshotDir(), fmt.Sprintf("%s-%d.json", p.ID, ts))
	latest := filepath.Join(snapshotDir(), p.ID+".latest.json")
	if err := os.WriteFile(hist, raw, 0o644); err != nil {
		log.Printf("[designer] 历史快照写入失败 plan=%s err=%v", p.ID, err)
	}
	if err := os.WriteFile(latest, raw, 0o644); err != nil {
		log.Printf("[designer] 基线快照写入失败 plan=%s err=%v", p.ID, err)
	}
}

// loadLatestSnapshot 读某计划的当前基线；无基线（从未落地）返回 nil。
func loadLatestSnapshot(planID string) *dsgPlan {
	raw, err := os.ReadFile(filepath.Join(snapshotDir(), planID+".latest.json"))
	if err != nil {
		return nil
	}
	var p dsgPlan
	if json.Unmarshal(raw, &p) != nil {
		return nil
	}
	return &p
}

// loadPlanFile 读计划文件（= 期望 / intended）；不存在或解析失败返回 nil（无画布）。
func loadPlanFile(planID string) *dsgPlan {
	raw, err := os.ReadFile(filepath.Join(designerDir(), planID+".json"))
	if err != nil {
		return nil
	}
	var p dsgPlan
	if json.Unmarshal(raw, &p) != nil {
		return nil
	}
	return &p
}
