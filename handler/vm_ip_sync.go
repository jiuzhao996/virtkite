package handler

// vms.ip 回填：libvirt DHCP 租约 → vms.ip。
// 调用时机：VM 列表 / 详情请求时惰性触发（syncVMIPs），全局节流 30s 一次，
// 避免列表页高频轮询放大 libvirt RPC。失败静默降级——IP 回填是尽力而为的增强，
// 只影响 Web 终端 SSH 白名单的「精确匹配」分支与前端 IP 展示，不影响主流程。

import (
	"log"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/jiuzhao/vmops/model"
)

const vmIPSyncInterval = 30 * time.Second

var (
	vmIPSyncMu   sync.Mutex
	vmIPSyncLast time.Time
)

// syncVMIPs 从 libvirt DHCP 租约按 MAC 匹配回填 vms.ip（节流，见文件头注释）。
func (h *VMHandler) syncVMIPs() {
	vmIPSyncMu.Lock()
	if time.Since(vmIPSyncLast) < vmIPSyncInterval {
		vmIPSyncMu.Unlock()
		return
	}
	vmIPSyncLast = time.Now()
	vmIPSyncMu.Unlock()

	// 该函数运行在 HTTP 请求链上（gin Recovery 覆盖），recover 兜一层属双保险：
	// DB 回写发生在响应路径上，panic 值进日志即可，不中断请求
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[vm-ip-sync] panic 已恢复: %v\n%s", r, debug.Stack())
		}
	}()

	leases, err := h.Virt.ListDHCPLeases()
	if err != nil {
		log.Printf("[vm-ip-sync] 查询 DHCP 租约失败: %v", err)
		return
	}
	if len(leases) == 0 {
		return
	}
	byMAC := make(map[string]string, len(leases))
	for _, it := range leases {
		if it.MAC != "" {
			byMAC[strings.ToLower(it.MAC)] = it.IP
		}
	}

	var vms []model.VM
	if err := h.DB.Find(&vms).Error; err != nil {
		log.Printf("[vm-ip-sync] 查询虚拟机列表失败: %v", err)
		return
	}
	// QGA 探测逐域 RPC 成本高，且仅在"运行中 + 尚无 IP + DHCP 租约未命中"时才有意义：
	// 典型场景是客户机内手工配置静态 IP（静态地址永远不出现在租约里），此时 guest-agent 是唯一自动途径
	var qgaCandidates []model.VM
	for i := range vms {
		ip, ok := byMAC[strings.ToLower(vms[i].MACAddress)]
		if !ok {
			if vms[i].Status == model.VMStatusRunning && vms[i].IP == "" {
				qgaCandidates = append(qgaCandidates, vms[i])
			}
			continue
		}
		if ip == vms[i].IP {
			continue
		}
		if err := h.DB.Model(&vms[i]).Update("ip", ip).Error; err != nil {
			log.Printf("[vm-ip-sync] 回填 %s IP 失败: %v", vms[i].Name, err)
			continue
		}
		vms[i].IP = ip
	}
	for _, vm := range qgaCandidates {
		ips, err := h.Virt.ListGuestIPs(vm.Name)
		if err != nil || len(ips) == 0 {
			// agent 未安装/未运行属常态，静默跳过；IP 行在前端可由管理员手工维护
			continue
		}
		if err := h.DB.Model(&model.VM{}).Where("id = ?", vm.ID).Update("ip", ips[0]).Error; err != nil {
			log.Printf("[vm-ip-sync] QGA 回填 %s IP 失败: %v", vm.Name, err)
			continue
		}
		log.Printf("[vm-ip-sync] 已通过 guest-agent 回填 %s IP=%s", vm.Name, ips[0])
	}
}
