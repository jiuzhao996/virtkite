// Package monitor 监控服务发现与告警配套能力。
// 当前提供 Prometheus file_sd 目标文件生成：平台内建的 VM 自动进入 node_exporter 抓取目标，
// 闭环「平台建 VM → VM 内 node_exporter 自动被抓取」，无需手工维护 prometheus.yml。
package monitor

import (
	"strconv"
	"strings"

	"github.com/jiuzhao/vmops/model"
)

// nodeExporterPort VM 内 node_exporter 的默认监听端口（部署约定，非平台分配）。
const nodeExporterPort = "9100"

// GenerateFileSD 把虚拟机列表转换为 Prometheus file_sd 条目（纯函数，可单测）。
//
// 规则：
//   - 只保留 status==running 且 IP 非空的 VM（关机/无 IP 的进不了抓取，写了也是死目标）；
//   - 输出格式为 Prometheus file_sd 约定的
//     [{"targets": ["<ip>:9100"], "labels": {"job": "vm-node", "vm_name": ..., "vm_id": ...}}]；
//   - 同 IP 去重：多台 VM 报同一 IP（典型如 NAT/复用）只生成一个目标，labels 的
//     vm_name/vm_id 用逗号拼接保留全部归属，避免 Prometheus 侧重复抓取同一端点；
//   - 平台自身 exporter（/metrics）不在此列：prometheus.yml 的静态抓取已覆盖，避免双份。
func GenerateFileSD(vms []model.VM) []map[string]interface{} {
	// 按 IP 分组；order 保留首次出现顺序，保证输出对同一输入稳定（利于 diff 与测试）
	grouped := make(map[string][]model.VM)
	var order []string
	for _, vm := range vms {
		if vm.Status != model.VMStatusRunning || vm.IP == "" {
			continue
		}
		if _, ok := grouped[vm.IP]; !ok {
			order = append(order, vm.IP)
		}
		grouped[vm.IP] = append(grouped[vm.IP], vm)
	}

	// make 保证空结果序列化为 [] 而非 null：file_sd 文件里写 null 会被 Prometheus 拒绝
	entries := make([]map[string]interface{}, 0, len(order))
	for _, ip := range order {
		group := grouped[ip]
		names := make([]string, len(group))
		ids := make([]string, len(group))
		for i, vm := range group {
			names[i] = vm.Name
			ids[i] = strconv.FormatUint(uint64(vm.ID), 10)
		}
		entries = append(entries, map[string]interface{}{
			"targets": []string{ip + ":" + nodeExporterPort},
			"labels": map[string]string{
				"job":     "vm-node",
				"vm_name": strings.Join(names, ","),
				"vm_id":   strings.Join(ids, ","),
			},
		})
	}
	return entries
}
