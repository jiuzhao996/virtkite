// ansible_targets.go：ansible_run 的目标解析——主机组成员展开、去重、inventory 分组与登录用户。
package tasks

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jiuzhao/vmops/model"
)

// ansibleSSHUser 免密通道的登录用户：必须是平台公钥当初写进谁家目录的那个用户
// （model.VM.AnsibleUser）。历史上这里硬编码 root，而 cloud-init 建机时运维常把 user
// 填成 admin/rocky——公钥在那边却以 root 登录，撞上镜像 root authorized_keys 的
// forced-command 提示（"Please login as the user ..."），提示语污染模块 stdout，
// ansible 报成「Module result deserialization failed」，与真实原因毫不相干。
// 空值回落 root（该字段引入前的存量行）。
func ansibleSSHUser(vm model.VM) string {
	if u := strings.TrimSpace(vm.AnsibleUser); u != "" {
		return u
	}
	return "root"
}

// resolveHostGroupVMIDs 解析主机组成员为 VM id 列表（VMIDs 是 JSON 文本数组）。
func resolveHostGroupVMIDs(ctx *ExecContext, groupID uint) ([]uint, error) {
	var g model.HostGroup
	if err := ctx.DB.First(&g, groupID).Error; err != nil {
		return nil, fmt.Errorf("主机组不存在: %w", err)
	}
	if strings.TrimSpace(g.VMIDs) == "" {
		return nil, nil
	}
	var ids []uint
	if err := json.Unmarshal([]byte(g.VMIDs), &ids); err != nil {
		return nil, fmt.Errorf("主机组「%s」成员数据损坏: %w", g.Name, err)
	}
	return ids, nil
}

// mergeUniqueUint 合并两个 id 切片并去重（保持 a 在前、b 在后的稳定顺序）。
func mergeUniqueUint(a, b []uint) []uint {
	seen := make(map[uint]bool, len(a)+len(b))
	out := make([]uint, 0, len(a)+len(b))
	for _, x := range a {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	for _, x := range b {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// buildAnsibleGroups 生成 inventory 分组段（组名 → 主机名列表）：把本次目标 VM 按其所属
// 主机组归类，供 playbook 用 `hosts: <组名>` 指定目标。只含本次目标里的成员（BuildInventory
// 另有防御性过滤），坏组跳过不阻断。
func buildAnsibleGroups(ctx *ExecContext, vms []model.VM, nameByVM map[uint]string) map[string][]string {
	var groups []model.HostGroup
	if err := ctx.DB.Find(&groups).Error; err != nil || len(groups) == 0 {
		return nil
	}
	targetSet := make(map[uint]bool, len(vms))
	for _, vm := range vms {
		targetSet[vm.ID] = true
	}
	out := make(map[string][]string)
	for _, g := range groups {
		if strings.TrimSpace(g.VMIDs) == "" {
			continue
		}
		var ids []uint
		if err := json.Unmarshal([]byte(g.VMIDs), &ids); err != nil {
			continue
		}
		members := make([]string, 0, len(ids))
		for _, id := range ids {
			if targetSet[id] {
				if hn, ok := nameByVM[id]; ok {
					members = append(members, hn)
				}
			}
		}
		if len(members) > 0 {
			out[g.Name] = members
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// uintSliceParam 取正整数切片参数（targets 等；容忍 json 反序列化出的 float64）。
func uintSliceParam(payload map[string]interface{}, key string) ([]uint, error) {
	raw, ok := payload[key].([]interface{})
	if !ok {
		return nil, fmt.Errorf("参数 %s 缺失或格式非法", key)
	}
	out := make([]uint, 0, len(raw))
	for _, item := range raw {
		switch v := item.(type) {
		case float64:
			if v <= 0 || v != float64(uint(v)) {
				return nil, fmt.Errorf("参数 %s 含非法值 %v", key, v)
			}
			out = append(out, uint(v))
		default:
			return nil, fmt.Errorf("参数 %s 含非法值 %v", key, item)
		}
	}
	return out, nil
}
