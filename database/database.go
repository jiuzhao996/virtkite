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

	var err error
	DB, err = gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
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
	)
	if err != nil {
		log.Fatalf("数据库迁移失败: %v", err)
	}

	log.Println("数据库连接成功")
}

func GetDB() *gorm.DB {
	return DB
}
