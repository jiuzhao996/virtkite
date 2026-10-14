package jumpd

import (
	"errors"
	"fmt"
	"log"
	"time"

	"gorm.io/gorm"

	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/dbx"
	"github.com/jiuzhao/vmops/service/secretbox"
)

// 本文件负责连接目标的选择与重验、以及跳板会话在 console_sessions 表的登记/关闭。
// 菜单可见资产的过滤在 menu.go，连接前必须现查重验（不信任菜单缓存）。

// jumpTarget 重验通过后的连接目标：所有字段都来自 DB 行（用户键盘输入不参与）
type jumpTarget struct {
	VM       model.VM
	User     string
	Port     int
	Password string // 明文，仅内存流转，禁止进日志/落库
}

// loadMenuAssets 查询某用户可见的菜单资产：running ∩ 有 IP ∩ 有托管凭据（∩ 有效授权，admin 豁免）。
// 授权「未过期」判定与 handler/vm_grant.go:45 逐字一致（fail-closed：查失败按空授权处理）。
func loadMenuAssets(db *gorm.DB, user *model.User, isAdmin bool) ([]menuVM, error) {
	var vms []model.VM
	if err := db.Where("status = ? AND ip <> ''", model.VMStatusRunning).Find(&vms).Error; err != nil {
		return nil, fmt.Errorf("查询虚拟机列表失败: %w", err)
	}

	authSet := map[uint]bool{}
	if !isAdmin {
		var ids []uint
		// 与 handler/vm_grant.go:45 的未过期条件逐字一致
		if err := db.Model(&model.VMGrant{}).
			Where("user_id = ? AND (expires_at IS NULL OR expires_at > ?)", user.ID, time.Now()).
			Pluck("vm_id", &ids).Error; err != nil {
			return nil, fmt.Errorf("查询授权失败: %w", err)
		}
		for _, id := range ids {
			authSet[id] = true
		}
	}

	var credIDs []uint
	if err := db.Model(&model.VMCredential{}).Pluck("vm_id", &credIDs).Error; err != nil {
		return nil, fmt.Errorf("查询托管凭据失败: %w", err)
	}
	credSet := map[uint]bool{}
	for _, id := range credIDs {
		credSet[id] = true
	}

	return filterMenuAssets(vms, authSet, credSet, isAdmin), nil
}

// revalidateSelection 连接前的现查重验（Review Focus #1/#2）：菜单展示到按键选择之间存在
// 时间窗，授权可能被收回、VM 可能关机、IP 可能清空——一切以当前 DB 行为准，绝不信任菜单缓存。
func revalidateSelection(db *gorm.DB, user *model.User, vmID uint, isAdmin bool, masterSecret string) (*jumpTarget, error) {
	var vm model.VM
	if err := db.First(&vm, vmID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("虚拟机不存在或已删除")
		}
		log.Printf("[jumpd] 重验查库失败，fail-closed 拒绝: %v", err)
		return nil, fmt.Errorf("服务暂时不可用，请稍后再试")
	}
	if vm.Status != model.VMStatusRunning {
		return nil, fmt.Errorf("虚拟机不在运行中，无法连接")
	}
	if vm.IP == "" {
		return nil, fmt.Errorf("虚拟机尚未获取 IP，无法连接")
	}

	if !isAdmin {
		var cnt int64
		if err := db.Model(&model.VMGrant{}).
			Where("user_id = ? AND vm_id = ? AND (expires_at IS NULL OR expires_at > ?)", user.ID, vmID, time.Now()).
			Count(&cnt).Error; err != nil {
			log.Printf("[jumpd] 重验授权查库失败，fail-closed 拒绝: %v", err)
			return nil, fmt.Errorf("授权校验失败，请稍后再试")
		}
		if cnt == 0 {
			return nil, fmt.Errorf("你已无该虚拟机的连接授权")
		}
	}

	var cred model.VMCredential
	if err := db.Where("vm_id = ?", vmID).First(&cred).Error; err != nil {
		return nil, fmt.Errorf("该虚拟机未托管 SSH 凭据，请先在 Web 端凭据面板保存")
	}
	plain, err := secretbox.OpenWithMaster(masterSecret, cred.Salt, cred.PasswordEnc)
	if err != nil {
		log.Printf("[jumpd] 凭据解密失败 vm=%d（主密钥是否轮换过？）: %v", vmID, err)
		return nil, fmt.Errorf("凭据解密失败，请在 Web 端重新保存该虚拟机的 SSH 凭据")
	}

	return &jumpTarget{VM: vm, User: cred.User, Port: cred.Port, Password: string(plain)}, nil
}

// 会话状态与类型取值与 model/console_sessions 现有风格一致（active/closed；type 增加 jump）
const (
	sessionTypeJump = "jump"
	sessionActive   = "active"
	sessionClosed   = "closed"
)

// openJumpSession 跳板会话登记（先例 registry.go OpenVNC 的直建行模式，不经 websocket 注册表）。
// 创建失败只留痕不阻断——审计缺失不应阻止合法连接，但必须有迹可查。
func openJumpSession(db *gorm.DB, vmID uint, vmName string, userID *uint, username, clientIP string) *model.ConsoleSession {
	now := time.Now()
	s := &model.ConsoleSession{
		VMID: vmID, VMName: vmName, Type: sessionTypeJump,
		UserID: userID, Username: username, ClientIP: clientIP,
		Status: sessionActive, StartedAt: now, LastSeen: now,
	}
	if err := db.Create(s).Error; err != nil {
		log.Printf("[jumpd] 会话登记失败 vm=%s user=%s err=%v", vmName, username, err)
		return nil
	}
	return s
}

// closeJumpSession 会话关闭（先例 registry.go Close 的 dbx 收口模式）。
// 幂等：只改 active 行，二次调用 0 行命中即无事发生。
func closeJumpSession(db *gorm.DB, id uint, reason string) {
	if id == 0 {
		return
	}
	dbx.PersistBestEffort(db, "jumpd-session-close", func() error {
		now := time.Now()
		return db.Model(&model.ConsoleSession{}).
			Where("id = ? AND status = ?", id, sessionActive).
			Updates(map[string]interface{}{"status": sessionClosed, "ended_at": &now}).Error
	})
	log.Printf("[jumpd] 会话 %d 关闭（%s）", id, reason)
}
