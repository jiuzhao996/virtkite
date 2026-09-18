package handler

// routes.go v3 批次 0：/api 路由注册按域收口（main.go 只保留顶层静态托管、health 与 Deps 组装）。
//
// 结构：
//   - Deps：handler 层统一依赖（新增 handler 一律从 Deps 取依赖；既有构造器已全部收口至此）
//   - RegisterPublic：公开路由（login / vnc token 解析 / 告警 webhook / metrics），无需认证
//   - RegisterAll：/api 认证组的全部业务路由
//
// 纪律：本文件只做「构造 + 注册」，业务逻辑一律在各 handler 文件；新模块路由追加到对应域函数尾部。

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/middleware"
	"github.com/jiuzhao/vmops/service/console"
	"github.com/jiuzhao/vmops/service/cron"
	"github.com/jiuzhao/vmops/service/setting"
	"github.com/jiuzhao/vmops/service/tasks"
	"github.com/jiuzhao/vmops/service/virt"
	"gorm.io/gorm"
)

// Deps handler 层统一依赖（批次 0：新增 handler 一律从 Deps 取依赖）。
type Deps struct {
	DB                *gorm.DB
	Virt              *virt.Virt
	Tasks             *tasks.Manager
	Sessions          *console.Registry
	SettingMgr        *setting.Manager
	AlertmanagerURL   string
	PrometheusURL     string
	AlertWebhookToken string
	MetricsToken      string
	LokiURL           string
}

// RegisterPublic 公开路由（无需认证，注册在引擎根上）。
func RegisterPublic(r *gin.Engine, deps Deps) {
	authHandler := NewAuthHandler(deps.DB)
	vncHandler := NewVNCHandler(deps.DB, deps.Sessions)
	alertWebhookHandler := NewAlertWebhookHandler(deps.DB, deps.AlertWebhookToken)
	metricsHandler := NewMetricsHandler(deps.DB)

	// 公开接口（无需认证）
	r.POST("/api/auth/login", authHandler.Login)

	// VNC token 解析（供 websockify JSONTokenApi 内网调用）
	r.GET("/api/vnc/token/:token", vncHandler.ResolveToken)

	// Alertmanager 告警网关（webhook 推送 → 去重入库告警历史）。
	// 配置 ALERT_WEBHOOK_TOKEN 后要求 ?token= 或 Bearer 匹配，需与 deploy/alertmanager.yml 同步。
	r.POST("/api/monitor/webhook", alertWebhookHandler.Handle)

	// Prometheus 指标（供 Prometheus scrape）。设置 METRICS_TOKEN 后要求 Bearer 认证
	// （Prometheus 抓取任务配 bearer_token；websockify 无关此路径），未设置则保持公开。
	if deps.MetricsToken != "" {
		metricsToken := deps.MetricsToken
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
}

// RegisterAll /api 认证组的全部业务路由（按域组织）。
func RegisterAll(api *gin.RouterGroup, deps Deps) {
	// handler 构造（批次 0：统一从 Deps 取依赖；auth/metrics/webhook 等公开路由 handler 在 RegisterPublic 内构造）
	authHandler := NewAuthHandler(deps.DB)
	userHandler := NewUserHandler(deps.DB)
	hostHandler := NewHostHandler(deps.DB)
	imageHandler := NewImageHandler(deps.DB, deps.Tasks)
	taskHandler := NewTaskHandler(deps.DB, deps.Tasks)
	sessionHandler := NewSessionHandler(deps.DB, deps.Sessions)
	settingsHandler := NewSettingsHandler(deps.DB, deps.SettingMgr)
	auditHandler := NewAuditHandler(deps.DB)
	dashboardHandler := NewDashboardHandler(deps.DB)
	storageHandler := NewStorageHandler(deps.DB, deps.Tasks)
	networkHandler := NewNetworkHandler()
	vncHandler := NewVNCHandler(deps.DB, deps.Sessions)
	terminalHandler := NewTerminalHandler(deps.DB, deps.Sessions)
	monitorHandler := NewMonitorHandler(deps.DB, deps.AlertmanagerURL)
	historyHandler := NewHistoryHandler(deps.DB, deps.PrometheusURL)
	dockerHandler := NewDockerHandler()
	appsHandler := NewAppsHandler(deps.DB, deps.Tasks)
	vmFilesHandler := NewVMFilesHandler(deps.DB)
	vmHandler := NewVMHandler(deps.DB, deps.Tasks, deps.Sessions)
	aiHandler := NewAIHandler(deps.DB, deps.SettingMgr)
	vmCredHandler := NewVMCredentialHandler(deps.DB, config.GlobalConfig.JWTSecretKey)
	lokiHandler := NewLokiHandler(deps.LokiURL)
	appStoreV2 := NewAppStoreV2Handler()
	cronScheduler := &cron.Scheduler{DB: deps.DB, Virt: deps.Virt, BackupDir: ""}
	cronScheduler.Start() // 内部自起 goroutine（整分 tick）
	cronsHandler := NewCronsHandler(deps.DB, cronScheduler)

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
		vms.POST("/:id/devices/disks", vmHandler.AttachDisk)
		vms.POST("/:id/devices/disks/quick", vmHandler.QuickAttachDisk)
		vms.DELETE("/:id/devices/disks/:target", vmHandler.DetachDisk)
		vms.POST("/:id/devices/interfaces", vmHandler.AttachInterface)
		vms.DELETE("/:id/devices/interfaces/:mac", vmHandler.DetachInterface)
		vms.POST("/:id/devices/standard", vmHandler.EnsureStandardDevices)
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
		// 资产授权（借鉴堡垒机 4A）：admin 分配/查看/收回；handler 内 requireAdminRole 二次收口
		vms.GET("/:id/grants", vmHandler.ListVMGrants)
		vms.POST("/:id/grants", vmHandler.GrantVM)
		vms.DELETE("/:id/grants/:gid", vmHandler.RevokeVMGrant)
		// VM 文件管理（v2 批次 2：SSH 在线通道，凭据请求期内存透传不落盘）
		vms.POST("/:id/files/list", vmFilesHandler.List)
		vms.POST("/:id/files/download", vmFilesHandler.Download)
		vms.POST("/:id/files/upload", vmFilesHandler.Upload)
		vms.POST("/:id/files/delete", vmFilesHandler.Delete)
		vms.POST("/:id/files/mkdir", vmFilesHandler.Mkdir)
		vms.GET("/:id/files/offline-capability", vmFilesHandler.OfflineCapability)
		// 离线挂载通道（guestmount 只读挂关机 VM 系统盘，不依赖 VM 内 SSH）
		vms.POST("/:id/files/offline/mount", vmFilesHandler.OfflineMount)
		vms.POST("/:id/files/offline/list", vmFilesHandler.OfflineList)
		vms.GET("/:id/files/offline/download", vmFilesHandler.OfflineDownload)
		vms.POST("/:id/files/offline/unmount", vmFilesHandler.OfflineUnmount)
		// 应用商店安装（v2 批次 3：挂 vms 前缀让 operator 放行——往自己 VM 装软件属操作语义）
		vms.POST("/:id/credentials", vmCredHandler.Save)
		vms.GET("/:id/credentials", vmCredHandler.Get)
		vms.DELETE("/:id/credentials", vmCredHandler.Delete)
		vms.POST("/apps/install", appsHandler.Install)
		vms.POST("/:id/vnc-token", vncHandler.RequestToken)
		vms.GET("/:id/terminal", terminalHandler.Connect)
		vms.GET("/:id/serial", vmHandler.ConnectSerial)
	}

	// 存储池管理（admin）
	storage := api.Group("/storage")
	storage.Use(middleware.OperatorMiddleware())
	{
		storage.GET("/pools", storageHandler.ListPools)
		storage.PUT("/pools/:name/meta", storageHandler.UpdatePoolMeta)
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
		images.POST("/upload", imageHandler.UploadImage)
		images.POST("/register", imageHandler.RegisterImage)
		images.POST("/:id/clone", imageHandler.CloneVM)
		images.PUT("/:id/template", imageHandler.SetImageTemplate)
		images.DELETE("/:id", imageHandler.DeleteImage)
	}

	// Docker 容器/镜像管理（v2 大升级批次 1：admin/operator 可用，viewer 403——
	// 容器清单含内网端口映射与镜像列表，与监控中心同口径走 NonViewerMiddleware）
	docker := api.Group("/docker")
	docker.Use(middleware.NonViewerMiddleware())
	{
		docker.GET("/containers", dockerHandler.ListContainers)
		docker.POST("/containers/:id/:action", dockerHandler.ContainerAction)
		docker.DELETE("/containers/:id", dockerHandler.RemoveContainer)
		docker.GET("/containers/:id/logs", dockerHandler.ContainerLogs)
		docker.GET("/images", dockerHandler.ListImages)
		docker.DELETE("/images/:id", dockerHandler.RemoveImage)
	}

	// AI 运维助手（viewer 403：消耗 API 配额且注入平台上下文属资产信息）
	ai := api.Group("/ai")
	ai.Use(middleware.NonViewerMiddleware())
	{
		ai.GET("/status", aiHandler.Status)
		ai.POST("/chat", aiHandler.Chat)
	}

	// 应用商店目录（登录可浏览；安装走 /api/vms/apps/install——挂在 vms 组让 operator 放行）
	apps := api.Group("/apps")
	apps.Use(middleware.OperatorMiddleware())
	{
		apps.GET("", appsHandler.List)
		apps.GET("/:id", appsHandler.Get)
	}

	// 应用商店 v2（声明式 compose 应用包，1Panel 对标）
	v2 := api.Group("/appstore")
	v2.Use(middleware.NonViewerMiddleware())
	{
		v2.GET("", appStoreV2.ListV2)
		v2.GET("/status", appStoreV2.StatusV2)
		v2.GET("/:key", appStoreV2.GetV2)
		v2.POST("/:key/install", appStoreV2.InstallV2)
		v2.POST("/:key/uninstall", appStoreV2.UninstallV2)
	}

	// 计划任务（平台管理语义，仅管理员）
	crons := api.Group("/crons")
	crons.Use(middleware.AdminMiddleware())
	{
		crons.GET("", cronsHandler.List)
		crons.POST("", cronsHandler.Create)
		crons.PUT("/:id", cronsHandler.Update)
		crons.DELETE("/:id", cronsHandler.Delete)
		crons.POST("/:id/toggle", cronsHandler.Toggle)
		crons.POST("/:id/run", cronsHandler.RunNow)
		crons.GET("/:id/runs", cronsHandler.ListRuns)
	}

	// 监控中心（操作员/管理员可见：告警与 file-sd 携带资产名单，viewer 403——全量审计 P0 收权）
	monitor := api.Group("/monitor")
	monitor.Use(middleware.NonViewerMiddleware())
	{
		monitor.GET("/alerts", monitorHandler.ListAlerts)
		// 告警历史（webhook 入库数据的追溯查询，与实时列表互补）
		monitor.GET("/alerts/history", monitorHandler.AlertHistory)
		// file_sd 抓取目标预览（与后台落盘文件同源，调试/前端展示用）
		monitor.GET("/file-sd", monitorHandler.PreviewFileSD)
		monitor.GET("/grafana-status", monitorHandler.GrafanaStatus)
		// Loki 日志查询（v3 批次 G：指标+日志+告警完整可观测性）
		monitor.GET("/loki/query", lokiHandler.Query)
		monitor.GET("/loki/labels", lokiHandler.Labels)
	}

	// 仪表盘（仅管理员）
	dashboard := api.Group("/dashboard")
	dashboard.Use(middleware.OperatorMiddleware())
	{
		dashboard.GET("/overview", dashboardHandler.Overview)
		dashboard.GET("/capacity", dashboardHandler.Capacity)
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
