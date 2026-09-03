package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/database"
	"github.com/jiuzhao/vmops/handler"
	"github.com/jiuzhao/vmops/middleware"
	"github.com/jiuzhao/vmops/model"
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
	vmHandler := handler.NewVMHandler(db)
	imageHandler := handler.NewImageHandler(db)
	auditHandler := handler.NewAuditHandler(db)
	dashboardHandler := handler.NewDashboardHandler(db)
	storageHandler := handler.NewStorageHandler()
	networkHandler := handler.NewNetworkHandler()

	// 公开接口（无需认证）
	r.POST("/api/auth/login", authHandler.Login)

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
		hosts.Use(middleware.AdminMiddleware())
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
		vms.Use(middleware.AdminMiddleware())
		{
			vms.GET("", vmHandler.ListVMs)
			vms.GET("/:id", vmHandler.GetVM)
			vms.GET("/:id/xml", vmHandler.GetVMXML)
			vms.PUT("/:id/xml", vmHandler.UpdateVMXML)
			vms.POST("", vmHandler.CreateVM)
			vms.POST("/:id/start", vmHandler.StartVM)
			vms.POST("/:id/stop", vmHandler.StopVM)
			vms.POST("/:id/restart", vmHandler.RestartVM)
			vms.DELETE("/:id", vmHandler.DeleteVM)
			vms.GET("/:id/snapshots", vmHandler.ListSnapshots)
			vms.POST("/:id/snapshots", vmHandler.CreateSnapshot)
			vms.DELETE("/:id/snapshots/:snap", vmHandler.DeleteSnapshot)
			vms.POST("/:id/snapshots/:snap/revert", vmHandler.RevertSnapshot)
		}

		// 存储池管理（admin）
		storage := api.Group("/storage")
		storage.Use(middleware.AdminMiddleware())
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
		networks.Use(middleware.AdminMiddleware())
		{
			networks.GET("", networkHandler.ListNetworks)
			networks.GET("/:name", networkHandler.GetNetwork)
			networks.POST("", networkHandler.CreateNetwork)
			networks.POST("/xml", networkHandler.DefineNetworkXML)
			networks.POST("/:name/start", networkHandler.StartNetwork)
			networks.POST("/:name/stop", networkHandler.StopNetwork)
			networks.DELETE("/:name", networkHandler.DeleteNetwork)
		}

		// 镜像管理（仅管理员）
		images := api.Group("/images")
		images.Use(middleware.AdminMiddleware())
		{
			images.GET("", imageHandler.ListImages)
			images.GET("/:id", imageHandler.GetImage)
			images.POST("/upload", imageHandler.UploadImage)
			images.DELETE("/:id", imageHandler.DeleteImage)
		}

		// 宿主机信息（兼容旧接口）
		api.GET("/host", vmHandler.GetHostInfo)

		// 仪表盘（仅管理员）
		dashboard := api.Group("/dashboard")
		dashboard.Use(middleware.AdminMiddleware())
		{
			dashboard.GET("/overview", dashboardHandler.Overview)
			dashboard.GET("/vm-status", dashboardHandler.VMStatusDistribution)
		}

		// 审计日志查询（仅管理员）
		audit := api.Group("/audit")
		audit.Use(middleware.AdminMiddleware())
		{
			audit.GET("", auditHandler.ListAuditLogs)
			audit.GET("/:id", auditHandler.GetAuditLog)
			audit.GET("/summary", auditHandler.AuditActionSummary)
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
