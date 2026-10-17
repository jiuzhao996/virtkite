// 本文件负责 create_vm 任务的 payload 子结构解析：磁盘、网卡、cloud-init 的
// 安全反序列化，以及磁盘卷命名规则（新建卷与云镜像增量盘共用）。
package tasks

import (
	"fmt"

	"github.com/jiuzhao/vmops/service/secretbox"
	"github.com/jiuzhao/vmops/service/virt"
)

// diskVolumeName 第 i 块磁盘的卷名（不含扩展名）：自定义名优先；否则首盘 <vm名>、后续 <vm名>-dN（N 从 2 起）。
// 新建卷与云镜像增量盘共用同一命名，删除兜底（execDeleteVM）与回收站恢复重建均按该约定找卷。
func diskVolumeName(name string, i int, custom string) string {
	if custom != "" {
		return custom
	}
	if i > 0 {
		return fmt.Sprintf("%s-d%d", name, i+1)
	}
	return name
}

// createDiskReq 创建 VM 时的磁盘描述。
// 分支优先级：source_image_id（云镜像增量盘，create_gb 可选作子卷容量）> create_gb（新建卷）
// > source（直接引用现有卷/镜像路径）。
type createDiskReq struct {
	CreateGB      int
	Source        string
	SourceImageID uint
	CloudInit     *virt.CloudInitSpec
	VolName       string // 自定义卷名（可空=自动命名 <vm名>[-dN]）
}

// parseTaskCloudInit 从 payload 子项安全解析 cloud-init 配置（非 map 时返回 nil）。
//
// 口令在 handler 边界已加密为 password_enc + salt（明文不落 tasks.payload）；
// 这里用任务主密钥解回 Password 供生成 seed ISO。解密失败必须让任务失败而不是
// 静默产出无密码的 seed（否则用户会拿到一台登不进去的机器）。
func parseTaskCloudInit(v interface{}, masterSecret string) (*virt.CloudInitSpec, error) {
	m, ok := v.(map[string]interface{})
	if !ok || m == nil {
		return nil, nil
	}
	spec := &virt.CloudInitSpec{}
	if s, ok := strParam(m, "hostname"); ok {
		spec.Hostname = s
	}
	if s, ok := strParam(m, "user"); ok {
		spec.User = s
	}
	if s, ok := strParam(m, "password"); ok {
		spec.Password = s
	}
	// 边界加密字段（新路径）：password 为空时解密还原
	if spec.Password == "" {
		if enc, ok := strParam(m, "password_enc"); ok && enc != "" {
			salt, _ := strParam(m, "salt")
			if masterSecret == "" {
				return nil, fmt.Errorf("服务端未配置凭据主密钥，无法解密 cloud-init 口令")
			}
			plain, err := secretbox.OpenWithMaster(masterSecret, salt, enc)
			if err != nil {
				return nil, fmt.Errorf("cloud-init 口令解密失败: %w", err)
			}
			spec.Password = string(plain)
		}
	}
	if s, ok := strParam(m, "ssh_key"); ok {
		spec.SSHKey = s
	}
	if s, ok := strParam(m, "net_mode"); ok {
		spec.NetMode = s
	}
	if s, ok := strParam(m, "ip"); ok {
		spec.IP = s
	}
	if s, ok := strParam(m, "gateway"); ok {
		spec.Gateway = s
	}
	if raw, ok := m["dns"]; ok && raw != nil {
		if arr, ok := raw.([]interface{}); ok {
			dns := make([]string, 0, len(arr))
			for _, e := range arr {
				if s, ok := e.(string); ok && s != "" {
					dns = append(dns, s)
				}
			}
			spec.DNS = dns
		}
	}
	return spec, nil
}

// parseTaskDisks 从 payload 安全解析磁盘列表。
func parseTaskDisks(v interface{}, masterSecret string) ([]createDiskReq, error) {
	arr, ok := v.([]interface{})
	if !ok {
		return nil, nil
	}
	out := make([]createDiskReq, 0, len(arr))
	for _, e := range arr {
		m, ok := e.(map[string]interface{})
		if !ok || m == nil {
			continue
		}
		var d createDiskReq
		if n, ok := intParam(m, "create_gb"); ok {
			d.CreateGB = n
		}
		if s, ok := strParam(m, "source"); ok {
			d.Source = s
		}
		if s, ok := strParam(m, "vol_name"); ok {
			d.VolName = s
		}
		if n, ok := intParam(m, "source_image_id"); ok && n > 0 {
			d.SourceImageID = uint(n)
		}
		if ci, ok := m["cloud_init"]; ok && ci != nil {
			spec, err := parseTaskCloudInit(ci, masterSecret)
			if err != nil {
				return nil, err
			}
			d.CloudInit = spec
		}
		out = append(out, d)
	}
	return out, nil
}

// parseTaskInterfaces 从 payload 安全解析网卡列表。
func parseTaskInterfaces(v interface{}) []virt.InterfaceSpec {
	arr, ok := v.([]interface{})
	if !ok {
		return nil
	}
	out := make([]virt.InterfaceSpec, 0, len(arr))
	for _, e := range arr {
		m, ok := e.(map[string]interface{})
		if !ok || m == nil {
			continue
		}
		var spec virt.InterfaceSpec
		if s, ok := strParam(m, "type"); ok {
			spec.Type = s
		}
		if s, ok := strParam(m, "source"); ok {
			spec.Source = s
		}
		if s, ok := strParam(m, "mac"); ok {
			spec.MAC = s
		}
		if s, ok := strParam(m, "model"); ok {
			spec.Model = s
		}
		out = append(out, spec)
	}
	return out
}
