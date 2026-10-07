package handler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/jiuzhao/vmops/model"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestComputeDrift 漂移计算（DE2）：造快照 + 内存库现实，断言 missing/extra 分类。
// 网络项依赖真实 libvirt（本机有 default 网络）；Docker 置 nil 时栈现实为空。
func TestComputeDrift(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("内存库失败: %v", err)
	}
	if err := db.AutoMigrate(&model.VM{}); err != nil {
		t.Fatal(err)
	}
	// 现实：存在机（在快照里）+ 多余机（不在快照里）
	if err := db.Create(&model.VM{Name: "存在机", UUID: "uuid-keep-1"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.VM{Name: "多余机", UUID: "uuid-extra-1"}).Error; err != nil {
		t.Fatal(err)
	}

	_ = os.MkdirAll(snapshotDir(), 0o755)
	snap := `{"id":"zz-drift-test","name":"漂移测试","nodes":[
	  {"id":"n1","kind":"vm","name":"存在机"},
	  {"id":"n2","kind":"vm","name":"已删机"},
	  {"id":"n3","kind":"container","ref":"lost-stack"},
	  {"id":"n4","kind":"net","name":"default"}
	]}`
	if err := os.WriteFile(filepath.Join(snapshotDir(), "zz-drift-test.latest.json"), []byte(snap), 0o644); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Remove(filepath.Join(snapshotDir(), "zz-drift-test.latest.json"))
	}()

	h := &DesignerHandler{DB: db} // Virt/Docker 为 nil：网络与栈现实按空处理
	res := h.ComputeDrift("zz-drift-test")
	if !res.HasSnap {
		t.Fatal("应识别到快照")
	}
	got := map[string]string{} // node → kind
	for _, it := range res.Items {
		got[it.Node] = it.Kind
	}
	if got["已删机"] != "missing" {
		t.Errorf("「已删机」应为 missing，实际 %q", got["已删机"])
	}
	if got["lost-stack"] != "missing" {
		t.Errorf("「lost-stack」应为 missing，实际 %q", got["lost-stack"])
	}
	if got["多余机"] != "extra" {
		t.Errorf("「多余机」应为 extra，实际 %q", got["多余机"])
	}
	if _, ok := got["存在机"]; ok {
		t.Error("「存在机」现实存在，不应报漂移")
	}
	if !res.Drifted {
		t.Error("有漂移项时 Drifted 应为 true")
	}

	// 无快照的计划：HasSnap=false 且不报漂移
	if r2 := h.ComputeDrift("zz-nonexistent"); r2.HasSnap || r2.Drifted {
		t.Errorf("无快照计划应 HasSnap=false 且无漂移，实际 %+v", r2)
	}

	// DriftSummary 汇总口径
	checked, drifted, summary := h.DriftSummary()
	if checked < 1 || drifted < 1 {
		t.Errorf("汇总应有 ≥1 已检查与 ≥1 漂移，实际 checked=%d drifted=%d summary=%s", checked, drifted, summary)
	}
	_, _ = json.Marshal(res)
}
