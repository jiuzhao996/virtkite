package handler

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/jiuzhao/vmops/model"
)

func groupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.VM{}, &model.VMGrant{}, &model.VMGroupGrant{}, &model.UserGroup{}, &model.UserGroupMember{}); err != nil {
		t.Fatalf("AutoMigrate 失败: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.Close()
		}
	})
	return db
}

// TestGrantedVMIDsUnionWithGroups 组授权与直接授权取并集；过期组授权不计入（终审口径：
// 「expires_at IS NULL OR > now」逐字一致；成员退出组（成员行删除）后可见性即时消失）
func TestGrantedVMIDsUnionWithGroups(t *testing.T) {
	db := groupTestDB(t)
	db.Create(&model.VM{ID: 1, Name: "direct", Status: model.VMStatusRunning, UUID: "u1"})
	db.Create(&model.VM{ID: 2, Name: "grouped", Status: model.VMStatusRunning, UUID: "u2"})
	db.Create(&model.VM{ID: 3, Name: "expired-group", Status: model.VMStatusRunning, UUID: "u3"})
	db.Create(&model.VM{ID: 4, Name: "no-access", Status: model.VMStatusRunning, UUID: "u4"})

	user := model.User{Username: "stu", Role: "operator", IsActive: true, PasswordHash: "x"}
	db.Create(&user)
	group := model.UserGroup{Name: "class-101"}
	db.Create(&group)
	db.Create(&model.UserGroupMember{GroupID: group.ID, UserID: user.ID})

	future := time.Now().Add(time.Hour)
	past := time.Now().Add(-time.Hour)
	db.Create(&model.VMGrant{UserID: user.ID, VMID: 1})                      // 直接授权
	db.Create(&model.VMGroupGrant{GroupID: group.ID, VMID: 2, ExpiresAt: &future}) // 有效组授权
	db.Create(&model.VMGroupGrant{GroupID: group.ID, VMID: 3, ExpiresAt: &past})   // 过期组授权

	got := grantedVMIDs(db, user.ID)
	if !got[1] {
		t.Fatalf("直接授权 VM1 应可见")
	}
	if !got[2] {
		t.Fatalf("有效组授权 VM2 应可见")
	}
	if got[3] {
		t.Fatalf("过期组授权 VM3 不可见")
	}
	if got[4] {
		t.Fatalf("无任何授权的 VM4 不可见")
	}

	// 成员退出组 → 组授权即时消失
	db.Where("group_id = ? AND user_id = ?", group.ID, user.ID).Delete(&model.UserGroupMember{})
	got = grantedVMIDs(db, user.ID)
	if got[2] {
		t.Fatalf("退出组后 VM2 应立即不可见")
	}
}

// TestGroupGrantCleanupOnVMDelete 删 VM 时组授权同批回收（vm_delete 逻辑的表级断言）
func TestGroupGrantCleanupOnVMDelete(t *testing.T) {
	db := groupTestDB(t)
	group := model.UserGroup{Name: "g"}
	db.Create(&group)
	db.Create(&model.VMGroupGrant{GroupID: group.ID, VMID: 9})

	db.Where("vm_id = ?", 9).Delete(&model.VMGrant{})
	db.Where("vm_id = ?", 9).Delete(&model.VMGroupGrant{})

	var cnt int64
	db.Model(&model.VMGroupGrant{}).Where("vm_id = ?", 9).Count(&cnt)
	if cnt != 0 {
		t.Fatalf("删 VM 后组授权应同批回收")
	}
}
