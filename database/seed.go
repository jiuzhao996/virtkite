package database

import (
	"crypto/rand"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/jiuzhao/vmops/middleware"
	"github.com/jiuzhao/vmops/model"
	"gorm.io/gorm"
)

// InitSeedData 初始化种子数据（首次启动创建 admin/user 两个账号）。
// 从 main.go 迁入：口令解析/生成/打印与建账号同属一个初始化语义，收拢在此便于整体审计。
func InitSeedData(db *gorm.DB) {
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

	// 初始口令优先取环境变量，未设置则随机生成（不再硬编码 "password"：种子账号是系统最高
	// 权限入口，公开口令等于整机失守）。生成失败必须中止，否则会写入空哈希造成账号不可登录。
	adminPass, err := resolveSeedPassword(adminPasswordEnv)
	if err != nil {
		log.Printf("解析管理员初始口令失败，跳过种子数据初始化: %v", err)
		return
	}
	viewerPass, err := resolveSeedPassword(viewerPasswordEnv)
	if err != nil {
		log.Printf("解析普通用户初始口令失败，跳过种子数据初始化: %v", err)
		return
	}

	// 生成密码哈希：失败必须中止，否则会写入空哈希造成账号静默不可登录
	adminHash, err := middleware.HashPassword(adminPass)
	if err != nil {
		log.Printf("生成管理员密码哈希失败，跳过种子数据初始化: %v", err)
		return
	}
	userHash, err := middleware.HashPassword(viewerPass)
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
		return
	}
	if err := db.Create(user).Error; err != nil {
		log.Printf("创建种子普通用户失败: %v", err)
		return
	}

	log.Println("✅ 种子数据初始化完成")
	log.Println("   👑 管理员: admin（角色 admin）")
	log.Println("   👤 用户:   user（角色 viewer）")
	// 口令明文只在这一次启动时打到 stdout（见 printSeedPasswords）：不落库、没有找回入口，
	// 日志之外的任何地方都不再出现。
	printSeedPasswords(adminPass, viewerPass)
}

// 种子账号初始口令的环境变量名：运维可预置（适合无人值守部署、口令只在交付环节告知），
// 未设置则回退到随机生成。
const (
	adminPasswordEnv  = "ADMIN_INITIAL_PASSWORD"
	viewerPasswordEnv = "VIEWER_INITIAL_PASSWORD"
)

// seedPasswordLen 随机口令长度：字符集 68 个 → 20 位约 122 bit 熵。
const seedPasswordLen = 20

// seedPasswordAlphabet 随机口令字符集：剔除 0/O、1/l/I 等易混淆字符——口令只打印一次、
// 靠人工抄录，抄错就得改库重置。
const seedPasswordAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789!@#$%^&*-_=+"

// weakSeedPasswords 已知弱口令黑名单：环境变量预置了这些值时不阻断启动（会让无人值守部署
// 起不来），只打醒目告警——口令来源是可控运维，风险交由登录后的改密处置。
// 只列通用弱口令，不写本项目历史口令字面量（免得 grep 审计再次命中）。
var weakSeedPasswords = map[string]bool{
	"password": true, "123456": true, "12345678": true, "admin": true,
	"root": true, "changeme": true,
}

// minSeedPasswordLen 环境变量预置口令的告警下限：短于此值视为弱口令（只告警不阻断）。
const minSeedPasswordLen = 12

// resolveSeedPassword 决定种子账号的初始口令：envKey 非空则用它，否则 crypto/rand 生成。
// 熵源读失败直接返回错误（%w 包装）：退化成可预测口令不如让种子创建失败并留下日志。
func resolveSeedPassword(envKey string) (string, error) {
	if v := strings.TrimSpace(os.Getenv(envKey)); v != "" {
		if len([]rune(v)) < minSeedPasswordLen || weakSeedPasswords[strings.ToLower(v)] {
			log.Printf("⚠️ %s 预置的初始口令过短或命中弱口令黑名单，请首次登录后立即修改", envKey)
		}
		return v, nil
	}
	return randomSeedPassword(seedPasswordLen)
}

// randomSeedPassword 用 crypto/rand 生成 length 位口令，采用拒绝采样避免取模偏置。
func randomSeedPassword(length int) (string, error) {
	if length <= 0 {
		return "", fmt.Errorf("初始口令长度必须为正整数，当前 %d", length)
	}
	// len(alphabet) 不整除 256，直接取模会让靠前字符概率偏高
	limit := 256 - 256%len(seedPasswordAlphabet)
	out := make([]byte, 0, length)
	buf := make([]byte, length)
	for len(out) < length {
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("读取随机源失败，无法生成初始口令: %w", err)
		}
		for _, b := range buf {
			if int(b) >= limit {
				continue
			}
			out = append(out, seedPasswordAlphabet[int(b)%len(seedPasswordAlphabet)])
			if len(out) == length {
				break
			}
		}
	}
	return string(out), nil
}

// printSeedPasswords 把两个种子账号的初始口令一次性打到 stdout（启动日志）。
// 随机口令不落库、无找回入口，这是唯一的可见机会；明文只出现一次，且明确提示立即改密。
func printSeedPasswords(adminPass, viewerPass string) {
	fmt.Fprintln(os.Stdout, "")
	fmt.Fprintln(os.Stdout, "🔐 初始口令（仅本次启动打印一次，请立即抄录并在首次登录后修改）：")
	fmt.Fprintf(os.Stdout, "   👑 admin（管理员）: %s\n", adminPass)
	fmt.Fprintf(os.Stdout, "   👤 user（只读）  : %s\n", viewerPass)
	fmt.Fprintln(os.Stdout, "   ⚠️ 口令不会再次显示，也不可用任何接口找回；遗忘只能由管理员改密或直连数据库重置")
	fmt.Fprintln(os.Stdout, "")
}
