package model

import "time"

// GrantRequestStatus 授权申请状态
const (
	GrantRequestPending  = "pending"  // 待审批
	GrantRequestApproved = "approved" // 已批准（授权已写入 vm_grants）
	GrantRequestRejected = "rejected" // 已驳回
)

// GrantRequest 资产授权申请（学生自助申请 → 教师审批 → 限时授权，借鉴 JumpServer 工单流）。
// 批准动作的唯一副作用是写入一条 vm_grants（带 expires_at）——审批不是独立权限体系，
// 只是「授权的申请入口」，可见性判定完全复用既有 vm_grants 链路。
type GrantRequest struct {
	ID         uint       `gorm:"primaryKey" json:"id"`
	UserID     uint       `gorm:"not null;index" json:"user_id"`
	Username   string     `gorm:"size:50" json:"username"`
	VMID       uint       `gorm:"not null;index" json:"vm_id"`
	VMName     string     `gorm:"size:100" json:"vm_name"`
	Reason     string     `gorm:"type:text" json:"reason"`
	Hours      int        `gorm:"default:2" json:"hours"` // 申请时长（小时），审批时可调整
	Status     string     `gorm:"size:20;index;default:pending" json:"status"`
	DecidedBy  *uint      `json:"decided_by"`
	DecideNote string     `gorm:"size:255" json:"decide_note"`
	DecidedAt  *time.Time `json:"decided_at"`
	CreatedAt  time.Time  `gorm:"index" json:"created_at"`
}

// TableName 指定表名
func (GrantRequest) TableName() string { return "grant_requests" }
