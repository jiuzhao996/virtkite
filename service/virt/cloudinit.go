package virt

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/kdomanski/iso9660"
)

// CloudInitSpec cloud-init 配置（创建 VM 时可选）。
// 契约约定该类型定义于本文件（cloudinit.go），DomainSpec 直接引用之（见 spec.go）。
type CloudInitSpec struct {
	Hostname string   `json:"hostname,omitempty"`
	User     string   `json:"user,omitempty"`
	Password string   `json:"password,omitempty"`
	SSHKey   string   `json:"ssh_key,omitempty"`
	NetMode  string   `json:"net_mode,omitempty"` // dhcp / static
	IP       string   `json:"ip,omitempty"`
	Gateway  string   `json:"gateway,omitempty"`
	DNS      []string `json:"dns,omitempty"`
}

// GenerateSeedISO 生成 cloud-init seed ISO（iso9660 格式），供 cloud-init 识别。
// 根目录含 user-data / meta-data / network-config 三个文件。
// 纯 Go 实现（github.com/kdomanski/iso9660），禁止调用 mkisofs/genisoimage 等系统工具。
func GenerateSeedISO(cfg *CloudInitSpec) ([]byte, error) {
	if cfg == nil {
		cfg = &CloudInitSpec{}
	}
	hostname := cfg.Hostname
	if hostname == "" {
		hostname = "vmops"
	}

	// meta-data
	metaData := fmt.Sprintf("instance-id: vmops-%s\nlocal-hostname: %s\n", hostname, hostname)

	// user-data
	var ub strings.Builder
	ub.WriteString("#cloud-config\n")
	if cfg.User != "" && cfg.Password != "" {
		fmt.Fprintf(&ub, "user: %s\npassword: %s\nchpasswd: {expire: false}\nssh_pwauth: true\n",
			cfg.User, cfg.Password)
	}
	if cfg.SSHKey != "" {
		ub.WriteString("ssh_authorized_keys:\n  - ")
		ub.WriteString(cfg.SSHKey)
		ub.WriteString("\n")
	}
	fmt.Fprintf(&ub, "hostname: %s\n", hostname)

	// network-config
	var nb strings.Builder
	if cfg.NetMode == "static" {
		dns := strings.Join(cfg.DNS, ", ")
		if dns == "" {
			dns = "8.8.8.8"
		}
		fmt.Fprintf(&nb, "version: 2\nethernets:\n  id0:\n    dhcp4: false\n    addresses: [%s/24]\n    gateway4: %s\n    nameservers: {addresses: [%s]}\n",
			cfg.IP, cfg.Gateway, dns)
	} else {
		nb.WriteString("version: 2\nethernets:\n  id0:\n    dhcp4: true\n")
	}

	w, err := iso9660.NewWriter()
	if err != nil {
		return nil, fmt.Errorf("初始化 iso9660 写入器失败: %w", err)
	}
	defer w.Cleanup()

	if err := w.AddFile(bytes.NewReader([]byte(metaData)), "meta-data"); err != nil {
		return nil, fmt.Errorf("写入 meta-data 失败: %w", err)
	}
	if err := w.AddFile(bytes.NewReader([]byte(ub.String())), "user-data"); err != nil {
		return nil, fmt.Errorf("写入 user-data 失败: %w", err)
	}
	if err := w.AddFile(bytes.NewReader([]byte(nb.String())), "network-config"); err != nil {
		return nil, fmt.Errorf("写入 network-config 失败: %w", err)
	}

	var buf bytes.Buffer
	// 卷标识符 cidata 是 cloud-init 查找 seed 设备的约定标签
	if err := w.WriteTo(&buf, "cidata"); err != nil {
		return nil, fmt.Errorf("生成 seed ISO 失败: %w", err)
	}
	return buf.Bytes(), nil
}
