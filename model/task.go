package model

import (
	"time"
)

// 任务状态常量（tasks 包内有同值非导出常量；metrics 等外部消费方从这里取）。
const (
	TaskStatusPending = "pending"
	TaskStatusRunning = "running"
	TaskStatusSuccess = "success"
	TaskStatusFailed  = "failed"
)

// Task 异步任务模型（对应 JumpServer/PVE task 队列语义）。
type Task struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Type      string    `gorm:"size:50;index" json:"type"`
	Title     string    `gorm:"size:200" json:"title"`
	Status    string    `gorm:"size:20;index" json:"status"`
	Progress  int       `gorm:"default:0" json:"progress"`
	Payload   string    `gorm:"type:text" json:"-"`
	Result    string    `gorm:"type:text" json:"result,omitempty"`
	Error     string    `gorm:"size:500" json:"error,omitempty"`
	UserID    *uint     `json:"user_id"`
	Username  string    `gorm:"size:100" json:"username"`
	VMID      *uint     `gorm:"index" json:"vm_id,omitempty"`
	VMName    string    `gorm:"size:100" json:"vm_name,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName 指定表名
func (Task) TableName() string {
	return "tasks"
}
