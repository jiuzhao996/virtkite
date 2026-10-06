package jumpd

import (
	"fmt"
	"log"
	"time"

	"gorm.io/gorm"

	"github.com/jiuzhao/vmops/middleware"
	"github.com/jiuzhao/vmops/model"
)

// authenticator SSH 跳板密码认证：users 表 bcrypt 校验 + viewer 拒绝 + 双维度失败限流。
// 校验顺序 = 限流 → 查库 → bcrypt → IsActive → viewer 拒绝 → success 清零；
// 与 handler/auth.go Login 的先查锁后验密口径一致（Review Focus #3：锁定态下正确密码也不放行）。
type authenticator struct {
	DB          *gorm.DB
	ipLimiter   *limiter
	userLimiter *limiter
}

// newAuthenticator 双维度限流参数集中处（对齐 JumpServer 口径）：
// IP 维度放宽到 20 次/分——教学场景全班共享 NAT 出口 IP，一个学生输错密码不该
// 把全班锁在门外；用户名维度保持 5 次/分——单账号防爆破，攻击者换 IP 也锁得住。
func newAuthenticator(db *gorm.DB) *authenticator {
	return &authenticator{
		DB:          db,
		ipLimiter:   newLimiter(time.Minute, 20),
		userLimiter: newLimiter(time.Minute, 5),
	}
}

// authenticate 认证一个跳板登录。
// 返回 (用户, 用户可见文案, 错误)：拒绝原因走文案（含 nil 用户 + 非 nil 错误的约定——
// 错误仅用于 DB 故障等 fail-closed 场景，文案同时给出中文提示）；密码绝不进日志。
func (a *authenticator) authenticate(ip, username, password string) (*model.User, string, error) {
	if locked, remain := a.ipLimiter.blocked(ip); locked {
		return nil, fmt.Sprintf("失败次数过多，请 %d 秒后再试", int(remain.Seconds())+1), fmt.Errorf("IP 已被限流锁定")
	}
	if locked, remain := a.userLimiter.blocked(username); locked {
		return nil, fmt.Sprintf("该账号失败次数过多，请 %d 秒后再试", int(remain.Seconds())+1), fmt.Errorf("账号已被限流锁定")
	}

	var u model.User
	err := a.DB.Where("username = ?", username).First(&u).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			a.failBoth(ip, username) // 用户不存在同样计一次爆破成本
			return nil, "用户名或密码不正确", fmt.Errorf("用户不存在")
		}
		// DB 故障 fail-closed：拒绝 + 留痕（不泄露内部细节给用户）
		log.Printf("[jumpd] 认证查库失败: %v", err)
		return nil, "服务暂时不可用，请稍后再试", fmt.Errorf("查询用户失败: %w", err)
	}

	if !middleware.CheckPassword(password, u.PasswordHash) {
		a.failBoth(ip, username)
		return nil, "用户名或密码不正确", fmt.Errorf("密码校验失败")
	}

	if !u.IsActive {
		return nil, "账号已被禁用", fmt.Errorf("账号已禁用")
	}

	// viewer 的拒绝不在认证层做：SSH 密码失败无法携带自定义文案（客户端只能看到
	// 通用 Permission denied），改为认证放行、由会话层提示后断开（server.go rejectReadOnly，
	// 对齐 Gitea/koko 的惯例）。limiter 照常 success（这是合法登录不是爆破）。
	a.ipLimiter.success(ip)
	a.userLimiter.success(username)
	return &u, "", nil
}

// failBoth 一次失败同时计入两个维度
func (a *authenticator) failBoth(ip, username string) {
	a.ipLimiter.fail(ip)
	a.userLimiter.fail(username)
}

// jumpdRoles 允许使用跳板的角色（与 RBAC 语义一致：viewer 只读无终端通道；
// 白名单制而非黑名单——未来新增角色默认拒绝，需显式放行）
var jumpdRoles = map[string]bool{"admin": true, "operator": true}

// roleAllowed 角色白名单判断
func roleAllowed(role string) bool {
	return jumpdRoles[role]
}
