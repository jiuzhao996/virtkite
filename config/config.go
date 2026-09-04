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

	// 虚拟化配置
	LibvirtURI string

	// 镜像存储目录
	ImageDir string
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

		// 虚拟化配置
		LibvirtURI: getEnv("LIBVIRT_URI", "qemu:///system"),

		// 镜像存储目录
		ImageDir: getEnv("IMAGE_DIR", "/var/lib/libvirt/images"),
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
