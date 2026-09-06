// Package setting 系统可写配置：DB 持久化 + 进程内缓存。
// 键必须是本包白名单内的合法键；取值经 Validate 校验后才允许落库。
// 消费方（tasks/console/vnc）通过包级 resolver 钩子读取，本包不反向依赖任何服务包。
package setting

import (
	"fmt"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/jiuzhao/vmops/model"
	"gorm.io/gorm"
)

// 可写配置键白名单。
const (
	KeyDefaultStoragePool = "default_storage_pool" // 未指定存储池时使用的默认池名
	KeyVNCTokenTTLMin     = "vnc_token_ttl_min"    // VNC 访问 token 有效期（分钟）
	KeyVNCStaleMin        = "vnc_stale_min"        // VNC 会话无活动判定过期时长（分钟）
)

// 各键默认值（与配置化之前的硬编码行为一致）。
const (
	// 默认池兜底值：vmops 池已按规划淘汰，缺省落到主力系统盘池 images
	DefaultStoragePoolFallback = "images"
	VNCTokenTTLDefaultMin      = 5
	VNCStaleDefaultMin         = 60
)

// poolNameRe 存储池名白名单：字母数字下划线点连字符（libvirt 池名约束的保守子集）。
var poolNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

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
func (m *Manager) All() map[string]string {
	return map[string]string{
		KeyDefaultStoragePool: m.DefaultStoragePool(),
		KeyVNCTokenTTLMin:     strconv.Itoa(m.getInt(KeyVNCTokenTTLMin, VNCTokenTTLDefaultMin)),
		KeyVNCStaleMin:        strconv.Itoa(m.getInt(KeyVNCStaleMin, VNCStaleDefaultMin)),
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
	default:
		return fmt.Errorf("不支持的配置项: %s", key)
	}
}

// validateRange 校验 value 是 [min,max] 内的整数。
func validateRange(value string, min, max int, label string) error {
	n, err := strconv.Atoi(value)
	if err != nil || n < min || n > max {
		return fmt.Errorf("%s 必须是 %d-%d 的整数", label, min, max)
	}
	return nil
}
