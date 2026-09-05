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

		// 获取用户信息：审计中间件注册在全局（早于 JWT 中间件），
		// 因此需要自行解析 Bearer token 补齐 username/user_id。
		var userID *uint
		var username string
		if uid, exists := c.Get("user_id"); exists {
			uidUint := uid.(uint)
			userID = &uidUint
		}
		if uname, exists := c.Get("username"); exists {
			username = uname.(string)
		}
		if userID == nil || username == "" {
			if claims, err := claimsFromRequest(c.Request); err == nil {
				uidUint := claims.UserID
				userID = &uidUint
				username = claims.Username
			}
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

// determineAction 根据请求方法和路径确定操作类型。
// 说明：gin 路由注册静态段优先于 :id 参数段，此处用前缀 + 后缀匹配区分子路径。
func determineAction(method, path string) string {
	// 认证相关
	if path == "/api/auth/login" {
		return "login"
	}
	if path == "/api/auth/logout" {
		return "logout"
	}

	// 虚拟机相关
	if strings.HasPrefix(path, "/api/vms") {
		return vmAction(method, path)
	}

	// 宿主机相关
	if strings.HasPrefix(path, "/api/hosts") {
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
	if strings.HasPrefix(path, "/api/images") {
		switch method {
		case "POST":
			if strings.HasSuffix(path, "/upload") {
				return "upload_image"
			}
			if strings.HasSuffix(path, "/clone") {
				return "clone_image"
			}
		case "PUT":
			if strings.HasSuffix(path, "/template") {
				return "set_image_template"
			}
		case "DELETE":
			return "delete_image"
		}
	}

	// 网络相关
	if strings.HasPrefix(path, "/api/networks") {
		switch method {
		case "POST":
			return "create_network"
		case "PUT":
			return "update_network"
		case "DELETE":
			return "delete_network"
		}
	}

	// 存储池/卷相关
	if strings.HasPrefix(path, "/api/storage") {
		switch method {
		case "POST":
			return "create_volume"
		case "DELETE":
			return "delete_volume"
		}
	}

	// 默认操作
	return "access"
}

// vmAction 虚拟机子路径操作映射（含新增的 pause/resume/clone/设备热插拔/配置调整等）。
func vmAction(method, path string) string {
	switch method {
	case "POST":
		switch {
		case path == "/api/vms/import":
			return "import_vm"
		case strings.HasSuffix(path, "/start"):
			return "start_vm"
		case strings.HasSuffix(path, "/stop"):
			return "stop_vm"
		case strings.HasSuffix(path, "/restart"):
			return "restart_vm"
		case strings.HasSuffix(path, "/pause"):
			return "pause_vm"
		case strings.HasSuffix(path, "/resume"):
			return "resume_vm"
		case strings.HasSuffix(path, "/clone"):
			return "clone_vm"
		case strings.HasSuffix(path, "/revert"):
			return "revert_snapshot"
		case strings.HasSuffix(path, "/snapshots"):
			return "create_snapshot"
		case strings.HasSuffix(path, "/devices/interfaces"):
			return "attach_nic"
		case strings.HasSuffix(path, "/devices/disks"):
			return "attach_disk"
		case path == "/api/vms":
			return "create_vm"
		}
	case "DELETE":
		switch {
		case strings.Contains(path, "/devices/interfaces/"):
			return "detach_nic"
		case strings.Contains(path, "/devices/disks/"):
			return "detach_disk"
		case strings.Contains(path, "/snapshots/"):
			return "delete_snapshot"
		case strings.HasPrefix(path, "/api/vms/"):
			return "delete_vm"
		}
	case "PUT":
		switch {
		case strings.HasSuffix(path, "/spec"):
			return "update_vm_spec"
		case strings.HasSuffix(path, "/xml"):
			return "update_vm_xml"
		case strings.HasSuffix(path, "/cpu"):
			return "set_vcpu"
		case strings.HasSuffix(path, "/memory"):
			return "set_memory"
		case strings.HasSuffix(path, "/autostart"):
			return "set_autostart"
		case strings.HasSuffix(path, "/boot"):
			return "set_boot"
		case strings.HasPrefix(path, "/api/vms/"):
			return "update_vm"
		}
	}
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
