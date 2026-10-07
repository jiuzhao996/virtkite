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
	"github.com/jiuzhao/vmops/middleware"
	"github.com/jiuzhao/vmops/service/console"
	"github.com/jiuzhao/vmops/service/cron"
	"github.com/jiuzhao/vmops/service/setting"
	"github.com/jiuzhao/vmops/service/tasks"
	"github.com/jiuzhao/vmops/service/virt"
	"github.com/jiuzhao/vmops/service/vnc"
	"gorm.io/gorm"
)

// Deps handler 层统一依赖（批次 0：新增 handler 一律从 Deps 取依赖）。
type Deps struct {
	DB         *gorm.DB
	Virt       *virt.Virt
	Tasks      *tasks.Manager
	Sessions   *console.Registry
	SettingMgr *setting.Manager
	// VNCTokens 全局唯一 VNC 令牌库：签发与解析必须共用同一实例，由 main 装配一次。
	VNCTokens         *vnc.TokenStore
	AlertmanagerURL   string
	PrometheusURL     string
	AlertWebhookToken string
	MetricsToken      string
}

// RegisterPublic 公开路由（无需认证，注册在引擎根上）。
func RegisterPublic(r *gin.Engine, deps Deps) {
	authHandler := NewAuthHandler(deps.DB)
	authHandler.SetSettingMgr(deps.SettingMgr) // 安全入口动态校验 + 密码策略依赖
	// 与 RegisterAll 内签发 handler 共用 deps.VNCTokens，否则解析恒失败
	vncHandler := NewVNCHandler(deps.DB, deps.Sessions, deps.VNCTokens)
	alertWebhookHandler := NewAlertWebhookHandler(deps.DB, deps.AlertWebhookToken)
	metricsHandler := NewMetricsHandler(deps.DB)

	// 公开接口（无需认证）。安全入口（批次 D）：设置后 login 必须携带 X-Entrance 头/参数，否则 404 伪装
	login := r.Group("/api/auth", middleware.EntranceMiddleware(deps.SettingMgr.SecurityEntrance()))
	login.POST("/login", authHandler.Login)

	// 系统公告公开读取（v3 批次 P：登录页/仪表盘展示；写入走 PUT /api/settings admin）
	announcementHandler := NewAnnouncementHandler(deps.DB, deps.SettingMgr)
	r.GET("/api/announcement", announcementHandler.Get)

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
	authHandler.SetSettingMgr(deps.SettingMgr)
	userHandler := NewUserHandler(deps.DB)
	grantReqHandler := &GrantRequestHandler{DB: deps.DB}
	userHandler.SetSettingMgr(deps.SettingMgr) // 改密/建用户密码策略
	vmExportHandler := NewVMExportHandler(deps.DB, deps.Virt)
	recycleHandler := NewVMRecycleHandler(deps.DB, deps.Virt)
	imageMarketHandler := NewImageMarketHandler(deps.DB, deps.Tasks)
	containerTerminalHandler := NewContainerTerminalHandler(deps.Sessions)
	hostHandler := NewHostHandler(deps.DB)
	imageHandler := NewImageHandler(deps.DB, deps.Tasks)
	taskHandler := NewTaskHandler(deps.DB, deps.Tasks)
	sessionHandler := NewSessionHandler(deps.DB, deps.Sessions)
	settingsHandler := NewSettingsHandler(deps.DB, deps.SettingMgr)
	auditHandler := NewAuditHandler(deps.DB)
	dashboardHandler := NewDashboardHandler(deps.DB)
	storageHandler := NewStorageHandler(deps.DB, deps.Tasks)
	networkHandler := NewNetworkHandler()
	networkFlowHandler := NewNetworkFlowHandler()
	networkTopologyHandler := NewNetworkTopologyHandler()
	networkIPAMHandler := NewNetworkIPAMHandler()
	// 与 RegisterPublic 内解析 handler 共用 deps.VNCTokens（同一实例是 VNC 链路存活的前提）
	vncHandler := NewVNCHandler(deps.DB, deps.Sessions, deps.VNCTokens)
	terminalHandler := NewTerminalHandler(deps.DB, deps.Sessions)
	monitorHandler := NewMonitorHandler(deps.DB, deps.AlertmanagerURL)
	historyHandler := NewHistoryHandler(deps.DB, deps.PrometheusURL)
	dockerHandler := NewDockerHandler()
	containerLogsHandler := NewContainerLogsHandler()
	appsHandler := NewAppsHandler(deps.DB, deps.Tasks)
	vmFilesHandler := NewVMFilesHandler(deps.DB)
	// 离线挂载守卫：启动对账清理上次进程遗留的 FUSE 挂载点，并挂上 SIGINT/SIGTERM 退出钩子
	StartOfflineMountGuard()
	vmHandler := NewVMHandler(deps.DB, deps.Tasks, deps.Sessions)
	aiHandler := NewAIHandler(deps.DB, deps.SettingMgr)
	// 凭据主密钥走独立配置项（回落与告警见 CredentialMasterSecret），不再直接喂 JWT 密钥
	vmCredHandler := NewVMCredentialHandler(deps.DB, CredentialMasterSecret())
	vmFilesHandler.SetVMCredentialHandler(vmCredHandler)
	// 应用商店安装：use_saved（或未带口令）走服务端凭据，明文不落 task.payload
	appsHandler.SetVMCredentialHandler(vmCredHandler)
	cronScheduler := &cron.Scheduler{DB: deps.DB, Virt: deps.Virt, BackupDir: "", TaskMgr: deps.Tasks}
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

	// 授权申请：我的申请（登录即可看）
	api.GET("/grant-requests/mine", grantReqHandler.ListMine)
	// 授权审批（仅管理员）
	grantsAdmin := api.Group("/grant-requests")
	grantsAdmin.Use(middleware.AdminMiddleware())
	{
		grantsAdmin.GET("", grantReqHandler.ListRequests)
		grantsAdmin.POST("/:id/approve", grantReqHandler.Approve)
		grantsAdmin.POST("/:id/reject", grantReqHandler.Reject)
	}

	// 用户组管理（仅管理员）：教学场景按组批量授权
	ugHandler := &UserGroupHandler{DB: deps.DB}
	userGroups := api.Group("/user-groups")
	userGroups.Use(middleware.AdminMiddleware())
	{
		userGroups.GET("", ugHandler.ListGroups)
		userGroups.POST("", ugHandler.CreateGroup)
		userGroups.PUT("/:id", ugHandler.UpdateGroup)
		userGroups.DELETE("/:id", ugHandler.DeleteGroup)
		userGroups.POST("/:id/members", ugHandler.SetMembers)
	}

	// SSH 主机指纹管理（仅管理员）：TOFU 首连记录的 host_keys，VM 重建换密钥后
	// 由管理员删旧指纹放行重录。暂无前端入口，curl/后续页面消费（记录在案）。
	sshHostKeysHandler := NewSSHHostKeyHandler(deps.DB)
	sshKeys := api.Group("/ssh-host-keys")
	sshKeys.Use(middleware.AdminMiddleware())
	{
		sshKeys.GET("", sshHostKeysHandler.List)
		sshKeys.DELETE("/:id", sshHostKeysHandler.Delete)
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
		// VM 内部指标（file_sd 抓取的 node_exporter：根分区/内存/负载；未装 exporter 降级 available=false）
		vms.GET("/:id/guest-metrics", historyHandler.GuestMetrics)
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
		// 删除预检（A）：删前亮家底（磁盘去向/快照数/守卫保留）
		vms.GET("/:id/delete-preview", vmHandler.DeletePreview)
		vms.DELETE("/:id", vmHandler.DeleteVM)
		vms.GET("/:id/snapshots", vmHandler.ListSnapshots)
		vms.POST("/:id/snapshots", vmHandler.CreateSnapshot)
		vms.DELETE("/:id/snapshots/:snap", vmHandler.DeleteSnapshot)
		vms.POST("/:id/snapshots/:snap/revert", vmHandler.RevertSnapshot)
		// 资产授权（借鉴堡垒机 4A）：admin 分配/查看/收回；handler 内 requireAdminRole 二次收口
		vms.GET("/:id/grants", vmHandler.ListVMGrants)
		vms.POST("/:id/grants", vmHandler.GrantVM)
		vms.DELETE("/:id/grants/:gid", vmHandler.RevokeVMGrant)
		// 授权申请（v3.6）：学生自助申请 → 教师审批（挂 vms 前缀以通过 operator 写权限）
		vms.GET("/apply-catalog", vmHandler.ApplyCatalog)
		vms.POST("/:id/grant-request", vmHandler.ApplyForAsset)
		// 组授权（v3.6）：组 → 资产，组内成员批量获得可见性
		vms.GET("/:id/group-grants", vmHandler.ListVMGroupGrants)
		vms.POST("/:id/group-grants", vmHandler.GrantVMToGroup)
		vms.DELETE("/:id/group-grants/:gid", vmHandler.RevokeVMGroupGrant)
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
		// VM 导出/导入（v3 批次 J：导出 operator，导入 handler 内收口仅 admin）
		vms.GET("/:id/export", vmExportHandler.Export)
		vms.POST("/import-file", vmExportHandler.Import)
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
		// 全库克隆家谱（跨池血缘图谱 + 回收候选聚合；克隆链天然跨池，数据与删卷守卫同源）
		storage.GET("/volume-graph", storageHandler.GetVolumeGraph)
		storage.POST("/pools/:name/orphan-cleanup", storageHandler.CleanupOrphans)
		storage.DELETE("/pools/:name/volumes/:vol", storageHandler.DeleteVolume)
	}

	// 网络管理（admin）
	networks := api.Group("/networks")
	networks.Use(middleware.OperatorMiddleware())
	{
		networks.GET("", networkHandler.ListNetworks)
		// 网络通信流量视图（P1：ss 归网连接边 + 接口速率，前端 3s 轮询）
		networks.GET("/flows", networkFlowHandler.Flows)
		// 全局网络拓扑（N1：宿主机 → 桥/虚拟网络 → VM/容器 三层关系图）
		networks.GET("/topology", networkTopologyHandler.Topology)
		// IP 地址分配一览（N2b：各网段已用/可用 IP 与归属）
		networks.GET("/ipam", networkIPAMHandler.IPAM)
		networks.GET("/:name", networkHandler.GetNetwork)
		networks.POST("", networkHandler.CreateNetwork)
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
		// 云镜像市场（v3 批次 H：下载仅管理员，handler 内 roleIsAdmin 收口）
		images.GET("/market", imageMarketHandler.ListMarket)
		images.POST("/market/download", imageMarketHandler.Download)
		images.GET("/market/iso", imageMarketHandler.ListMarketISO)
		images.POST("/market/iso/download", imageMarketHandler.DownloadISO)
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
		// 一键创建容器（v3.2 R8 收尾：docker run -d 封装，契约见 dockerx.ContainerOpts）
		docker.POST("/containers", dockerHandler.CreateContainer)
		docker.POST("/containers/:id/:action", dockerHandler.ContainerAction)
		docker.DELETE("/containers/:id", dockerHandler.RemoveContainer)
		docker.GET("/containers/:id/logs", dockerHandler.ContainerLogs)
		// 容器日志实时流（R4：Engine API logs follow，WS 推送 stdout/stderr 分色）
		docker.GET("/containers/:id/logs/ws", containerLogsHandler.Connect)
		// 容器终端（v3.2 R2：WS ↔ Docker Engine API exec TTY 流；viewer 由组内 NonViewerMiddleware 403）
		docker.GET("/containers/:id/terminal", containerTerminalHandler.Connect)
		// 容器详情 / 实时统计 / 全量统计（v3.2 R1/R4）
		docker.GET("/containers/:id/inspect", dockerHandler.InspectContainer)
		docker.GET("/containers/:id/stats", dockerHandler.ContainerStats)
		docker.GET("/stats", dockerHandler.StatsAll)
		docker.POST("/images/pull", dockerHandler.PullImage)
		docker.POST("/prune", dockerHandler.Prune)
		// 网络与卷管理（v3.2 R9/R10）
		docker.GET("/networks", dockerHandler.ListNetworks)
		docker.GET("/networks/:name", dockerHandler.NetworkDetail)
		docker.POST("/networks", dockerHandler.CreateNetwork)
		docker.DELETE("/networks/:name", dockerHandler.RemoveNetwork)
		docker.GET("/volumes", dockerHandler.ListVolumes)
		docker.POST("/volumes", dockerHandler.CreateVolume)
		docker.POST("/volumes/prune", dockerHandler.PruneVolumes)
		docker.DELETE("/volumes/:name", dockerHandler.RemoveVolume)
		// 编排项目（v3.2：compose ls + 项目级启停）
		docker.GET("/compose", dockerHandler.ComposeList)
		docker.POST("/compose/:name/:action", dockerHandler.ComposeAction)
		// 服务级：列表与单服务重启（栈详情抽屉用）
		docker.GET("/compose/:name/services", dockerHandler.ComposeServices)
		docker.POST("/compose/:name/services/:service/:action", dockerHandler.ComposeServiceAction)
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

	// 运维自动化（P4）：引擎状态 + 批量执行（adhoc/playbook）+ playbook CRUD
	EnsureSeedPlaybooks() // 内置种子 playbook 落盘（缺哪个补哪个，用户改过的不覆盖）
	ansibleHandler := NewAnsibleHandler(deps.DB, deps.Tasks, CredentialMasterSecret())
	ansibleGroup := api.Group("/ansible")
	ansibleGroup.Use(middleware.OperatorMiddleware())
	{
		// 主机组（AU1）：批量执行目标的快捷集合
		hostGroupHandler := NewHostGroupHandler(deps.DB)
		ansibleGroup.GET("/host-groups", hostGroupHandler.List)
		ansibleGroup.POST("/host-groups", hostGroupHandler.Create)
		ansibleGroup.PUT("/host-groups/:id", hostGroupHandler.Update)
		ansibleGroup.DELETE("/host-groups/:id", hostGroupHandler.Delete)
		ansibleGroup.GET("/status", ansibleHandler.Status)
		ansibleGroup.POST("/run", ansibleHandler.Run)
		ansibleGroup.POST("/deploy-key", ansibleHandler.DeployKey)
		ansibleGroup.GET("/playbooks", ansibleHandler.ListPlaybooks)
		ansibleGroup.GET("/playbooks/:id", ansibleHandler.GetPlaybook)
		ansibleGroup.POST("/playbooks", ansibleHandler.CreatePlaybook)
		ansibleGroup.POST("/playbooks/check", ansibleHandler.CheckPlaybook)
		ansibleGroup.PUT("/playbooks/:id", ansibleHandler.UpdatePlaybook)
		ansibleGroup.DELETE("/playbooks/:id", ansibleHandler.DeletePlaybook)
	}

	// cloud-init 配置模板（operator/admin：创建向导「套用模板/保存为模板」与管理页共用）
	citHandler := NewCloudInitTemplateHandler(deps.DB)
	cit := api.Group("/cloud-init-templates")
	cit.Use(middleware.OperatorMiddleware())
	{
		cit.GET("", citHandler.List)
		cit.GET("/:id", citHandler.Get)
		cit.POST("", citHandler.Create)
		cit.PUT("/:id", citHandler.Update)
		cit.DELETE("/:id", citHandler.Delete)
	}

	// 回收站（v3 批次 N：软删 VM 可视化恢复/彻底清除，仅管理员）
	recycle := api.Group("/vms-recycle")
	recycle.Use(middleware.AdminMiddleware())
	{
		recycle.GET("", recycleHandler.ListDeleted)
		recycle.POST("/:id/restore", recycleHandler.Restore)
		recycle.DELETE("/:id/purge", recycleHandler.Purge)
	}

	// 工具箱（v3 批次 E：进程/磁盘/清理，仅管理员）

	// 计划任务（平台管理语义，仅管理员）
	crons := api.Group("/crons")
	crons.Use(middleware.AdminMiddleware())
	{
		crons.GET("", cronsHandler.List)
		crons.POST("", cronsHandler.Create)
		// 表达式下次执行预览（静态路由与 /:id 同级，gin 静态优先，与 vms/options 同款）
		crons.GET("/preview", cronsHandler.Preview)
		// 内置任务模板（只读，前端「从模板新建」用）
		crons.GET("/templates", cronsHandler.Templates)
		// 复制任务为副本（默认停用）
		crons.POST("/:id/duplicate", cronsHandler.Duplicate)
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
		// 原生看板历史曲线（Grafana 退役批次 2026-10：池使用率 + VM 六指标）
		monitor.GET("/pool-history", historyHandler.PoolHistory)
		monitor.GET("/vm-metrics-history", historyHandler.VMDetailedHistory)
		// 通用只读 PromQL 查询（原生看板扩展层：panels.js 注册表驱动，加面板零后端改动）
		monitor.POST("/prom-query", historyHandler.PromQuery)
	}

	// 声明式部署栈（P2A：清单 operator+；deploy admin；docs 命中栈元数据清单才可读）
	stackHandler := NewStackHandler(deps.Tasks)
	stacksGroup := api.Group("/stacks")
	stacksGroup.Use(middleware.OperatorMiddleware())
	{
		stacksGroup.GET("", stackHandler.List)
		stacksGroup.GET("/:id/docs", stackHandler.Docs)
		stacksGroup.POST("/:id/deploy", stackHandler.Deploy)
		// 栈详情（服务列表 + 部署副本 + 漂移）与文件编辑（admin）、升级（admin，异步任务）
		stacksGroup.GET("/:id/detail", stackHandler.Detail)
		stacksGroup.PUT("/:id/file", stackHandler.File)
		stacksGroup.POST("/:id/upgrade", stackHandler.Upgrade)
	}

	// 镜像版本检测（P3：本地 digest vs 镜像代理远端 digest，按钮触发非轮询）
	versionCheckHandler := NewVersionCheckHandler()
	docker.GET("/version-check", versionCheckHandler.Check)

	// 架构设计器（P2B：模板/计划 CRUD operator+；export 文本下载；apply admin）
	designerHandler := NewDesignerHandler(deps.DB, deps.Tasks, vmCredHandler)
	// DE2：把漂移巡检注入 cron（cron → handler 反向 import 会成环，用回调注入）
	cronScheduler.DriftCheck = designerHandler.DriftSummary
	designer := api.Group("/designer")
	designer.Use(middleware.OperatorMiddleware())
	{
		designer.GET("/templates", designerHandler.Templates)
		designer.GET("/plans", designerHandler.ListPlans)
		designer.POST("/plans", designerHandler.SavePlan)
		designer.DELETE("/plans/:id", designerHandler.DeletePlan)
		designer.GET("/plans/:id/export", designerHandler.ExportYAML)
		// 画布 → Ansible（DE1）：inventory + site.yml，落 playbook 库
		designer.GET("/plans/:id/export-ansible", designerHandler.ExportAnsible)
		// 快照 vs 现实 漂移对比（DE2）
		designer.GET("/plans/:id/drift", designerHandler.Drift)
		designer.POST("/plans/:id/apply", designerHandler.Apply)
		designer.GET("/plans/:id/apply-status", designerHandler.ApplyStatus)
	}

	// 站内通知（告警到人）：所有登录角色可见自己的通知（viewer 也收告警——被授权资产的故障他们必须知道）
	notificationHandler := NewNotificationHandler(deps.DB)
	notifications := api.Group("/notifications")
	{
		notifications.GET("", notificationHandler.List)
		notifications.PUT("/read-all", notificationHandler.MarkAllRead)
		notifications.PUT("/:id/read", notificationHandler.MarkRead)
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
		// 全站活动流（最近审计写操作摘要）：operator 可见，viewer 由组内 OperatorMiddleware 403
		dashboard.GET("/activity", dashboardHandler.Activity)
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
		// 审计 CSV 流式导出（S1-3：替代前端拼接，万条级不卡浏览器）
		audit.GET("/export", auditHandler.ExportAuditCSV)
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
		taskRoutes.POST("/:id/cancel", taskHandler.CancelTask)
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
