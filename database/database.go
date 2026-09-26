package database

import (
	"fmt"
	"log"
	"time"

	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/model"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var DB *gorm.DB

func Init() {
	cfg := config.GlobalConfig

	// 构建DSN
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		cfg.DBUser,
		cfg.DBPassword,
		cfg.DBHost,
		cfg.DBPort,
		cfg.DBName,
	)

	// SQL 日志级别按模式切分：debug 保留 Info（开发可见全量 SQL）；release 降 Warn——
	// Info 会把绑定值（任务 payload、审计明细）打进 stdout，量大且敏感（全量审计 P2）
	logLevel := logger.Warn
	if cfg.ServerMode == "debug" {
		logLevel = logger.Info
	}
	var err error
	DB, err = gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logLevel),
	})
	if err != nil {
		log.Fatalf("连接数据库失败: %v", err)
	}

	// 配置连接池
	sqlDB, err := DB.DB()
	if err != nil {
		log.Fatalf("获取数据库连接失败: %v", err)
	}
	sqlDB.SetMaxOpenConns(50)
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetConnMaxLifetime(time.Hour)
	sqlDB.SetConnMaxIdleTime(10 * time.Minute) // 防 MySQL wait_timeout 边界撞闲置死链

	// 自动迁移
	err = DB.AutoMigrate(
		&model.User{},
		&model.Host{},
		&model.VM{},
		&model.Image{},
		&model.AuditLog{},
		&model.Task{},
		&model.ConsoleSession{},
		&model.Setting{},
		&model.Alert{},
		&model.PoolMeta{},
		&model.VMGrant{},
		&model.UserGroup{},
		&model.UserGroupMember{},
		&model.VMGroupGrant{},
		&model.GrantRequest{},
		&model.CronRun{},
		&model.VMCredential{},
		&model.ScheduledTask{},
		&model.CloudInitTemplate{},
		&model.HostKey{},
	)
	if err != nil {
		log.Fatalf("数据库迁移失败: %v", err)
	}

	log.Println("数据库连接成功")
}

func GetDB() *gorm.DB {
	return DB
}
