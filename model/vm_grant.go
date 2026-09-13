package model

import "time"

// VMGrant VM 资产授权（借鉴堡垒机 4A 模型：分配是一等实体，授权决定可见性）。
// 一行 = 「把某台 VM 授给某个用户」；用户可见/可操作的 VM = 其有效授权的并集。
// 与 JumpServer 的差异（刻意取舍）：不做 用户组×节点树×账号×动作 的四维矩阵，
// 单宿主机教学场景一张平面表足够——多级模型的重是它的场景，不是它的好。
// ExpiresAt 为 nil 表示长期有效；过期授权在判定时即视为不存在（查询时校验，无需清扫协程）。
type VMGrant struct {
	ID        uint       `gorm:"primaryKey" json:"id"`
	UserID    uint       `gorm:"not null;uniqueIndex:idx_grant_user_vm,priority:1" json:"user_id"`
	VMID      uint       `gorm:"not null;index;uniqueIndex:idx_grant_user_vm,priority:2" json:"vm_id"`
	ExpiresAt *time.Time `json:"expires_at"` // null = 长期有效；到期即失效（软回收，行保留可查授权历史）
	GrantedBy uint       `json:"granted_by"` // 授权人（admin 用户 ID）
	CreatedAt time.Time  `json:"created_at"`
}
