package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/database"
	"github.com/jiuzhao/vmops/handler"
	"github.com/jiuzhao/vmops/middleware"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/console"
	"github.com/jiuzhao/vmops/service/tasks"
	"github.com/joho/godotenv"
	"gorm.io/gorm"
)

func main() {
	// 加载环境变量
	if err := godotenv.Load(); err != nil {
		log.Println("警告: .env文件不存在或加载失败")
	}

	// 初始化配置
	config.Init()

	// release 模式下必须配置 JWT_SECRET_KEY
	if config.GlobalConfig.ServerMode == "release" && config.GlobalConfig.JWTSecretKey == "" {
		log.Fatalf("JWT_SECRET_KEY is required in release mode")
	}

	// 初始化数据库
	database.Init()
	db := database.GetDB()

	// 启动收敛：内存队列/连接随进程消失，DB 里残留的 pending/running 任务与 ssh/serial 会话
	// 置终态，避免重启后幽灵任务与幽灵会话（VNC 靠 last_seen 过期清扫收敛，无需处理）
	sweepStaleRecords(db)

	// 初始化Gin
	if config.GlobalConfig.ServerMode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.Default()

	// 注册中间件
	r.Use(middleware.CORSMiddleware())
	r.Use(middleware.AuditMiddleware(db))

	// 静态文件服务：优先托管 Vite 构建产物 web/dist，缺失时回退到 static/index.html。
	// 用 os.ReadFile + c.Data 返回 index.html，规避 gin 对含 .html 路径的 301 目录索引重定向怪癖。
	exePath, _ := os.Executable()
	exeDir := filepath.Dir(exePath)
	webDir := filepath.Join(exeDir, "web", "dist")
	indexFile := filepath.Join(webDir, "index.html")
	indexBytes, err := os.ReadFile(indexFile)
	if err != nil {
		// 回退：旧版单文件前端
		indexFile = filepath.Join(exeDir, "static", "index.html")
		indexBytes, err = os.ReadFile(indexFile)
		if err != nil {
			log.Fatalf("读取前端入口失败（请先 `cd web && npm run build`）: %v", err)
		}
	}

	serveIndex := func(c *gin.Context) {
		c.Data(200, "text/html; charset=utf-8", indexBytes)
	}
	r.GET("/", serveIndex)
	r.NoRoute(serveIndex)

	// 托管 Vite 构建的静态资源（js/css 等），避免被 NoRoute 兜底为 index.html
	if info, statErr := os.Stat(filepath.Join(webDir, "assets")); statErr == nil && info.IsDir() {
		r.Static("/assets", filepath.Join(webDir, "assets"))
	}

	// 初始化Handler
	authHandler := handler.NewAuthHandler(db)
	userHandler := handler.NewUserHandler(db)
	hostHandler := handler.NewHostHandler(db)
	// 异步任务管理器单例：耗时操作（创建/删除/克隆/优雅关机）走后台 worker
	taskMgr := tasks.NewManager(db)
	tasks.RegisterVMTasks(taskMgr)
	// 控制台会话注册表单例：VNC/SSH/串口连接跟踪 + 服务端强制断开 + VNC 过期清扫
	consoleRegistry := console.NewRegistry(db)
	consoleRegistry.StartSweeper()
	vmHandler := handler.NewVMHandler(db, taskMgr, consoleRegistry)
	imageHandler := handler.NewImageHandler(db, taskMgr)
	taskHandler := handler.NewTaskHandler(db, taskMgr)
	sessionHandler := handler.NewSessionHandler(db, consoleRegistry)
	auditHandler := handler.NewAuditHandler(db)
	dashboardHandler := handler.NewDashboardHandler(db)
	storageHandler := handler.NewStorageHandler()
	networkHandler := handler.NewNetworkHandler()
	vncHandler := handler.NewVNCHandler(db, consoleRegistry)
	terminalHandler := handler.NewTerminalHandler(db, consoleRegistry)

	// 公开接口（无需认证）
	r.POST("/api/auth/login", authHandler.Login)

	// VNC token 解析（供 websockify JSONTokenApi 内网调用）
	r.GET("/api/vnc/token/:token", vncHandler.ResolveToken)

	// 需要认证的接口
	api := r.Group("/api")
	api.Use(middleware.AuthMiddleware(db))
	{
		// 认证相关
		api.GET("/auth/me", authHandler.GetMe)

		// 用户管理（仅管理员）
		users := api.Group("/users")
		users.Use(middleware.AdminMiddleware())
		{
			users.GET("", userHandler.ListUsers)
			users.POST("", userHandler.CreateUser)
			users.PUT("/:id", userHandler.UpdateUser)
			users.DELETE("/:id", userHandler.DeleteUser)
		}

		// 宿主机管理（仅管理员）
		hosts := api.Group("/hosts")
		hosts.Use(middleware.OperatorMiddleware())
		{
			hosts.GET("", hostHandler.ListHosts)
			hosts.POST("", hostHandler.CreateHost)
			hosts.PUT("/:id", hostHandler.UpdateHost)
			hosts.DELETE("/:id", hostHandler.DeleteHost)
			hosts.POST("/:id/test", hostHandler.TestHost)
			hosts.GET("/:id/stats", hostHandler.GetHostStats)
		}

		// 虚拟机管理（仅管理员）
		vms := api.Group("/vms")
		vms.Use(middleware.OperatorMiddleware())
		{
			vms.GET("", vmHandler.ListVMs)
			vms.GET("/options", vmHandler.GetVMOptions)
			vms.GET("/import/scan", vmHandler.ScanImportVMs)
			vms.POST("/import", vmHandler.ImportVMs)
			vms.POST("", vmHandler.CreateVM)
			vms.GET("/:id", vmHandler.GetVM)
			vms.GET("/:id/detail", vmHandler.GetVMDetail)
			vms.GET("/:id/spec", vmHandler.GetVMSpec)
			vms.PUT("/:id/spec", vmHandler.UpdateVMSpec)
			vms.POST("/:id/clone", vmHandler.CloneVM)
			vms.POST("/:id/pause", vmHandler.PauseVM)
			vms.POST("/:id/resume", vmHandler.ResumeVM)
			vms.GET("/:id/stats", vmHandler.GetVMStats)
			vms.PUT("/:id/cpu", vmHandler.SetVcpu)
			vms.PUT("/:id/memory", vmHandler.SetMemory)
			vms.PUT("/:id/autostart", vmHandler.SetAutostart)
			vms.PUT("/:id/boot", vmHandler.SetBoot)
			vms.POST("/:id/devices/disks", vmHandler.AttachDisk)
			vms.DELETE("/:id/devices/disks/:target", vmHandler.DetachDisk)
			vms.POST("/:id/devices/interfaces", vmHandler.AttachInterface)
			vms.DELETE("/:id/devices/interfaces/:mac", vmHandler.DetachInterface)
			vms.GET("/:id/xml", vmHandler.GetVMXML)
			vms.PUT("/:id/xml", vmHandler.UpdateVMXML)
			vms.POST("/:id/start", vmHandler.StartVM)
			vms.POST("/:id/stop", vmHandler.StopVM)
			vms.POST("/:id/restart", vmHandler.RestartVM)
			vms.DELETE("/:id", vmHandler.DeleteVM)
			vms.GET("/:id/snapshots", vmHandler.ListSnapshots)
			vms.POST("/:id/snapshots", vmHandler.CreateSnapshot)
			vms.DELETE("/:id/snapshots/:snap", vmHandler.DeleteSnapshot)
			vms.POST("/:id/snapshots/:snap/revert", vmHandler.RevertSnapshot)
			vms.POST("/:id/vnc-token", vncHandler.RequestToken)
			vms.GET("/:id/terminal", terminalHandler.Connect)
			vms.GET("/:id/serial", vmHandler.ConnectSerial)
		}

		// 存储池管理（admin）
		storage := api.Group("/storage")
		storage.Use(middleware.OperatorMiddleware())
		{
			storage.GET("/pools", storageHandler.ListPools)
			storage.GET("/pools/:name", storageHandler.GetPool)
			storage.POST("/pools", storageHandler.CreatePool)
			storage.DELETE("/pools/:name", storageHandler.DeletePool)
			storage.POST("/pools/:name/volumes", storageHandler.CreateVolume)
			storage.DELETE("/pools/:name/volumes/:vol", storageHandler.DeleteVolume)
		}

		// 网络管理（admin）
		networks := api.Group("/networks")
		networks.Use(middleware.OperatorMiddleware())
		{
			networks.GET("", networkHandler.ListNetworks)
			networks.GET("/:name", networkHandler.GetNetwork)
			networks.POST("", networkHandler.CreateNetwork)
			networks.POST("/xml", networkHandler.DefineNetworkXML)
			networks.PUT("/:name", networkHandler.UpdateNetwork)
			networks.POST("/:name/start", networkHandler.StartNetwork)
			networks.POST("/:name/stop", networkHandler.StopNetwork)
			networks.DELETE("/:name", networkHandler.DeleteNetwork)
		}

		// 镜像管理（仅管理员）
		images := api.Group("/images")
		images.Use(middleware.OperatorMiddleware())
		{
			images.GET("", imageHandler.ListImages)
			images.GET("/:id", imageHandler.GetImage)
			images.POST("/upload", imageHandler.UploadImage)
			images.POST("/:id/clone", imageHandler.CloneVM)
			images.PUT("/:id/template", imageHandler.SetImageTemplate)
			images.DELETE("/:id", imageHandler.DeleteImage)
		}

		// 宿主机信息（兼容旧接口）
		api.GET("/host", vmHandler.GetHostInfo)

		// 仪表盘（仅管理员）
		dashboard := api.Group("/dashboard")
		dashboard.Use(middleware.OperatorMiddleware())
		{
			dashboard.GET("/overview", dashboardHandler.Overview)
			dashboard.GET("/vm-status", dashboardHandler.VMStatusDistribution)
			dashboard.GET("/host-stats", dashboardHandler.HostStats)
			dashboard.GET("/vm-perf", dashboardHandler.VmPerf)
		}

		// 审计日志查询（仅管理员）
		audit := api.Group("/audit")
		audit.Use(middleware.AdminMiddleware())
		{
			audit.GET("", auditHandler.ListAuditLogs)
			audit.GET("/actions", auditHandler.ListAuditActions)
			audit.GET("/summary", auditHandler.AuditActionSummary)
			audit.GET("/:id", auditHandler.GetAuditLog)
		}

		// 异步任务查询（仅管理员）
		taskRoutes := api.Group("/tasks")
		taskRoutes.Use(middleware.OperatorMiddleware())
		{
			taskRoutes.GET("", taskHandler.ListTasks)
			taskRoutes.GET("/:id", taskHandler.GetTask)
			taskRoutes.DELETE("/:id", taskHandler.DeleteTask)
		}

		// 控制台会话（仅管理员）：谁连了哪台 VM、强制断开
		sessRoutes := api.Group("/sessions")
		sessRoutes.Use(middleware.OperatorMiddleware())
		{
			sessRoutes.GET("", sessionHandler.ListSessions)
			sessRoutes.POST("/:id/disconnect", sessionHandler.DisconnectSession)
		}
	}

	// 健康检查
	r.GET("/api/health", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"status":  "ok",
			"version": "1.0.0",
		})
	})

	// 初始化种子数据
	initSeedData(database.GetDB())

	// 启动服务器
	addr := fmt.Sprintf(":%s", config.GlobalConfig.ServerPort)
	log.Printf("🚀 vmops 启动成功 → http://localhost%s", addr)
	if err := r.Run(addr); err != nil {
		log.Fatalf("启动服务器失败: %v", err)
	}
}

// sweepStaleRecords 启动收敛：进程重启导致内存态丢失，DB 残留的中间态需置终态。
//   - tasks: pending/running 的任务队列已丢，不可重入（executor 非幂等，重跑会重复建盘），标记 failed 并写明原因
//   - console_sessions: ssh/serial 的 WS 随进程死亡，标记 closed；VNC 靠 last_seen 过期清扫，不动
func sweepStaleRecords(db *gorm.DB) {
	now := time.Now()
	r1 := db.Model(&model.Task{}).
		Where("status IN ?", []string{"pending", "running"}).
		Updates(map[string]interface{}{"status": "failed", "error": "服务重启，未完成任务已终止，请重新提交"})
	if r1.Error == nil && r1.RowsAffected > 0 {
		log.Printf("🧹 收敛残留任务 %d 个", r1.RowsAffected)
	}
	r2 := db.Model(&model.ConsoleSession{}).
		Where("status = ? AND type IN ?", "active", []string{"ssh", "serial"}).
		Updates(map[string]interface{}{"status": "closed", "ended_at": now})
	if r2.Error == nil && r2.RowsAffected > 0 {
		log.Printf("🧹 收敛残留控制台会话 %d 个", r2.RowsAffected)
	}
}

// initSeedData 初始化种子数据
func initSeedData(db *gorm.DB) {
	// 检查是否有用户
	var count int64
	db.Model(&model.User{}).Count(&count)
	if count > 0 {
		return
	}

	log.Println("初始化种子数据...")

	// 生成密码哈希
	adminHash, _ := middleware.HashPassword("password")
	userHash, _ := middleware.HashPassword("123456")

	// 创建管理员
	admin := &model.User{
		Username:     "admin",
		PasswordHash: adminHash,
		Role:         "admin",
		RealName:     "管理员",
		IsActive:     true,
	}

	// 创建普通用户
	user := &model.User{
		Username:     "user",
		PasswordHash: userHash,
		Role:         "viewer",
		RealName:     "普通用户",
		IsActive:     true,
	}

	db.Create(admin)
	db.Create(user)

	log.Println("✅ 种子数据初始化完成")
	log.Println("   👑 管理员: admin / password")
	log.Println("   👤 用户:   user / 123456")
}
