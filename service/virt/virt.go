package virt

import (
	"fmt"
	"net/url"
	"sync"

	"github.com/digitalocean/go-libvirt"
)

// URI 平台实际使用的 libvirt 连接地址（unix socket 直连系统守护进程）。
// 早期由 LIBVIRT_URI 环境变量配置但从未接入连接逻辑，已删配置项，导出常量供设置页展示真实值。
const URI = string(libvirt.QEMUSystem)

// Virt 是对 digitalocean/go-libvirt 的封装，提供 libvirt RPC 直连能力。
// 底层通过 unix socket 连接 qemu:///system，无需 CGO 与 C 头文件。
type Virt struct {
	mu  sync.Mutex
	uri libvirt.ConnectURI
	con *libvirt.Libvirt

	// statsMu 保护 statsCache（性能统计差分采样缓存，见 stats.go）。
	statsMu    sync.Mutex
	statsCache map[string]*statSample
}

// New 创建 Virt 实例（惰性连接，首次调用时建立）。
func New() *Virt {
	return &Virt{uri: libvirt.QEMUSystem}
}

// Connect 建立 libvirt 连接；已连接则直接返回。
func (v *Virt) Connect() (*libvirt.Libvirt, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.con != nil {
		return v.con, nil
	}

	u, err := url.Parse(string(v.uri))
	if err != nil {
		return nil, fmt.Errorf("解析 libvirt URI 失败: %w", err)
	}

	l, err := libvirt.ConnectToURI(u)
	if err != nil {
		return nil, fmt.Errorf("连接 libvirt 失败: %w", err)
	}

	v.con = l
	return l, nil
}

// Reset 关闭并重置连接（断线后调用，下次 Connect 重建）。
func (v *Virt) Reset() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.con != nil {
		_ = v.con.Disconnect()
		v.con = nil
	}
}

// getConn 获取可用连接，连接已断开则自动重建。
// 每次调用都做一次廉价 RPC 探活：libvirt 连接断开（如 libvirtd 重启）时，
// 立即 Reset 并重建连接，避免长期持有失效连接导致所有后续操作失败。
func (v *Virt) getConn() (*libvirt.Libvirt, error) {
	l, err := v.Connect()
	if err != nil {
		return nil, err
	}
	if _, err := l.ConnectGetVersion(); err != nil {
		v.Reset()
		l, err = v.Connect()
		if err != nil {
			return nil, err
		}
	}
	return l, nil
}
