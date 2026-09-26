package jumpd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/secretbox"
)

const testMaster = "test-master-secret"

// seedJumpVM 造一台 VM（可选带托管凭据），复用 auth_test 的 testDB
func seedJumpVM(t *testing.T, db *gorm.DB, id uint, name, ip, status, credPass string) {
	t.Helper()
	vm := model.VM{ID: id, Name: name, IP: ip, Status: status, UUID: "uuid-" + itoa(int(id))}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("造 VM 失败: %v", err)
	}
	if credPass != "" {
		enc, salt, err := secretbox.SealWithMaster(testMaster, credPass)
		if err != nil {
			t.Fatalf("造凭据失败: %v", err)
		}
		if err := db.Create(&model.VMCredential{VMID: id, User: "root", Port: 22, PasswordEnc: enc, Salt: salt}).Error; err != nil {
			t.Fatalf("造凭据失败: %v", err)
		}
	}
}

func TestMenuQueryFiltering(t *testing.T) {
	db := testDB(t)
	viewer := seedUser(t, db, "op1", "x", "operator", true)
	admin := seedUser(t, db, "ad1", "x", "admin", true)

	seedJumpVM(t, db, 1, "alpha", "192.168.1.1", model.VMStatusRunning, "pass1") // op1 有效授权
	seedJumpVM(t, db, 2, "beta", "192.168.1.2", model.VMStatusRunning, "pass2")  // op1 过期授权
	seedJumpVM(t, db, 3, "gamma", "192.168.1.3", model.VMStatusRunning, "")      // 无凭据
	seedJumpVM(t, db, 4, "delta", "", model.VMStatusRunning, "pass4")            // 无 IP
	seedJumpVM(t, db, 5, "echo", "192.168.1.5", model.VMStatusShutOff, "pass5")  // 非运行中
	seedJumpVM(t, db, 6, "fox", "192.168.1.6", model.VMStatusRunning, "pass6")   // 无任何授权

	future := time.Now().Add(2 * time.Hour)
	past := time.Now().Add(-2 * time.Hour)
	db.Create(&model.VMGrant{UserID: viewer.ID, VMID: 1, ExpiresAt: &future, GrantedBy: admin.ID})
	db.Create(&model.VMGrant{UserID: viewer.ID, VMID: 2, ExpiresAt: &past, GrantedBy: admin.ID})

	// operator：有效授权 ∩ running ∩ 有 IP ∩ 有凭据 → 只有 alpha
	got, err := loadMenuAssets(db, viewer, false)
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if len(got) != 1 || got[0].Name != "alpha" {
		t.Fatalf("operator 菜单应只有 alpha，得到 %+v", got)
	}

	// admin：不看授权表 → running∩有IP∩有凭据 全量（含 beta：过期授权是 op1 的，与 admin 无关）
	got, err = loadMenuAssets(db, admin, true)
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if len(got) != 3 || got[0].Name != "alpha" || got[1].Name != "beta" || got[2].Name != "fox" {
		t.Fatalf("admin 菜单应为 alpha+beta+fox，得到 %+v", got)
	}
}

func TestRevalidateSelection(t *testing.T) {
	db := testDB(t)
	viewer := seedUser(t, db, "op1", "x", "operator", true)
	admin := seedUser(t, db, "ad1", "x", "admin", true)
	seedJumpVM(t, db, 10, "target", "192.168.2.10", model.VMStatusRunning, "real-secret")
	past := time.Now().Add(-time.Hour)
	db.Create(&model.VMGrant{UserID: viewer.ID, VMID: 10, ExpiresAt: &past, GrantedBy: admin.ID})

	// 授权已过期 → 拒绝（Review Focus #1：菜单展示后授权可能已失效）
	if _, err := revalidateSelection(db, viewer, 10, false, testMaster); err == nil {
		t.Fatalf("授权过期应拒绝")
	}

	// admin 不看授权 → 通过，凭据解密正确（Seal→Open 全链路）
	tgt, err := revalidateSelection(db, admin, 10, true, testMaster)
	if err != nil {
		t.Fatalf("admin 应通过: %v", err)
	}
	if tgt.User != "root" || tgt.Port != 22 || tgt.Password != "real-secret" {
		t.Fatalf("解密凭据不对: user=%q port=%d pass=%q", tgt.User, tgt.Port, tgt.Password)
	}
	if tgt.VM.Name != "target" {
		t.Fatalf("返回 VM 不对: %+v", tgt.VM)
	}

	// VM 关机 → 拒绝（Review Focus #2：选中瞬间状态可能已变）
	db.Model(&model.VM{ID: 10}).Update("status", model.VMStatusShutOff)
	if _, err := revalidateSelection(db, admin, 10, true, testMaster); err == nil {
		t.Fatalf("VM 已关机应拒绝")
	}

	// IP 清空 → 拒绝
	db.Model(&model.VM{ID: 10}).Updates(map[string]interface{}{"status": model.VMStatusRunning, "ip": ""})
	if _, err := revalidateSelection(db, admin, 10, true, testMaster); err == nil {
		t.Fatalf("IP 清空应拒绝")
	}

	// 凭据行不存在 → 拒绝（fail-closed）
	db.Model(&model.VM{ID: 10}).Update("ip", "192.168.2.10")
	db.Where("vm_id = ?", 10).Delete(&model.VMCredential{})
	if _, err := revalidateSelection(db, admin, 10, true, testMaster); err == nil {
		t.Fatalf("凭据缺失应拒绝")
	}
}

func TestRecordSessionLifecycle(t *testing.T) {
	db := testDB(t)
	uid := uint(7)
	s := openJumpSession(db, 10, "target", &uid, "stu", "10.9.9.9")
	if s == nil || s.ID == 0 {
		t.Fatalf("会话应建行成功")
	}
	if s.Type != "jump" || s.Status != "active" {
		t.Fatalf("type/status 应为 jump/active: %+v", s)
	}
	if s.UserID == nil || *s.UserID != 7 || s.ClientIP != "10.9.9.9" {
		t.Fatalf("UserID/ClientIP 应就位: %+v", s)
	}
	if s.StartedAt.IsZero() {
		t.Fatalf("StartedAt 应就位")
	}

	// 关闭：closed + ended_at 非空
	closeJumpSession(db, s.ID, "用户退出")
	var after model.ConsoleSession
	db.First(&after, s.ID)
	if after.Status != "closed" || after.EndedAt == nil {
		t.Fatalf("关闭后应 closed 且 ended_at 非空: %+v", after)
	}

	// 幂等：二次关闭不报错、不重复改
	closeJumpSession(db, s.ID, "用户退出")
	var again model.ConsoleSession
	db.First(&again, s.ID)
	if !after.EndedAt.Equal(*again.EndedAt) {
		t.Fatalf("二次关闭不应改写 ended_at")
	}
}

// panicReader 必 panic 的 reader：验证 safeCopy 的 recover 兜底
type panicReader struct{}

func (panicReader) Read([]byte) (int, error) { panic("boom: 模拟底层异常") }

func TestSafeCopyRecovers(t *testing.T) {
	// panic 不带出到调用方（生产中该函数跑在 goroutine 里，panic 外泄会带走进程）
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("safeCopy 不应把 panic 带出到调用方: %v", r)
			}
		}()
		safeCopy(&bytes.Buffer{}, panicReader{}, 1)
	}()

	// 正常路径：字节照常拷贝
	src := strings.NewReader("hello jump")
	var dst bytes.Buffer
	safeCopy(&dst, src, 1)
	if dst.String() != "hello jump" {
		t.Fatalf("正常拷贝内容不对: %q", dst.String())
	}
}
