package jumpd

import (
	"strconv"
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
	seedUser(t, db, "mate", "goodpass", "operator", true) // 同 IP 的同学（NAT 邻居）
	a := newAuthenticator(db)

	// 正确密码 → 返回用户
	u, msg, err := a.authenticate("10.1.0.1", "stu", "goodpass")
	if err != nil || u == nil {
		t.Fatalf("正确密码应通过: err=%v msg=%q", err, msg)
	}
	if u.Role != "operator" || u.ID == 0 {
		t.Fatalf("返回用户字段不对: %+v", u)
	}

	// 错误密码 ×5（同用户同 IP）→ 账号维度锁定；IP 维度 5<20 不锁（NAT 教室不连坐）
	for i := 0; i < 5; i++ {
		_, _, _ = a.authenticate("10.1.0.2", "stu", "wrongpass")
	}
	if locked, _ := a.userLimiter.blocked("stu"); !locked {
		t.Fatalf("同账号 5 次失败应触发账号维度锁定")
	}
	if locked, _ := a.ipLimiter.blocked("10.1.0.2"); locked {
		t.Fatalf("IP 维度上限 20，5 次失败不应锁全班共享的出口 IP")
	}
	// NAT 不误伤：同 IP 的同学照常登录
	if u, msg, err := a.authenticate("10.1.0.2", "mate", "goodpass"); err != nil || u == nil {
		t.Fatalf("同 IP 其他用户不应被连坐: err=%v msg=%q", err, msg)
	}
	// 账号维度跟着人走：换 IP 也进不来（锁定态下正确密码同样拒绝）
	if _, msg, err := a.authenticate("10.9.9.9", "stu", "goodpass"); err == nil || !strings.Contains(msg, "失败次数过多") {
		t.Fatalf("锁定的账号换 IP 应被拒: err=%v msg=%q", err, msg)
	}

	// 用户不存在 → 同样计爆破成本（账号维度锁定该假名）
	for i := 0; i < 5; i++ {
		_, _, _ = a.authenticate("10.1.0.3", "ghost", "whatever")
	}
	if locked, _ := a.userLimiter.blocked("ghost"); !locked {
		t.Fatalf("不存在的用户名同样应计数锁定")
	}

	// 禁用账号 → 拒绝且文案明确
	seedUser(t, db, "off", "goodpass", "operator", false)
	_, msg, err = a.authenticate("10.1.0.4", "off", "goodpass")
	if err == nil || !strings.Contains(msg, "账号已被禁用") {
		t.Fatalf("禁用账号应拒绝且文案含「账号已被禁用」: err=%v msg=%q", err, msg)
	}
}

// TestAuthenticateIPLock 单一来源扫号（轮换用户名让每个账号维度只计 1 次）由 IP 维度兜住：
// 校园场景 IP 上限放宽的前提是它仍能拦住单源爆破
func TestAuthenticateIPLock(t *testing.T) {
	db := testDB(t)
	seedUser(t, db, "stu", "goodpass", "operator", true)
	a := newAuthenticator(db)

	for i := 0; i < 20; i++ {
		_, _, _ = a.authenticate("10.5.0.1", "bot"+strconv.Itoa(i), "x")
	}
	if locked, _ := a.ipLimiter.blocked("10.5.0.1"); !locked {
		t.Fatalf("单源 20 次失败应触发 IP 维度锁定")
	}
	// IP 锁定后连正确密码也拒绝（先查锁后验密）
	_, msg, err := a.authenticate("10.5.0.1", "stu", "goodpass")
	if err == nil || !strings.Contains(msg, "失败次数过多") {
		t.Fatalf("IP 锁定态应拒绝: err=%v msg=%q", err, msg)
	}
}

func TestAuthenticateViewerPassesToSessionLayer(t *testing.T) {
	db := testDB(t)
	seedUser(t, db, "viewer1", "goodpass", "viewer", true)
	seedUser(t, db, "admin1", "goodpass", "admin", true)
	a := newAuthenticator(db)

	// Ruling：SSH 密码失败无法携带自定义文案，viewer 改为认证放行、会话层拒绝
	// （server.go rejectReadOnly），此处断言放行 + 角色白名单把 viewer 挡在会话层
	u, msg, err := a.authenticate("10.2.0.1", "viewer1", "goodpass")
	if err != nil || u == nil || msg != "" {
		t.Fatalf("viewer 认证应放行（会话层拒绝）: err=%v msg=%q", err, msg)
	}
	if roleAllowed("viewer") {
		t.Fatalf("viewer 不应通过角色白名单")
	}
	if !roleAllowed("operator") || !roleAllowed("admin") {
		t.Fatalf("operator/admin 应通过角色白名单")
	}
	if locked, _ := a.ipLimiter.blocked("10.2.0.1"); locked {
		t.Fatalf("合法登录不应计入爆破限流")
	}

	if u, msg, err := a.authenticate("10.2.0.2", "admin1", "goodpass"); err != nil || u == nil {
		t.Fatalf("admin 应通过: err=%v msg=%q", err, msg)
	}
}

func TestAuthenticateLocked(t *testing.T) {
	db := testDB(t)
	seedUser(t, db, "stu", "goodpass", "operator", true)
	l := newLimiter(time.Minute, 20)
	for i := 0; i < 20; i++ {
		l.fail("10.3.0.1")
	}
	a := &authenticator{DB: db, ipLimiter: l, userLimiter: newLimiter(time.Minute, 5)}

	// 锁定态下正确密码也拒绝，且拒绝发生在密码校验之前（先查锁）
	_, msg, err := a.authenticate("10.3.0.1", "stu", "goodpass")
	if err == nil {
		t.Fatalf("锁定态应拒绝")
	}
	if !strings.Contains(msg, "失败次数过多") {
		t.Fatalf("锁定文案应提示失败次数过多: %q", msg)
	}
}
