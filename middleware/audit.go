package middleware

import (
	"bytes"
	"io"
	"log"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"gorm.io/gorm"
)

// AuditMiddleware 审计日志中间件
func AuditMiddleware(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 记录请求开始时间
		startTime := time.Now()

		// 获取请求信息
		method := c.Request.Method
		path := c.Request.URL.Path
		sourceIP := c.ClientIP()

		// 静态资源与健康检查不写审计，避免刷屏
		if strings.HasPrefix(path, "/static") || path == "/api/health" {
			c.Next()
			return
		}

		// 获取用户信息
		var userID *uint
		var username string
		if uid, exists := c.Get("user_id"); exists {
			uidUint := uid.(uint)
			userID = &uidUint
		}
		if uname, exists := c.Get("username"); exists {
			username = uname.(string)
		}

		// 读取请求体（用于审计）
		var requestBody []byte
		if c.Request.Body != nil {
			requestBody, _ = io.ReadAll(c.Request.Body)
			c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
		}

		// 处理请求
		c.Next()

		// 计算处理时间
		duration := time.Since(startTime)

		// 判断操作类型和状态
		action := determineAction(method, path)
		objectType := determineObjectType(path)
		status := "success"
		if c.Writer.Status() >= 400 {
			status = "failed"
		}

		// 记录审计日志
		auditLog := model.AuditLog{
			UserID:     userID,
			Username:   username,
			Action:     action,
			ObjectType: objectType,
			SourceIP:   sourceIP,
			Status:     status,
			Detail:     "请求处理时间: " + duration.String(),
		}

		// 同步写入审计日志
		if err := db.Create(&auditLog).Error; err != nil {
			log.Printf("写入审计日志失败: %v", err)
		}
	}
}

// determineAction 根据请求方法和路径确定操作类型
func determineAction(method, path string) string {
	// 认证相关
	if path == "/api/auth/login" {
		return "login"
	}
	if path == "/api/auth/logout" {
		return "logout"
	}

	// 虚拟机相关
	if strings.Contains(path, "/api/vms") {
		switch method {
		case "POST":
			if path == "/api/vms/import" {
				return "import_vm"
			}
			if strings.Contains(path, "/start") {
				return "start_vm"
			}
			if strings.Contains(path, "/stop") {
				return "stop_vm"
			}
			if strings.Contains(path, "/restart") {
				return "restart_vm"
			}
			return "create_vm"
		case "DELETE":
			return "delete_vm"
		}
	}

	// 宿主机相关
	if strings.Contains(path, "/api/hosts") {
		switch method {
		case "POST":
			return "create_host"
		case "PUT":
			return "update_host"
		case "DELETE":
			return "delete_host"
		}
	}

	// 镜像相关
	if strings.Contains(path, "/api/images") {
		switch method {
		case "POST":
			return "upload_image"
		case "DELETE":
			return "delete_image"
		}
	}

	// 默认操作
	return "access"
}

// determineObjectType 根据路径确定对象类型
func determineObjectType(path string) string {
	if strings.Contains(path, "/api/vms") {
		return "vm"
	}
	if strings.Contains(path, "/api/hosts") {
		return "host"
	}
	if strings.Contains(path, "/api/images") {
		return "image"
	}
	if strings.Contains(path, "/api/users") {
		return "user"
	}
	return "system"
}
