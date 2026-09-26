package model

import "time"

// UserGroup 用户组（教学场景：给「全班」批量授权，而不是逐个用户）。
type UserGroup struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Name        string    `gorm:"size:100;uniqueIndex;not null" json:"name"`
	Description string    `gorm:"size:255" json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// TableName 指定表名
func (UserGroup) TableName() string { return "user_groups" }

// UserGroupMember 组成员关系（n:m）
type UserGroupMember struct {
	ID       uint      `gorm:"primaryKey" json:"id"`
	GroupID  uint      `gorm:"not null;uniqueIndex:idx_ug_pair,priority:1" json:"group_id"`
	UserID   uint      `gorm:"not null;uniqueIndex:idx_ug_pair,priority:2" json:"user_id"`
	JoinedAt time.Time `json:"joined_at"`
}

// TableName 指定表名
func (UserGroupMember) TableName() string { return "user_group_members" }

// VMGroupGrant 组级资产授权：组内全部成员获得该 VM 的可见性。
// ExpiresAt 语义与 VMGrant 一致（nil=长期有效，过期即失效——查询时判定，无需清扫协程）。
type VMGroupGrant struct {
	ID        uint       `gorm:"primaryKey" json:"id"`
	GroupID   uint       `gorm:"not null;uniqueIndex:idx_vgg_pair,priority:1" json:"group_id"`
	VMID      uint       `gorm:"not null;index;uniqueIndex:idx_vgg_pair,priority:2" json:"vm_id"`
	ExpiresAt *time.Time `json:"expires_at"`
	GrantedBy uint       `json:"granted_by"`
	CreatedAt time.Time  `json:"created_at"`
}

// TableName 指定表名
func (VMGroupGrant) TableName() string { return "vm_group_grants" }
