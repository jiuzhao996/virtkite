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
}

var GlobalConfig *Config

func Init() {
	GlobalConfig = &Config{
		// 数据库配置
		DBHost:     getEnv("DB_HOST", "127.0.0.1"),
		DBPort:     getEnvAsInt("DB_PORT", 3306),
		DBUser:     getEnv("DB_USER", "vmops"),
		DBPassword: getEnv("DB_PASSWORD", "vmops123"),
		DBName:     getEnv("DB_NAME", "vmops"),

		// JWT配置
		JWTSecretKey:     getEnv("JWT_SECRET_KEY", "vmops-jwt-secret-key-change-in-production"),
		JWTExpireMinutes: getEnvAsInt("JWT_EXPIRE_MINUTES", 1440),

		// 服务器配置
		ServerPort: getEnv("SERVER_PORT", "8080"),
		ServerMode: getEnv("SERVER_MODE", "debug"),

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
