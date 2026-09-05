package model

import (
	"time"
)

// ConsoleSession 控制台会话记录（谁在何时以何种方式连了哪台 VM）。
// VNC 走 websockify 中转，后端看不到断开事件，靠 token 解析刷新 last_seen + 过期清扫收敛；
// SSH/串口走后端 WS 桥，连接持有在内存注册表，可服务端强制断开，关闭时回写 ended_at。
type ConsoleSession struct {
	ID        uint       `gorm:"primaryKey" json:"id"`
	VMID      uint       `gorm:"index" json:"vm_id"`
	VMName    string     `gorm:"size:100;index" json:"vm_name"`
	UserID    *uint      `json:"user_id"`
	Username  string     `gorm:"size:100" json:"username"`
	Type      string     `gorm:"size:20;index" json:"type"` // vnc / ssh / serial
	ClientIP  string     `gorm:"size:45" json:"client_ip"`
	Status    string     `gorm:"size:20;index" json:"status"` // active / closed
	Token     string     `gorm:"size:100" json:"-"`           // VNC token（内部关联解析事件，不返回前端）
	StartedAt time.Time  `json:"started_at"`
	LastSeen  time.Time  `json:"last_seen"` // VNC token 最近一次被 websockify 解析
	EndedAt   *time.Time `json:"ended_at,omitempty"`
}

// TableName 指定表名
func (ConsoleSession) TableName() string {
	return "console_sessions"
}
