package virt

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/kdomanski/iso9660"
)

// CloudInitSpec cloud-init 配置（创建 VM 时可选）。
// 契约约定该类型定义于本文件（cloudinit.go），DomainSpec 直接引用之（见 spec.go）。
type CloudInitSpec struct {
	Hostname string `json:"hostname,omitempty"`
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
	SSHKey   string `json:"ssh_key,omitempty"`
	// SSHKeys 多公钥注入（P4-S4 免密渐进）：平台 ansible 公钥与用户自备公钥并存
	SSHKeys []string `json:"ssh_keys,omitempty"`
	NetMode string   `json:"net_mode,omitempty"` // dhcp / static
	IP      string   `json:"ip,omitempty"`
	Gateway string   `json:"gateway,omitempty"`
	DNS     []string `json:"dns,omitempty"`
}

// ===== user-data / meta-data / network-config 的结构化定义 =====
// 三个文件都是 YAML，全部走 yaml.Marshal 序列化：换行、冒号、# 等危险字符由库
// 负责加引号或转块标量，从结构上杜绝「往字段里塞换行注入 runcmd 顶层键」
// （原 fmt.Fprintf 手拼时代的注入面，注入用例见 cloudinit_test.go）。
// 字段声明顺序即输出顺序，与旧手拼版保持一致。

// chpasswdConfig 口令首登不强制改（实验平台直接可用）。
type chpasswdConfig struct {
	Expire bool `yaml:"expire"`
}

// writeFileRule cloud-config write_files 单条目。
type writeFileRule struct {
	Path    string `yaml:"path"`
	Content string `yaml:"content"`
}

// userDataCloudConfig user-data 的 #cloud-config 顶层结构（只列平台会写的键）。
type userDataCloudConfig struct {
	User              string          `yaml:"user,omitempty"`
	Password          string          `yaml:"password,omitempty"`
	Chpasswd          *chpasswdConfig `yaml:"chpasswd,omitempty"`
	SSHPwAuth         bool            `yaml:"ssh_pwauth,omitempty"`
	SSHAuthorizedKeys []string        `yaml:"ssh_authorized_keys,omitempty"`
	WriteFiles        []writeFileRule `yaml:"write_files,omitempty"`
	RunCmd            [][]string      `yaml:"runcmd,omitempty"`
	Hostname          string          `yaml:"hostname"`
}

// metaDataFile NoCloud meta-data：instance-id 是 cloud-init 判断「要不要重新执行
// 初始化」的唯一依据，随主机名变化，克隆机才会重跑一次初始化。
type metaDataFile struct {
	InstanceID    string `yaml:"instance-id"`
	LocalHostname string `yaml:"local-hostname"`
}

// networkConfigFile netplan v2（network-config）。
type networkConfigFile struct {
	Version   int                 `yaml:"version"`
	Ethernets map[string]ethernet `yaml:"ethernets"`
}

// nameserversConfig DNS 地址列表。
type nameserversConfig struct {
	Addresses []string `yaml:"addresses"`
}

// ethernet netplan 单网卡（id0 固定名，与旧手拼版一致）。
type ethernet struct {
	DHCP4       bool               `yaml:"dhcp4"`
	Addresses   []string           `yaml:"addresses,omitempty"`
	Gateway4    string             `yaml:"gateway4,omitempty"`
	Nameservers *nameserversConfig `yaml:"nameservers,omitempty"`
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

	// user-data：user 与 password 是独立分支（修复旧版「与」关系——只填其一时
	// 另一个也丢：仅密码 → 设到镜像默认用户；仅用户名 → 指定默认用户名并把公钥装到该用户名下）
	ud := userDataCloudConfig{Hostname: hostname}
	if cfg.User != "" {
		ud.User = cfg.User
	}
	if cfg.Password != "" {
		ud.Password = cfg.Password
		ud.Chpasswd = &chpasswdConfig{}
		ud.SSHPwAuth = true
	}
	// 公钥合并成单个 ssh_authorized_keys 列表（YAML 映射里重复键会覆盖，不能分开写）
	keys := make([]string, 0, 1+len(cfg.SSHKeys))
	if cfg.SSHKey != "" {
		keys = append(keys, cfg.SSHKey)
	}
	for _, k := range cfg.SSHKeys {
		if strings.TrimSpace(k) != "" {
			keys = append(keys, strings.TrimSpace(k))
		}
	}
	ud.SSHAuthorizedKeys = keys
	// root 口令登录：Rocky/RHEL 系 sshd 默认 PermitRootLogin prohibit-password——
	// ssh_pwauth 只开全局口令认证，root 仍被拒。实验平台需 root 口令直登
	// （app_install / 设计器应用安装均以 root SSH），显式放开。
	if cfg.User == "root" && cfg.Password != "" {
		ud.WriteFiles = []writeFileRule{{
			Path:    "/etc/ssh/sshd_config.d/40-enable-root-login.conf",
			Content: "PermitRootLogin yes",
		}}
		ud.RunCmd = [][]string{
			{"sed", "-i", `s/^#\?PermitRootLogin.*/PermitRootLogin yes/`, "/etc/ssh/sshd_config"},
			{"systemctl", "restart", "sshd"},
		}
	}
	userData, err := yaml.Marshal(ud)
	if err != nil {
		return nil, fmt.Errorf("生成 user-data 失败: %w", err)
	}

	// meta-data
	metaData, err := yaml.Marshal(metaDataFile{InstanceID: "vmops-" + hostname, LocalHostname: hostname})
	if err != nil {
		return nil, fmt.Errorf("生成 meta-data 失败: %w", err)
	}

	// network-config：static 判定大小写不敏感（旧版裸 == 会把 "STATIC" 静默降级成
	// DHCP，用户配的静态 IP 悄悄丢掉）；缺 IP/网关直接报错，不再拼出语法合法、
	// 语义非法的 netplan（那会让 cloud-init 网络配置整段失效，机器起来一张网卡都不通）
	nc := networkConfigFile{Version: 2, Ethernets: map[string]ethernet{}}
	if strings.EqualFold(cfg.NetMode, "static") {
		if cfg.IP == "" || cfg.Gateway == "" {
			return nil, fmt.Errorf("cloud-init 静态网络模式必须填写 IP 与网关")
		}
		dns := make([]string, 0, len(cfg.DNS))
		for _, d := range cfg.DNS {
			if strings.TrimSpace(d) != "" {
				dns = append(dns, strings.TrimSpace(d))
			}
		}
		if len(dns) == 0 {
			dns = []string{"8.8.8.8"}
		}
		nc.Ethernets["id0"] = ethernet{
			DHCP4:       false,
			Addresses:   []string{cfg.IP + "/24"},
			Gateway4:    cfg.Gateway,
			Nameservers: &nameserversConfig{Addresses: dns},
		}
	} else {
		nc.Ethernets["id0"] = ethernet{DHCP4: true}
	}
	networkConfig, err := yaml.Marshal(nc)
	if err != nil {
		return nil, fmt.Errorf("生成 network-config 失败: %w", err)
	}

	w, err := iso9660.NewWriter()
	if err != nil {
		return nil, fmt.Errorf("初始化 iso9660 写入器失败: %w", err)
	}
	// Cleanup 释放写入器内部资源；源全是内存缓冲（无落盘句柄），失败无下游影响，显式忽略
	defer func() { _ = w.Cleanup() }()

	if err := w.AddFile(bytes.NewReader([]byte("#cloud-config\n"+string(userData))), "user-data"); err != nil {
		return nil, fmt.Errorf("写入 user-data 失败: %w", err)
	}
	if err := w.AddFile(bytes.NewReader(metaData), "meta-data"); err != nil {
		return nil, fmt.Errorf("写入 meta-data 失败: %w", err)
	}
	if err := w.AddFile(bytes.NewReader(networkConfig), "network-config"); err != nil {
		return nil, fmt.Errorf("写入 network-config 失败: %w", err)
	}

	var buf bytes.Buffer
	// 卷标识符 cidata 是 cloud-init 查找 seed 设备的约定标签
	if err := w.WriteTo(&buf, "cidata"); err != nil {
		return nil, fmt.Errorf("生成 seed ISO 失败: %w", err)
	}
	return buf.Bytes(), nil
}
