package virt

import (
	"net/url"
	"sync"

	"github.com/digitalocean/go-libvirt"
)

// Virt 是对 digitalocean/go-libvirt 的封装，提供 libvirt RPC 直连能力。
// 底层通过 unix socket 连接 qemu:///system，无需 CGO 与 C 头文件。
type Virt struct {
	mu  sync.Mutex
	uri libvirt.ConnectURI
	con *libvirt.Libvirt
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
		return nil, err
	}

	l, err := libvirt.ConnectToURI(u)
	if err != nil {
		return nil, err
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

// getConn 获取可用连接，连接已断开则惰性重建。
func (v *Virt) getConn() (*libvirt.Libvirt, error) {
	l, err := v.Connect()
	if err != nil {
		return nil, err
	}
	// 简单探活：libvirt 连接断开时，后续调用会返回错误并触发 Reset。
	return l, nil
}