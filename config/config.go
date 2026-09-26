package config

import (
	"os"
	"strconv"
)

type Config struct {
	// 数据库配置
	DBHost     string
	DBPort     int
	DBUser     string
	DBPassword string
	DBName     string

	// JWT配置
	JWTSecretKey     string
	JWTExpireMinutes int

	// 凭据加密主密钥（VM SSH 凭据落库用的 AES-256-GCM 主密钥）。
	// 必须与 JWT 密钥分离：二者同源会让「轮换 JWT_SECRET_KEY」连带废掉全部历史凭据
	// （报错表现为「凭据不存在或已损坏」，极易被误判为数据损坏而触发重装）。
	// 未配置（空）时由 handler.CredentialMasterSecret 回落到 JWTSecretKey 并打醒目警告。
	CredentialMasterKey string

	// 服务器配置
	ServerPort string
	ServerMode string

	// CORS配置
	CORSOrigins string

	// 镜像存储目录
	ImageDir string

	// cloud-init seed 镜像目录（需当前用户可写、qemu 进程可读；不依赖存储池目录权限）
	SeedDir string

	// Grafana 地址（监控中心看板 iframe 与探活用；compose 内为 http://grafana:3000）
	GrafanaURL string

	// Alertmanager 地址（监控中心代理查询告警用；docker compose 内为 http://alertmanager:9093）
	AlertmanagerURL string

	// Prometheus 地址（仪表盘/虚拟机详情的历史曲线查询用；docker compose 内为 http://prometheus:9090）
	PrometheusURL string

	// /metrics 抓取令牌（非空时 /metrics 要求 Authorization: Bearer <token> 或 ?token=<token>，
	// 与 deploy/prometheus.yml 抓取任务的 bearer_token 配对；为空则保持公开，用防火墙限制来源）
	MetricsToken string

	// Prometheus file_sd 目标文件路径（非空时后台协程每分钟把 running 且有 IP 的 VM
	// 原子写入该文件，供 prometheus.yml 的 file_sd_configs 消费；为空则不启用）
	FileSDPath string

	// Alertmanager webhook 令牌（非空时 POST /api/monitor/webhook 要求 ?token= 或
	// Authorization: Bearer 匹配；为空则公开，与 deploy/alertmanager.yml 的 webhook_config 配对）
	AlertWebhookToken string

	// Loki 日志栈地址（监控中心日志查询代理用；compose 内 http://loki:3100）
	LokiURL string

	// SSH 跳板入口（jumpd）：JUMPD_ENABLED=1 才启动；监听 JUMPD_PORT（默认 2222）。
	// 默认关闭——公网部署须先评估口令爆破面（服务内有 per-IP 限流兜底）
	JumpdEnabled bool
	JumpdPort    int
}

var GlobalConfig *Config

func Init() {
	GlobalConfig = &Config{
		// 数据库配置
		DBHost: getEnv("DB_HOST", "127.0.0.1"),
		DBPort: getEnvAsInt("DB_PORT", 3306),
		DBUser: getEnv("DB_USER", "vmops"),
		// 口令不再提供默认值：此前默认 vmops123 与 docker-compose.yml 的弱口令互为影子，
		// 源码外泄即等于库口令外泄；且运维改了库口令后，未设此变量的环境会静默连库失败。
		// 部署必须显式提供（见 .env.example），缺失时由连接错误直接暴露，好过带弱口令"跑起来"。
		DBPassword: getEnv("DB_PASSWORD", ""),
		DBName:     getEnv("DB_NAME", "vmops"),

		// JWT配置
		JWTSecretKey:     getEnv("JWT_SECRET_KEY", "vmops-jwt-secret-key-change-in-production"),
		JWTExpireMinutes: getEnvAsInt("JWT_EXPIRE_MINUTES", 1440),

		// 凭据加密主密钥（独立配置项，留空即回落 JWT 密钥并告警；新部署一律显式配置）
		CredentialMasterKey: getEnv("CREDENTIAL_MASTER_KEY", ""),

		// 服务器配置。默认 release（安全默认）：裸部署不再落入"默认 JWT 密钥 + CORS *"
		// 的可伪造态；本地开发由 start.sh 显式 export SERVER_MODE=debug。
		ServerPort: getEnv("SERVER_PORT", "8080"),
		ServerMode: getEnv("SERVER_MODE", "release"),

		// CORS配置
		CORSOrigins: getEnv("CORS_ORIGINS", "*"),

		// 镜像存储目录
		ImageDir: getEnv("IMAGE_DIR", "/var/lib/libvirt/images"),

		// cloud-init seed 镜像目录
		SeedDir: getEnv("SEED_DIR", "/home/jiuzhao/vmops/data/seed"),

		// Grafana 地址
		GrafanaURL: getEnv("GRAFANA_URL", "http://127.0.0.1:3000"),

		// Alertmanager 地址
		AlertmanagerURL: getEnv("ALERTMANAGER_URL", "http://127.0.0.1:9093"),

		// Prometheus 地址
		PrometheusURL: getEnv("PROMETHEUS_URL", "http://127.0.0.1:9090"),

		// /metrics 抓取令牌
		MetricsToken: getEnv("METRICS_TOKEN", ""),

		// Prometheus file_sd 目标文件路径
		FileSDPath: getEnv("FILE_SD_PATH", ""),

		// Alertmanager webhook 令牌
		AlertWebhookToken: getEnv("ALERT_WEBHOOK_TOKEN", ""),
		JumpdEnabled:      getEnv("JUMPD_ENABLED", "") == "1",
		JumpdPort:         getEnvAsInt("JUMPD_PORT", 2222),
	}
}

func getEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

func getEnvAsInt(key string, defaultValue int) int {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	intValue, err := strconv.Atoi(value)
	if err != nil {
		return defaultValue
	}
	return intValue
}
