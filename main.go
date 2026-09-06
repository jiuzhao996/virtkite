package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/database"
	"github.com/jiuzhao/vmops/handler"
	"github.com/jiuzhao/vmops/middleware"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/console"
	monitor "github.com/jiuzhao/vmops/service/monitor"
	"github.com/jiuzhao/vmops/service/setting"
	"github.com/jiuzhao/vmops/service/tasks"
	"github.com/jiuzhao/vmops/service/vnc"
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

	// release 模式下必须显式配置 JWT_SECRET_KEY。
	// 注意：config 包给该项兜了硬编码默认值，所以只判 `== ""` 永不成立（校验形同虚设），
	// 必须同时拒绝「仍是内置默认值」的情况，否则线上密钥公开可见、Token 可被任意伪造。
	const builtinJWTSecret = "vmops-jwt-secret-key-change-in-production"
	if config.GlobalConfig.ServerMode == "release" &&
		(config.GlobalConfig.JWTSecretKey == "" || config.GlobalConfig.JWTSecretKey == builtinJWTSecret) {
		log.Fatalf("release 模式必须设置 JWT_SECRET_KEY 环境变量：当前为空或仍是内置默认值，存在 Token 伪造风险")
	}

	// release 模式禁止 CORS 通配符：* 等于允许任意站点携带凭据跨域调用全部 API。
	// 与 JWT 校验同款策略：显式配置具体来源后才允许启动。
	if config.GlobalConfig.ServerMode == "release" && config.GlobalConfig.CORSOrigins == "*" {
		log.Fatalf("release 模式必须设置 CORS_ORIGINS 环境变量（如 https://vmops.example.com）：* 通配符存在跨站调用 API 的风险")
	}
	if config.GlobalConfig.ServerMode == "release" {
		if config.GlobalConfig.MetricsToken == "" {
			log.Println("⚠️ /metrics 为公开端点（Prometheus 抓取用）：建议设置 METRICS_TOKEN 开启 Bearer 认证，或用防火墙限制 :8080 的来源网段")
		}
	}

	// 初始化数据库
	database.Init()
	db := database.GetDB()

	// 系统可写配置（DB 持久化）：接线到各消费方，运行时修改即时生效
	settingMgr := setting.NewManager(db)
	tasks.DefaultStoragePoolResolver = settingMgr.DefaultStoragePool
	vnc.TTLResolver = settingMgr.VNCTokenTTL
	console.StaleAfterResolver = settingMgr.VNCStale

	// 启动收敛：内存队列/连接随进程消失，DB 里残留的 pending/running 任务与 ssh/serial 会话
	// 置终态，避免重启后幽灵任务与幽灵会话（VNC 靠 last_seen 过期清扫收敛，无需处理）
	sweepStaleRecords(db)
	startAuditRetention(db)

	// Prometheus file_sd 目标文件写入（服务发现闭环：平台建 VM → VM 的 node_exporter
	// 自动进抓取目标）。FILE_SD_PATH 未配置时内部直接不启动。
	monitor.StartFileSDWriter(db, config.GlobalConfig.FileSDPath, time.Minute)

	// 初始化Gin
	if config.GlobalConfig.ServerMode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.Default()

	// 镜像上传走 multipart：超过该阈值的部分落磁盘临时文件，不再全量驻留内存，
	// 避免多 GB 的 qcow2/ISO 把进程内存打满（显式声明 32MB 意图，勿改大）
	r.MaxMultipartMemory = 32 << 20

	// 注册中间件
	r.Use(middleware.CORSMiddleware())
	r.Use(middleware.AuditMiddleware(db))

	// 静态文件服务：按候选目录依次探测前端产物，第一个存在 index.html 的胜出。
	// 用 os.ReadFile + c.Data 返回 index.html，规避 gin 对含 .html 路径的 301 目录索引重定向怪癖。
	webDir, indexBytes := locateWebRoot()

	if indexBytes != nil {
		serveIndex := func(c *gin.Context) {
			c.Data(http.StatusOK, "text/html; charset=utf-8", indexBytes)
		}
		r.GET("/", serveIndex)
		r.NoRoute(serveIndex)

		// 托管前端静态资源（js/css 等），避免被 NoRoute 兜底为 index.html。
		// 目录必须取自上面探测命中的 webDir，不能硬编码相对路径（工作目录不确定）。
		assetsDir := filepath.Join(webDir, "assets")
		if info, statErr := os.Stat(assetsDir); statErr == nil && info.IsDir() {
			r.Static("/assets", assetsDir)
		}
	} else {
		// 开发场景：后端 `go run main.go` + 前端 `npm run dev`（Vite 把 /api 代理到 8080），
		// 此时没有构建产物是正常的，只提供 API 即可，绝不能因此退出进程。
		log.Printf("⚠️ 未找到前端构建产物（已探测 web/dist 与 static 候选目录），本次仅提供 API 服务；需要页面请先执行 `cd web && npm run build` 再重启后端")

		// 产物缺失时给出中文纯文本提示，避免空 body / 404 让人误判后端已挂
		hint := "vmops 后端已启动，但未找到前端构建产物（web/dist/index.html）。\n" +
			"当前仅提供 API 服务。\n" +
			"构建前端：cd web && npm run build，然后重启后端。\n" +
			"开发模式：cd web && npm run dev，直接访问 Vite 开发服务器（其 /api 已代理到本服务）。\n"
		serveHint := func(c *gin.Context) {
			c.String(http.StatusServiceUnavailable, hint)
		}
		r.GET("/", serveHint)
		r.NoRoute(serveHint)
		// 产物缺失时跳过 /assets 注册（无目录可托管）
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
	settingsHandler := handler.NewSettingsHandler(db, settingMgr)
	auditHandler := handler.NewAuditHandler(db)
	dashboardHandler := handler.NewDashboardHandler(db)
	storageHandler := handler.NewStorageHandler(db, taskMgr)
	networkHandler := handler.NewNetworkHandler()
	vncHandler := handler.NewVNCHandler(db, consoleRegistry)
	terminalHandler := handler.NewTerminalHandler(db, consoleRegistry)
	metricsHandler := handler.NewMetricsHandler(db)
	monitorHandler := handler.NewMonitorHandler(db, config.GlobalConfig.AlertmanagerURL)
	alertWebhookHandler := handler.NewAlertWebhookHandler(db, config.GlobalConfig.AlertWebhookToken)
	historyHandler := handler.NewHistoryHandler(db, config.GlobalConfig.PrometheusURL)

	// 公开接口（无需认证）
	r.POST("/api/auth/login", authHandler.Login)

	// VNC token 解析（供 websockify JSONTokenApi 内网调用）
	r.GET("/api/vnc/token/:token", vncHandler.ResolveToken)

	// Alertmanager 告警网关（webhook 推送 → 去重入库告警历史）。
	// 配置 ALERT_WEBHOOK_TOKEN 后要求 ?token= 或 Bearer 匹配，需与 deploy/alertmanager.yml 同步。
	r.POST("/api/monitor/webhook", alertWebhookHandler.Handle)

	// Prometheus 指标（供 Prometheus scrape）。设置 METRICS_TOKEN 后要求 Bearer 认证
	// （Prometheus 抓取任务配 bearer_token；websockify 无关此路径），未设置则保持公开。
	if config.GlobalConfig.MetricsToken != "" {
		metricsToken := config.GlobalConfig.MetricsToken
		r.GET("/metrics", func(c *gin.Context) {
			if c.GetHeader("Authorization") != "Bearer "+metricsToken && c.Query("token") != metricsToken {
				c.AbortWithStatus(http.StatusUnauthorized)
				return
			}
			metricsHandler.Handler(c)
		})
	} else {
		r.GET("/metrics", metricsHandler.Handler)
	}

	// 需要认证的接口
	api := r.Group("/api")
	api.Use(middleware.AuthMiddleware(db))
	{
		// 认证相关
		api.GET("/auth/me", authHandler.GetMe)
		// 当前用户改密码（所有角色，需校验旧密码）
		api.PUT("/users/me/password", userHandler.ChangeMyPassword)

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
			vms.GET("/:id/spec", vmHandler.GetVMSpec)
			vms.PUT("/:id/spec", vmHandler.UpdateVMSpec)
			vms.POST("/:id/clone", vmHandler.CloneVM)
			vms.POST("/:id/pause", vmHandler.PauseVM)
			vms.POST("/:id/resume", vmHandler.ResumeVM)
			vms.GET("/:id/stats", vmHandler.GetVMStats)
			// 虚拟机历史曲线（Prometheus query_range，进详情页即画满）
			vms.GET("/:id/stats-history", historyHandler.VMStatsHistory)
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
			storage.GET("/pools/:name/volume-refs", storageHandler.GetVolumeRefs)
			storage.POST("/pools/:name/orphan-cleanup", storageHandler.CleanupOrphans)
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
			networks.PUT("/:name/autostart", networkHandler.SetNetworkAutostart)
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

		// 监控中心（登录即可看：告警列表 + Grafana 看板入口，与 dashboard 同级）
		monitor := api.Group("/monitor")
		{
			monitor.GET("/alerts", monitorHandler.ListAlerts)
			// 告警历史（webhook 入库数据的追溯查询，与实时列表互补）
			monitor.GET("/alerts/history", monitorHandler.AlertHistory)
			// file_sd 抓取目标预览（与后台落盘文件同源，调试/前端展示用）
			monitor.GET("/file-sd", monitorHandler.PreviewFileSD)
		}

		// 仪表盘（仅管理员）
		dashboard := api.Group("/dashboard")
		dashboard.Use(middleware.OperatorMiddleware())
		{
			dashboard.GET("/overview", dashboardHandler.Overview)
			dashboard.GET("/vm-status", dashboardHandler.VMStatusDistribution)
			dashboard.GET("/host-stats", dashboardHandler.HostStats)
			dashboard.GET("/vm-perf", dashboardHandler.VmPerf)
			// 宿主机历史曲线（Prometheus query_range，进页面即画满）
			dashboard.GET("/host-history", historyHandler.HostHistory)
			// 全部虚拟机历史曲线（虚拟机列表页迷你图预填）
			dashboard.GET("/vm-history", historyHandler.VMsHistory)
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

		// 系统设置快照（仅管理员）
		settings := api.Group("/settings")
		settings.Use(middleware.AdminMiddleware())
		{
			settings.GET("", settingsHandler.GetSettings)
			settings.PUT("", settingsHandler.UpdateSettings)
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
//
// startAuditRetention 审计日志保留期清理：启动跑一轮 + 每 24h 一轮，
// 删除 30 天前的记录（分批 DELETE 防止单语句锁表太久）。
// 背景：审计表曾因 GET 轮询全量记录在数小时内膨胀到 6 万条；GET 已不再入审计，
// 此任务兜底长期运行的存量增长。后台 goroutine 自带 recover（见 AGENTS 并发规范）。
func startAuditRetention(db *gorm.DB) {
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("[audit] 保留期清理 panic=%v\n%s", rec, debug.Stack())
			}
		}()

		cleanup := func() {
			cutoff := time.Now().AddDate(0, 0, -30)
			for {
				res := db.Exec("DELETE FROM audit_logs WHERE created_at < ? LIMIT 10000", cutoff)
				if res.Error != nil {
					log.Printf("[audit] 保留期清理失败: %v", res.Error)
					return
				}
				if res.RowsAffected < 10000 {
					return
				}
				time.Sleep(200 * time.Millisecond)
			}
		}

		cleanup()
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			cleanup()
		}
	}()
}

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

// locateWebRoot 按候选目录依次探测前端产物，返回命中的目录与 index.html 内容。
// 候选顺序：<exeDir>/web/dist → <exeDir>/static → <cwd>/web/dist → <cwd>/static。
// 追加 cwd 候选是因为 `go run main.go` 时二进制在 /tmp/go-build*/ 临时目录，
// exeDir 下必然找不到产物（README/AGENTS.md 都把 go run 写成标准运行方式）。
// 全部未命中返回 ("", nil)，交由调用方降级为「仅 API」模式，不视为致命错误。
func locateWebRoot() (dir string, index []byte) {
	var candidates []string
	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		candidates = append(candidates, filepath.Join(exeDir, "web", "dist"), filepath.Join(exeDir, "static"))
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, "web", "dist"), filepath.Join(cwd, "static"))
	}

	for _, candidate := range candidates {
		data, err := os.ReadFile(filepath.Join(candidate, "index.html"))
		if err != nil {
			continue
		}
		// 打印命中目录：改前端后没生效多半是命中了另一份产物（见 AGENTS.md 踩坑记录）
		log.Printf("前端产物目录: %s", candidate)
		return candidate, data
	}
	return "", nil
}

// initSeedData 初始化种子数据
func initSeedData(db *gorm.DB) {
	// 检查是否有用户
	var count int64
	if err := db.Model(&model.User{}).Count(&count).Error; err != nil {
		// 查不到就无法判断是否需要种子，此时建账号可能撞唯一索引，直接放弃并留日志
		log.Printf("统计用户数失败，跳过种子数据初始化: %v", err)
		return
	}
	if count > 0 {
		return
	}

	log.Println("初始化种子数据...")

	// 生成密码哈希：失败必须中止，否则会写入空哈希造成账号静默不可登录
	adminHash, err := middleware.HashPassword("password")
	if err != nil {
		log.Printf("生成管理员密码哈希失败，跳过种子数据初始化: %v", err)
		return
	}
	userHash, err := middleware.HashPassword("123456")
	if err != nil {
		log.Printf("生成普通用户密码哈希失败，跳过种子数据初始化: %v", err)
		return
	}

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

	// 种子账号写入失败必须可见，否则首次启动后无法登录却毫无线索
	if err := db.Create(admin).Error; err != nil {
		log.Printf("创建种子管理员账号失败: %v", err)
	}
	if err := db.Create(user).Error; err != nil {
		log.Printf("创建种子普通用户失败: %v", err)
	}

	// 只打用户名与角色，绝不把明文口令写进日志（日志常被收集/共享）
	log.Println("✅ 种子数据初始化完成")
	log.Println("   👑 管理员: admin（角色 admin）")
	log.Println("   👤 用户:   user（角色 viewer）")
	log.Println("   🔑 默认口令见 README，首次登录后请立即修改")
}
