package model

import "time"

// HostKey SSH 主机指纹（TOFU：Trust On First Use，首次连接记录、后续连接比对）。
// Web 终端（handler/terminal.go）与 VM 文件管理（service/vmssh）两条 SSH 通道共用：
// 首次连上某 host:port 时把服务器公钥的 SHA256 指纹落库；再次连接指纹不一致即拒绝，
// 防中间人替换主机密钥。虚拟机重建会换主机密钥，此时由管理员删除旧指纹记录放行重录
// （DELETE /api/ssh-host-keys/:id，仅 admin）。
type HostKey struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Host        string    `gorm:"size:255;not null;uniqueIndex:idx_host_key_host_port,priority:1" json:"host"`
	Port        int       `gorm:"not null;uniqueIndex:idx_host_key_host_port,priority:2" json:"port"`
	KeyType     string    `gorm:"size:50;not null" json:"key_type"`     // 公钥算法名，如 ssh-ed25519
	Fingerprint string    `gorm:"size:100;not null" json:"fingerprint"` // SHA256:...（ssh.FingerprintSHA256 格式）
	CreatedAt   time.Time `json:"created_at"`                           // 首次连接（信任建立）时间
}

// TableName 指定表名
func (HostKey) TableName() string {
	return "host_keys"
}
