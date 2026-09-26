package jumpd

import (
	"fmt"
	"log"

	"gorm.io/gorm"

	"github.com/jiuzhao/vmops/middleware"
	"github.com/jiuzhao/vmops/model"
)

// authenticator SSH 跳板密码认证：users 表 bcrypt 校验 + viewer 拒绝 + 失败限流。
// 校验顺序 = 限流 → 查库 → bcrypt → IsActive → viewer 拒绝 → success 清零；
// 与 handler/auth.go Login 的先查锁后验密口径一致（Review Focus #3：锁定态下正确密码也不放行）。
type authenticator struct {
	DB      *gorm.DB
	limiter *limiter
}

// authenticate 认证一个跳板登录。
// 返回 (用户, 用户可见文案, 错误)：拒绝原因走文案（含 nil 用户 + 非 nil 错误的约定——
// 错误仅用于 DB 故障等 fail-closed 场景，文案同时给出中文提示）；密码绝不进日志。
func (a *authenticator) authenticate(ip, username, password string) (*model.User, string, error) {
	if locked, remain := a.limiter.blocked(ip); locked {
		return nil, fmt.Sprintf("失败次数过多，请 %d 秒后再试", int(remain.Seconds())+1), fmt.Errorf("IP 已被限流锁定")
	}

	var u model.User
	err := a.DB.Where("username = ?", username).First(&u).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			a.limiter.fail(ip) // 用户不存在同样计一次爆破成本
			return nil, "用户名或密码不正确", fmt.Errorf("用户不存在")
		}
		// DB 故障 fail-closed：拒绝 + 留痕（不泄露内部细节给用户）
		log.Printf("[jumpd] 认证查库失败: %v", err)
		return nil, "服务暂时不可用，请稍后再试", fmt.Errorf("查询用户失败: %w", err)
	}

	if !middleware.CheckPassword(password, u.PasswordHash) {
		a.limiter.fail(ip)
		return nil, "用户名或密码不正确", fmt.Errorf("密码校验失败")
	}

	if !u.IsActive {
		return nil, "账号已被禁用", fmt.Errorf("账号已禁用")
	}

	// 角色白名单：admin/operator 可用；viewer 与未知角色一律拒绝（fail-closed）
	if !roleAllowed(u.Role) {
		return nil, "只读角色不支持终端登录", fmt.Errorf("角色 %q 无终端权限", u.Role)
	}

	a.limiter.success(ip)
	return &u, "", nil
}

// jumpdRoles 允许使用跳板的角色（与 RBAC 语义一致：viewer 只读无终端通道；
// 白名单制而非黑名单——未来新增角色默认拒绝，需显式放行）
var jumpdRoles = map[string]bool{"admin": true, "operator": true}

// roleAllowed 角色白名单判断
func roleAllowed(role string) bool {
	return jumpdRoles[role]
}
