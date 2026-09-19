// hostkey.go SSH 主机密钥 TOFU（Trust On First Use）校验。
//
// 此前 handler/terminal.go 与本包各写一处 ssh.InsecureIgnoreHostKey()，主机密钥完全不
// 校验：中间人只要能劫持路由（ARP 欺骗 / DHCP 抢占）即可在 SSH 认证发生前替换对端。
// 本文件提供统一的主机密钥回调：首次连接记录服务器公钥的 SHA256 指纹（等价 openssh
// 客户端写入 known_hosts），后续连接逐一比对，不一致即拒绝连接。
//
// 取舍（与 AGENTS.md 记录的旧论证衔接）：平台自建虚拟机重建即换主机密钥，维护
// known_hosts 的「人工核对」环节不可操作，故采用自动 TOFU 而非人工确认；
// 目标范围仍由调用方 validateSSHTarget 白名单收敛到本机私有网段，本层是纵深防御。
//
// 存储经 HostKeyStore 接口抽象（gorm 实现在本文件），单测用内存 map 替换，不依赖 DB。
package vmssh

import (
	"errors"
	"fmt"
	"log"
	"net"
	"strconv"
	"sync"

	"github.com/jiuzhao/vmops/model"
	"golang.org/x/crypto/ssh"
	"gorm.io/gorm"
)

// ErrHostKeyMismatch 主机密钥指纹与首次连接记录不一致（TOFU 校验失败）的哨兵错误。
// 调用方用 errors.Is 识别后向前端回显固定中文文案（Error() 即文案本身），
// 完整错误（含期望/实际指纹）进服务端日志。
var ErrHostKeyMismatch = errors.New("目标主机密钥指纹与首次连接记录不一致，可能存在中间人风险；确认安全后请管理员删除旧指纹记录再重试")

// HostKeyStore 主机指纹存储抽象。Lookup 未命中返回 (nil, nil)；DB 故障返回 error，
// 回调一律 fail-closed（查不到记录状态时拒绝连接，绝不能退化成不校验）。
type HostKeyStore interface {
	// Lookup 取 (host, port) 已记录的指纹；无记录返回 (nil, nil)。
	Lookup(host string, port int) (*model.HostKey, error)
	// Save 首次连接时落库指纹。
	Save(k *model.HostKey) error
}

// GormHostKeyStore HostKeyStore 的 gorm 实现（host_keys 表，main 启动时注入全局）。
type GormHostKeyStore struct {
	DB *gorm.DB
}

// Lookup 按 (host, port) 查已记录指纹；ErrRecordNotFound 归一化为 (nil, nil)。
func (s GormHostKeyStore) Lookup(host string, port int) (*model.HostKey, error) {
	var rec model.HostKey
	err := s.DB.Where("host = ? AND port = ?", host, port).First(&rec).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

// Save 落库指纹。(host, port) 有唯一索引，并发首连撞索引时第二个连接报错拒绝——
// 两条记录的指纹必然相同（同一台服务器），重连一次即可，属可接受的保守行为。
func (s GormHostKeyStore) Save(k *model.HostKey) error {
	return s.DB.Create(k).Error
}

// 全局指纹存储：main 启动注入（SetHostKeyStore），terminal.go 与本包拨号共用，
// 调用点无需逐处传 store（与 tasks.DefaultStoragePoolResolver 同款包级接线风格）。
var (
	storeMu     sync.RWMutex
	hostKeyStor HostKeyStore
)

// SetHostKeyStore 注入全局主机指纹存储（main 启动时调用一次）。
func SetHostKeyStore(s HostKeyStore) {
	storeMu.Lock()
	defer storeMu.Unlock()
	hostKeyStor = s
}

// currentStore 取全局存储（未注入返回 nil，回调内 fail-closed）。
func currentStore() HostKeyStore {
	storeMu.RLock()
	defer storeMu.RUnlock()
	return hostKeyStor
}

// FingerprintSHA256 公钥的 SHA256 指纹（"SHA256:<base64>"，与 openssh
// `ssh-keygen -lf` / known_hosts 的格式一致）。
func FingerprintSHA256(pub ssh.PublicKey) string {
	return ssh.FingerprintSHA256(pub)
}

// TOFUHostKeyCallback 返回基于全局指纹存储的主机密钥校验回调。
// 这是对 ssh.InsecureIgnoreHostKey 的全仓库唯一替代——两处 SSH 拨号必须都走这里。
func TOFUHostKeyCallback() ssh.HostKeyCallback {
	return tofuCallback(currentStore())
}

// tofuCallback 基于给定存储构造回调（store 为 nil 时一律拒绝，fail-closed）。
// 单测入口：传内存实现即可覆盖首次记录/一致放行/不一致拒绝三条路径。
func tofuCallback(store HostKeyStore) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		if store == nil {
			return errors.New("主机指纹存储未初始化，已拒绝连接")
		}
		// ssh.Dial 收到的恒为 host:port（IPv6 为 [host]:port），SplitHostPort 还原两段
		host, portStr, err := net.SplitHostPort(hostname)
		if err != nil {
			return fmt.Errorf("主机地址解析失败: %w", err)
		}
		port, err := strconv.Atoi(portStr)
		if err != nil {
			return fmt.Errorf("主机端口解析失败: %w", err)
		}

		fp := FingerprintSHA256(key)
		rec, err := store.Lookup(host, port)
		if err != nil {
			// 记录状态未知时拒绝（fail-closed）：放行等于回退到不校验
			return fmt.Errorf("主机指纹查询失败，已拒绝连接: %w", err)
		}
		if rec == nil {
			// TOFU 首次连接：记录指纹并放行。落库失败同样拒绝——不落库的放行
			// 会让后续每次连接都停留在「首次」，校验形同虚设
			rec = &model.HostKey{Host: host, Port: port, KeyType: key.Type(), Fingerprint: fp}
			if err := store.Save(rec); err != nil {
				return fmt.Errorf("主机指纹记录失败，已拒绝连接: %w", err)
			}
			log.Printf("[vmssh] TOFU 首次连接已记录主机指纹 %s:%d %s %s", host, port, key.Type(), fp)
			return nil
		}
		if rec.Fingerprint != fp {
			// 带上下文包装哨兵错误：errors.Is 可识别（WS 帧取固定文案），细节进日志
			log.Printf("[vmssh] 主机指纹不一致 host=%s:%d key_type=%s 期望=%s 实际=%s",
				host, port, key.Type(), rec.Fingerprint, fp)
			return fmt.Errorf("%w（%s:%d 期望 %s，实际 %s）", ErrHostKeyMismatch, host, port, rec.Fingerprint, fp)
		}
		return nil
	}
}
