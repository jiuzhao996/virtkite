package model

import "time"

// Setting 系统可写配置项（KV 表）。
// 与 config 包的环境变量静态配置不同：这里的项可在运行时经 PUT /api/settings 修改并立即生效。
type Setting struct {
	Key       string    `gorm:"size:100;primaryKey" json:"key"`
	Value     string    `gorm:"size:500;not null" json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName 指定表名（settings 与 MySQL 保留字语义易混，用 system_settings 更明确）。
func (Setting) TableName() string { return "system_settings" }
