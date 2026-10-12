package tasks

import (
	"fmt"
	"testing"

	"github.com/jiuzhao/vmops/model"
)

// TestRecordStackVersionDedup 内容未变时不新增版本（重复 redeploy/upgrade 不产生噪音行）。
func TestRecordStackVersionDedup(t *testing.T) {
	db := newProvTestDB(t, &model.StackVersion{})

	v1, added, err := RecordStackVersion(db, "web", "content-A", "部署", "alice", nil, nil)
	if err != nil {
		t.Fatalf("首次记录失败: %v", err)
	}
	if !added || v1.Version != 1 {
		t.Fatalf("首次应为 v1 且新增，got added=%v ver=%d", added, v1.Version)
	}

	v2, added, err := RecordStackVersion(db, "web", "content-A", "升级", "alice", nil, nil)
	if err != nil {
		t.Fatalf("重复记录失败: %v", err)
	}
	if added {
		t.Fatal("内容一致不应新增版本")
	}
	if v2.ID != v1.ID {
		t.Fatalf("去重应返回既有版本 id=%d，got=%d", v1.ID, v2.ID)
	}

	var count int64
	db.Model(&model.StackVersion{}).Where("stack_id = ?", "web").Count(&count)
	if count != 1 {
		t.Fatalf("去重后应仅 1 行，got=%d", count)
	}
}

// TestRecordStackVersionTrimBoundary 保留策略裁剪边界：超过上限只留最近 N 个（版本号连续递增）。
func TestRecordStackVersionTrimBoundary(t *testing.T) {
	db := newProvTestDB(t, &model.StackVersion{})

	total := stackVersionKeepN + 2 // 多造 2 条以触发裁剪
	for i := 1; i <= total; i++ {
		if _, _, err := RecordStackVersion(db, "web", fmt.Sprintf("content-%d", i), "部署", "alice", nil, nil); err != nil {
			t.Fatalf("第 %d 次记录失败: %v", i, err)
		}
	}

	var items []model.StackVersion
	if err := db.Where("stack_id = ?", "web").Order("version ASC").Find(&items).Error; err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if len(items) != stackVersionKeepN {
		t.Fatalf("应保留 %d 个版本，got=%d", stackVersionKeepN, len(items))
	}
	// 保留的是最近的 N 个：版本号应从 (total-N+1) 连续到 total。
	wantFirst := total - stackVersionKeepN + 1
	if items[0].Version != wantFirst {
		t.Fatalf("最旧保留版本应为 v%d，got=v%d", wantFirst, items[0].Version)
	}
	if items[len(items)-1].Version != total {
		t.Fatalf("最新版本应为 v%d，got=v%d", total, items[len(items)-1].Version)
	}
	// 内容与版本号对应，确认裁掉的是最旧的而非随机。
	if items[0].Content != fmt.Sprintf("content-%d", wantFirst) {
		t.Fatalf("v%d 内容应为 content-%d，got=%q", wantFirst, wantFirst, items[0].Content)
	}
}

// TestRecordStackVersionTrimIsolatedPerStack 裁剪按栈隔离：一个栈的历史不影响另一个栈。
func TestRecordStackVersionTrimIsolatedPerStack(t *testing.T) {
	db := newProvTestDB(t, &model.StackVersion{})

	for i := 1; i <= stackVersionKeepN+3; i++ {
		if _, _, err := RecordStackVersion(db, "busy", fmt.Sprintf("c-%d", i), "部署", "", nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := RecordStackVersion(db, "quiet", "only-one", "部署", "", nil, nil); err != nil {
		t.Fatal(err)
	}

	var busyCount, quietCount int64
	db.Model(&model.StackVersion{}).Where("stack_id = ?", "busy").Count(&busyCount)
	db.Model(&model.StackVersion{}).Where("stack_id = ?", "quiet").Count(&quietCount)
	if busyCount != stackVersionKeepN {
		t.Fatalf("busy 栈应裁剪到 %d，got=%d", stackVersionKeepN, busyCount)
	}
	if quietCount != 1 {
		t.Fatalf("quiet 栈不应被别的栈的裁剪影响，got=%d", quietCount)
	}
}

// TestRollbackVersionOrdering 回滚前后的版本入栈顺序（最易错处）：
// v1(A) → v2(B) → 回滚到 v1：先记「回滚前快照」(B，去重跳过)，再记「回滚到 v1」(A)。
// 期望历史为 [A(v1), B(v2), A(v3, rollback_from=1)]——回滚后仍能找回「回滚前」的 B(v2)。
func TestRollbackVersionOrdering(t *testing.T) {
	db := newProvTestDB(t, &model.StackVersion{})

	if _, _, err := RecordStackVersion(db, "web", "A", "部署", "alice", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RecordStackVersion(db, "web", "B", "升级", "bob", nil, nil); err != nil {
		t.Fatal(err)
	}

	// 回滚到 v1：executor 先记回滚前快照（当前文件内容 B），再写回 A 后记回滚结果。
	if _, added, err := RecordStackVersion(db, "web", "B", "回滚前快照", "carol", nil, nil); err != nil || added {
		t.Fatalf("回滚前快照内容 B 已是最新版本，应去重跳过，added=%v err=%v", added, err)
	}
	from := 1
	v3, added, err := RecordStackVersion(db, "web", "A", "回滚到 v1", "carol", nil, &from)
	if err != nil || !added {
		t.Fatalf("回滚结果应新增版本，added=%v err=%v", added, err)
	}
	if v3.Version != 3 {
		t.Fatalf("回滚结果应为 v3，got=v%d", v3.Version)
	}
	if v3.RollbackFrom == nil || *v3.RollbackFrom != 1 {
		t.Fatalf("RollbackFrom 应标记为 1，got=%v", v3.RollbackFrom)
	}

	var items []model.StackVersion
	db.Where("stack_id = ?", "web").Order("version ASC").Find(&items)
	if len(items) != 3 {
		t.Fatalf("历史应有 3 条，got=%d", len(items))
	}
	want := []string{"A", "B", "A"}
	for i, w := range want {
		if items[i].Content != w {
			t.Fatalf("v%d 内容应为 %q，got=%q", i+1, w, items[i].Content)
		}
	}
}

// TestGetStackVersionCrossStack 按 id 读取版本并核对归属栈（回滚跨栈防护的数据基础）。
func TestGetStackVersionCrossStack(t *testing.T) {
	db := newProvTestDB(t, &model.StackVersion{})

	va, _, err := RecordStackVersion(db, "stack-a", "content-A", "部署", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := RecordStackVersion(db, "stack-b", "content-B", "部署", "", nil, nil); err != nil {
		t.Fatal(err)
	}

	got, err := GetStackVersion(db, va.ID)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if got.StackID != "stack-a" {
		t.Fatalf("归属栈应为 stack-a，got=%q", got.StackID)
	}
	if got.Content != "content-A" {
		t.Fatalf("内容应为 content-A，got=%q", got.Content)
	}

	if _, err := GetStackVersion(db, 999999); err == nil {
		t.Fatal("不存在的版本应返回错误")
	}
}
