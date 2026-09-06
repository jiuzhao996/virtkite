package model

import "time"

// PoolMeta 存储池平台侧元数据。
// libvirt 池 XML 没有 description/role 字段，池的「角色」（模板基盘/系统盘/数据盘等）
// 与描述只能由平台自己存。Role 为空时前端展示按路径自动推断的角色（handler.inferPoolRole）。
type PoolMeta struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	PoolName    string    `gorm:"size:64;uniqueIndex" json:"pool_name"`
	Role        string    `gorm:"size:20" json:"role"` // 空串 = 跟随自动推断
	Description string    `gorm:"size:500" json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// TableName 指定表名
func (PoolMeta) TableName() string {
	return "pool_meta"
}
