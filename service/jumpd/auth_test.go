package jumpd

import (
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/jiuzhao/vmops/middleware"
	"github.com/jiuzhao/vmops/model"
)

// testDB 项目首个 GORM 测试库（go.mod 此前无任何 sqlite 驱动，见计划 Global Constraints #10）。
// 纯 Go 驱动 glebarez/sqlite（无 CGO），仅测试代码 import，生产代码不碰。
func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Discard, // 静默 GORM 内部日志（record not found 等），保持测试输出干净
	})
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.VM{}, &model.VMGrant{}, &model.VMCredential{}, &model.ConsoleSession{}); err != nil {
		t.Fatalf("AutoMigrate 失败: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.Close()
		}
	})
	return db
}

// seedUser 造用户：bcrypt 哈希经 middleware.HashPassword 生成（与生产同链路）。
// 注意：IsActive 有 gorm default:true 标签，显式 false 是零值会被 Create 省略而落列默认——
// 非活跃用户必须 Create 后再 Update 显式写 false。
func seedUser(t *testing.T, db *gorm.DB, username, password, role string, active bool) *model.User {
	t.Helper()
	hash, err := middleware.HashPassword(password)
	if err != nil {
		t.Fatalf("生成哈希失败: %v", err)
	}
	u := &model.User{Username: username, PasswordHash: hash, Role: role, IsActive: true}
	if err := db.Create(u).Error; err != nil {
		t.Fatalf("造用户失败: %v", err)
	}
	if !active {
		if err := db.Model(u).Update("is_active", false).Error; err != nil {
			t.Fatalf("置为禁用失败: %v", err)
		}
	}
	return u
}

func TestAuthenticate(t *testing.T) {
	db := testDB(t)
	seedUser(t, db, "stu", "goodpass", "operator", true)
	a := &authenticator{DB: db, limiter: newLimiter(time.Minute, 5)}

	// 正确密码 → 返回用户
	u, msg, err := a.authenticate("10.1.0.1", "stu", "goodpass")
	if err != nil || u == nil {
		t.Fatalf("正确密码应通过: err=%v msg=%q", err, msg)
	}
	if u.Role != "operator" || u.ID == 0 {
		t.Fatalf("返回用户字段不对: %+v", u)
	}

	// 错误密码 → 拒绝且限流计数
	for i := 0; i < 3; i++ {
		_, _, _ = a.authenticate("10.1.0.2", "stu", "wrongpass")
	}
	if locked, _ := a.limiter.blocked("10.1.0.2"); locked {
		t.Fatalf("3 次失败不应锁定")
	}
	// 再错 2 次凑满 5 → 锁定（证明每次失败都被计数）
	for i := 0; i < 2; i++ {
		_, _, _ = a.authenticate("10.1.0.2", "stu", "wrongpass")
	}
	if locked, _ := a.limiter.blocked("10.1.0.2"); !locked {
		t.Fatalf("累计 5 次密码错误应触发限流锁定")
	}

	// 用户不存在 → 拒绝且计数（5 次后锁定，证明不存在的用户名同样计爆破成本）
	for i := 0; i < 5; i++ {
		_, _, _ = a.authenticate("10.1.0.3", "ghost", "whatever")
	}
	if locked, _ := a.limiter.blocked("10.1.0.3"); !locked {
		t.Fatalf("用户不存在的失败应计数")
	}

	// 禁用账号 → 拒绝且文案明确
	seedUser(t, db, "off", "goodpass", "operator", false)
	_, msg, err = a.authenticate("10.1.0.4", "off", "goodpass")
	if err == nil || !strings.Contains(msg, "账号已被禁用") {
		t.Fatalf("禁用账号应拒绝且文案含「账号已被禁用」: err=%v msg=%q", err, msg)
	}
}

func TestAuthenticateViewerRejected(t *testing.T) {
	db := testDB(t)
	seedUser(t, db, "viewer1", "goodpass", "viewer", true)
	seedUser(t, db, "admin1", "goodpass", "admin", true)
	a := &authenticator{DB: db, limiter: newLimiter(time.Minute, 5)}

	_, msg, err := a.authenticate("10.2.0.1", "viewer1", "goodpass")
	if err == nil || !strings.Contains(msg, "只读角色不支持终端登录") {
		t.Fatalf("viewer 应被拒绝且文案正确: err=%v msg=%q", err, msg)
	}
	if locked, _ := a.limiter.blocked("10.2.0.1"); locked {
		t.Fatalf("viewer 拒绝是权限语义，不应计入爆破限流")
	}

	if u, msg, err := a.authenticate("10.2.0.2", "admin1", "goodpass"); err != nil || u == nil {
		t.Fatalf("admin 应通过: err=%v msg=%q", err, msg)
	}
}

func TestAuthenticateLocked(t *testing.T) {
	db := testDB(t)
	seedUser(t, db, "stu", "goodpass", "operator", true)
	l := newLimiter(time.Minute, 5)
	for i := 0; i < 5; i++ {
		l.fail("10.3.0.1")
	}
	a := &authenticator{DB: db, limiter: l}

	// 锁定态下正确密码也拒绝，且拒绝发生在密码校验之前（先查锁）
	_, msg, err := a.authenticate("10.3.0.1", "stu", "goodpass")
	if err == nil {
		t.Fatalf("锁定态应拒绝")
	}
	if !strings.Contains(msg, "失败次数过多") {
		t.Fatalf("锁定文案应提示失败次数过多: %q", msg)
	}
}
