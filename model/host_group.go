package model

import "time"

// HostGroup 运维主机组：批量执行（adhoc / playbook / 定时任务）的目标快捷集合。
// 成员存 VM ID 的 JSON 数组——组是轻量便捷入口而非资产权威，VM 删除后残留 ID
// 在使用处按「存在且运行中」过滤即可，不做外键级联。
type HostGroup struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Name        string    `gorm:"size:50;not null;uniqueIndex" json:"name"` // 组名（如 ceph / k8s-node）
	Description string    `gorm:"size:200" json:"description"`              // 用途说明
	VMIDs       string    `gorm:"type:text" json:"vm_ids"`                  // 成员 VM ID 的 JSON 数组，如 [1,2,3]
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// TableName 指定表名
func (HostGroup) TableName() string { return "host_groups" }
