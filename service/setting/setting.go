// Package setting 系统可写配置：DB 持久化 + 进程内缓存。
// 键必须是本包白名单内的合法键；取值经 Validate 校验后才允许落库。
// 消费方（tasks/console/vnc）通过包级 resolver 钩子读取，本包不反向依赖任何服务包。
package setting

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jiuzhao/vmops/model"
	"gorm.io/gorm"
)

// 可写配置键白名单。
const (
	KeyDefaultStoragePool = "default_storage_pool" // 未指定存储池时使用的默认池名
	KeyVNCTokenTTLMin     = "vnc_token_ttl_min"    // VNC 访问 token 有效期（分钟）
	KeyVNCStaleMin        = "vnc_stale_min"        // VNC 会话无活动判定过期时长（分钟）
	KeyAIBaseURL          = "ai_base_url"          // OpenAI 兼容 API 地址（如 https://api.deepseek.com/v1）
	KeyAIAPIKey           = "ai_api_key"           // AI 服务 API Key（服务端保存，永不下发前端）
	KeyAIModel            = "ai_model"             // 模型名（如 deepseek-chat / glm-4）
	KeySecurityEntrance   = "security_entrance"    // 登录安全入口口令（空=关闭；设置后登录须携带 X-Entrance 头或 ?entrance= 参数）
	KeyPasswordMinLength  = "password_min_length"  // 密码最小长度（0=关闭策略；默认 8）
)

// 各键默认值（与配置化之前的硬编码行为一致）。
const (
	// 默认池兜底值：vmops 池已按规划淘汰，缺省落到主力系统盘池 images
	DefaultStoragePoolFallback = "images"
	VNCTokenTTLDefaultMin      = 5
	VNCStaleDefaultMin         = 60
	SecurityEntranceDefault    = "" // 空串=安全入口关闭（放行所有登录请求）
	PasswordMinLengthDefault   = 8

	// passwordStrongPolicyMin 密码策略混合字符要求的起算长度：
	// 策略达到该值时除长度外还要求至少含字母与数字各一（等价常见 Web 平台的弱口令底线）。
	passwordStrongPolicyMin = 8
)

// poolNameRe 存储池名白名单：字母数字下划线点连字符（libvirt 池名约束的保守子集）。
var poolNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// entranceRe 安全入口口令白名单：仅 URL 与 HTTP 头都安全的字符（字母数字下划线连字符），
// 避免放进 X-Entrance 头或 query 参数时出现转义/解析歧义。
var entranceRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Manager 系统可写配置管理器。
type Manager struct {
	db    *gorm.DB
	mu    sync.RWMutex
	cache map[string]string
}

// NewManager 创建管理器并把已有配置载入缓存；DB 异常不阻断启动（退回全默认值）。
func NewManager(db *gorm.DB) *Manager {
	m := &Manager{db: db, cache: map[string]string{}}
	var rows []model.Setting
	if err := db.Find(&rows).Error; err != nil {
		fmt.Printf("[setting] 载入系统配置失败，全部使用默认值: %v\n", err)
		return m
	}
	for _, r := range rows {
		m.cache[r.Key] = r.Value
	}
	return m
}

// GetStr 读取字符串配置，未设置或为空返回 def。
func (m *Manager) GetStr(key, def string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if v, ok := m.cache[key]; ok && v != "" {
		return v
	}
	return def
}

// getInt 读取整数配置，未设置或非法返回 def。
func (m *Manager) getInt(key string, def int) int {
	m.mu.RLock()
	v, ok := m.cache[key]
	m.mu.RUnlock()
	if !ok {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// Set 写入配置并刷新缓存（等价 virsh 侧无对应命令，属平台自身配置）。
// 调用方必须先过 Validate。
func (m *Manager) Set(key, value string) error {
	if err := Validate(key, value); err != nil {
		return err
	}
	s := model.Setting{Key: key, Value: value, UpdatedAt: time.Now()}
	// 主键为字符串键，用 Save 做不存在则插入、存在则全量覆盖
	if err := m.db.Save(&s).Error; err != nil {
		return fmt.Errorf("写入系统配置 %s: %w", key, err)
	}
	m.mu.Lock()
	m.cache[key] = value
	m.mu.Unlock()
	return nil
}

// All 返回全部可写项当前值（未设置的键填充默认值），供设置页快照展示。
// 注意：ai_* 三键刻意不进快照（ai_api_key 永不下发前端，见键注释）。
func (m *Manager) All() map[string]string {
	return map[string]string{
		KeyDefaultStoragePool: m.DefaultStoragePool(),
		KeyVNCTokenTTLMin:     strconv.Itoa(m.getInt(KeyVNCTokenTTLMin, VNCTokenTTLDefaultMin)),
		KeyVNCStaleMin:        strconv.Itoa(m.getInt(KeyVNCStaleMin, VNCStaleDefaultMin)),
		KeySecurityEntrance:   m.SecurityEntrance(),
		KeyPasswordMinLength:  strconv.Itoa(m.PasswordMinLength()),
	}
}

// DefaultStoragePool 未指定存储池时使用的默认池名（等价 virsh pool-list 里的默认目标池）。
func (m *Manager) DefaultStoragePool() string {
	return m.GetStr(KeyDefaultStoragePool, DefaultStoragePoolFallback)
}

// VNCTokenTTL VNC 访问 token 有效期（等价 websockify token 的一次性授权窗口）。
func (m *Manager) VNCTokenTTL() time.Duration {
	return time.Duration(m.getInt(KeyVNCTokenTTLMin, VNCTokenTTLDefaultMin)) * time.Minute
}

// VNCStale VNC 会话无活动判定过期的时长（等价会话清扫器的收敛阈值）。
func (m *Manager) VNCStale() time.Duration {
	return time.Duration(m.getInt(KeyVNCStaleMin, VNCStaleDefaultMin)) * time.Minute
}

// SecurityEntrance 登录安全入口口令（等价 nginx 屏蔽后台地址的自定义暗号）。
// 空=关闭；非空时登录请求须携带匹配的 X-Entrance 头或 ?entrance= 查询参数，不匹配按 404 处理。
func (m *Manager) SecurityEntrance() string {
	return m.GetStr(KeySecurityEntrance, SecurityEntranceDefault)
}

// PasswordMinLength 密码最小长度策略（0=关闭策略）。
func (m *Manager) PasswordMinLength() int {
	return m.getInt(KeyPasswordMinLength, PasswordMinLengthDefault)
}

// Validate 校验键与取值。键必须在白名单内，取值按键的类型与范围校验。
func Validate(key, value string) error {
	switch key {
	case KeyDefaultStoragePool:
		if !poolNameRe.MatchString(value) {
			return fmt.Errorf("默认存储池名不合法（1-64 位字母数字_.-）")
		}
		return nil
	case KeyVNCTokenTTLMin:
		return validateRange(value, 1, 60, "VNC token 有效期")
	case KeyVNCStaleMin:
		return validateRange(value, 5, 1440, "VNC 会话过期判定时长")
	case KeyAIBaseURL, KeyAIModel:
		if len(strings.TrimSpace(value)) == 0 || len(value) > 300 {
			return fmt.Errorf("取值长度需在 1-300 之间")
		}
		return nil
	case KeyAIAPIKey:
		if len(strings.TrimSpace(value)) == 0 || len(value) > 300 {
			return fmt.Errorf("取值长度需在 1-300 之间")
		}
		return nil
	case KeySecurityEntrance:
		// 空值=关闭入口，允许；设置时仅接受 URL/头安全的字符，且不收纯空白
		if value == "" {
			return nil
		}
		if strings.TrimSpace(value) == "" || !entranceRe.MatchString(value) {
			return fmt.Errorf("安全入口口令不合法（1-64 位字母数字_-，空值表示关闭）")
		}
		return nil
	case KeyPasswordMinLength:
		return validateRange(value, 0, 64, "密码最小长度")
	default:
		return fmt.Errorf("不支持的配置项: %s", key)
	}
}

// ValidatePassword 按策略校验密码（纯函数，供改密/建用户/改用户口令各入口复用）。
// policy <= 0 视为策略关闭直接放行；长度不足报中文固定文案（可直接回显给前端）；
// policy >= passwordStrongPolicyMin 时额外要求至少含一个字母与一个数字，堵纯数字/纯符号弱口令。
func ValidatePassword(policy int, password string) error {
	if policy <= 0 {
		return nil
	}
	// 按 Unicode 字符数计长（中文口令按字数而非字节数，与用户直觉一致）
	if utf8.RuneCountInString(password) < policy {
		return fmt.Errorf("密码长度需不少于 %d 位", policy)
	}
	if policy >= passwordStrongPolicyMin {
		var hasLetter, hasDigit bool
		for _, r := range password {
			switch {
			case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
				hasLetter = true
			case r >= '0' && r <= '9':
				hasDigit = true
			}
		}
		if !hasLetter || !hasDigit {
			return fmt.Errorf("密码需同时包含字母和数字")
		}
	}
	return nil
}

// validateRange 校验 value 是 [min,max] 内的整数。
func validateRange(value string, min, max int, label string) error {
	n, err := strconv.Atoi(value)
	if err != nil || n < min || n > max {
		return fmt.Errorf("%s 必须是 %d-%d 的整数", label, min, max)
	}
	return nil
}
