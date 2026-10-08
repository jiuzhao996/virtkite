package tasks

import (
	"encoding/json"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/jiuzhao/vmops/model"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newProvTestDB 内存 SQLite + AutoMigrate（测试不连真库）。
func newProvTestDB(t *testing.T, models ...interface{}) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	if err := db.AutoMigrate(models...); err != nil {
		t.Fatalf("AutoMigrate 失败: %v", err)
	}
	return db
}

// TestParseTaskProvision 覆盖 provision 块解析：缺省 nil（行为不变），完整块字段齐全。
func TestParseTaskProvision(t *testing.T) {
	if p := parseTaskProvision(map[string]interface{}{"name": "x"}); p != nil {
		t.Fatalf("无 provision 应返回 nil（保持历史行为），got=%+v", p)
	}
	if p := parseTaskProvision(map[string]interface{}{"provision": "not-a-map"}); p != nil {
		t.Fatalf("非 map 的 provision 应返回 nil，got=%+v", p)
	}
	raw := map[string]interface{}{
		"provision": map[string]interface{}{
			"start": true, "wait_ip": true, "ssh_user": "root",
			"password_enc": "CIPHER", "salt": "SALT",
		},
	}
	p := parseTaskProvision(raw)
	if p == nil {
		t.Fatal("完整 provision 不应为 nil")
	}
	if !p.Start || !p.WaitIP || p.SSHUser != "root" || p.PasswordEnc != "CIPHER" || p.Salt != "SALT" {
		t.Fatalf("解析结果不符: %+v", p)
	}
}

// TestMergeUniqueUint 覆盖目标合并去重（保持 a 前 b 后的稳定顺序）。
func TestMergeUniqueUint(t *testing.T) {
	got := mergeUniqueUint([]uint{1, 2, 3}, []uint{3, 4, 1})
	want := []uint{1, 2, 3, 4}
	if len(got) != len(want) {
		t.Fatalf("长度不符 got=%v want=%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("顺序/去重不符 got=%v want=%v", got, want)
		}
	}
	if g := mergeUniqueUint(nil, []uint{5}); len(g) != 1 || g[0] != 5 {
		t.Fatalf("nil 左值应返回右值，got=%v", g)
	}
}

// TestRemoveVMFromHostGroups 覆盖删除级联：成员剔除、空组清空、脏数据不阻断。
func TestRemoveVMFromHostGroups(t *testing.T) {
	db := newProvTestDB(t, &model.HostGroup{})
	db.Create(&model.HostGroup{Name: "g1", VMIDs: "[1,2,3]"})
	db.Create(&model.HostGroup{Name: "g2", VMIDs: "[2]"})
	db.Create(&model.HostGroup{Name: "g3", VMIDs: "broken-json"}) // 脏数据：跳过不报错
	db.Create(&model.HostGroup{Name: "g4", VMIDs: ""})

	ctx := &ExecContext{DB: db}
	if err := removeVMFromHostGroups(ctx, 2); err != nil {
		t.Fatalf("清理失败: %v", err)
	}

	var g1 model.HostGroup
	db.Where("name = ?", "g1").First(&g1)
	var ids []uint
	if err := json.Unmarshal([]byte(g1.VMIDs), &ids); err != nil {
		t.Fatalf("g1 结果非法 JSON: %s", g1.VMIDs)
	}
	if len(ids) != 2 || ids[0] != 1 || ids[1] != 3 {
		t.Errorf("g1 应剩 [1,3]，got=%s", g1.VMIDs)
	}
	var g2 model.HostGroup
	db.Where("name = ?", "g2").First(&g2)
	if g2.VMIDs != "[]" {
		t.Errorf("g2 应清空为 []，got=%s", g2.VMIDs)
	}
	var g3 model.HostGroup
	db.Where("name = ?", "g3").First(&g3)
	if g3.VMIDs != "broken-json" {
		t.Errorf("脏数据组不应被改写，got=%s", g3.VMIDs)
	}
}

// TestBuildAnsibleGroups 覆盖 inventory 分组生成：只含本次目标成员，无目标成员的组不出现。
func TestBuildAnsibleGroups(t *testing.T) {
	db := newProvTestDB(t, &model.HostGroup{})
	db.Create(&model.HostGroup{Name: "web", VMIDs: "[1,2]"})
	db.Create(&model.HostGroup{Name: "db", VMIDs: "[3]"}) // 3 不在目标里

	ctx := &ExecContext{DB: db}
	vms := []model.VM{{ID: 1}, {ID: 2}}
	nameByVM := map[uint]string{1: "vm1", 2: "vm2"}
	groups := buildAnsibleGroups(ctx, vms, nameByVM)

	if len(groups["web"]) != 2 || groups["web"][0] != "vm1" || groups["web"][1] != "vm2" {
		t.Errorf("web 组应为 [vm1 vm2]，got=%v", groups["web"])
	}
	if _, ok := groups["db"]; ok {
		t.Errorf("db 组无目标成员，不应出现，got=%v", groups["db"])
	}
}
