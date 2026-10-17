// 本文件负责 create_vm 的「建机后初始化」：开机 → 等 DHCP 租约回填 IP（含域活性
// 自愈）→ 托管 SSH 凭据，以及托管凭据的密文落库实现。
package tasks

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/secretbox"
	"github.com/jiuzhao/vmops/service/virt"
	"gorm.io/gorm"
)

// taskProvision create_vm 的可选「建机后初始化」块（向导「自动开机并初始化」/ 设计器落地共用）。
// 口令一律收密文（secretbox），executor 解密后用于凭据托管——payload 与日志都不留明文。
type taskProvision struct {
	Start       bool
	WaitIP      bool
	SSHUser     string
	PasswordEnc string
	Salt        string
}

// parseTaskProvision 从 payload 解析 provision 块；缺失或非 map 返回 nil（= 不初始化，行为与历史一致）。
func parseTaskProvision(payload map[string]interface{}) *taskProvision {
	raw, ok := payload["provision"]
	if !ok || raw == nil {
		return nil
	}
	m, ok := raw.(map[string]interface{})
	if !ok || m == nil {
		return nil
	}
	p := &taskProvision{}
	p.Start, _ = boolParam(m, "start")
	p.WaitIP, _ = boolParam(m, "wait_ip")
	p.SSHUser, _ = strParam(m, "ssh_user")
	p.PasswordEnc, _ = strParam(m, "password_enc")
	p.Salt, _ = strParam(m, "salt")
	return p
}

// provisionIPTimeout 等 IP 的单窗口时长（域活性自愈会重置该窗口，最多重启 2 次）。
const provisionIPTimeout = 3 * time.Minute

// runProvision 执行建机后初始化：开机 → 等 IP（DHCP 租约按 MAC 匹配 + 域活性自愈）→ 托管凭据。
// 与设计器原 provisionVM 的编排同源（此处下沉为 create_vm 的一部分，向导/设计器共用）。
func runProvision(ctx *ExecContext, vm *model.VM, p *taskProvision, masterSecret string) error {
	if p.Start {
		if err := ctx.Virt.StartDomain(vm.Name); err != nil {
			return fmt.Errorf("开机失败: %w", err)
		}
		reportProgress(ctx, 75, "已开机，等待系统启动与 IP 分配")
		if err := ctx.DB.Model(&model.VM{}).Where("id = ?", vm.ID).Update("status", model.VMStatusRunning).Error; err != nil {
			log.Printf("[tasks] 开机回写状态失败 vm=%s err=%v", vm.Name, err)
		}
		vm.Status = model.VMStatusRunning
	}
	if p.WaitIP {
		ip, err := waitProvisionIP(ctx, vm)
		if err != nil {
			return err
		}
		if err := ctx.DB.Model(&model.VM{}).Where("id = ?", vm.ID).Update("ip", ip).Error; err != nil {
			return fmt.Errorf("回填虚拟机 IP 失败: %w", err)
		}
		vm.IP = ip
		reportProgress(ctx, 88, "已获取 IP "+ip)
	}
	// 凭据托管：供 ansible_run（凭据通道）/ app_install（use_saved）/ 终端免密复用。
	// 托管失败不阻断（应用安装走显式密文通道不受影响），留痕即可。
	if p.PasswordEnc != "" && p.Salt != "" && p.SSHUser != "" {
		if masterSecret == "" {
			return errors.New("凭据托管未初始化（主密钥未配置）")
		}
		plain, err := secretbox.OpenWithMaster(masterSecret, p.Salt, p.PasswordEnc)
		if err != nil {
			return fmt.Errorf("SSH 口令解密失败: %w", err)
		}
		if err := upsertVMCredential(ctx, masterSecret, vm.ID, p.SSHUser, string(plain)); err != nil {
			log.Printf("[tasks] 凭据托管失败 vm=%s err=%v", vm.Name, err)
		} else {
			reportProgress(ctx, 92, "SSH 口令已自动托管")
		}
	}
	return nil
}

// waitProvisionIP 轮询 DHCP 租约按 MAC 拿到 IP；域中途停掉（启动竞态）自动重启，最多 2 次。
func waitProvisionIP(ctx *ExecContext, vm *model.VM) (string, error) {
	restarts := 0
	deadline := time.Now().Add(provisionIPTimeout)
	for time.Now().Before(deadline) {
		var cur model.VM
		if err := ctx.DB.First(&cur, vm.ID).Error; err != nil {
			return "", fmt.Errorf("读取虚拟机记录失败: %w", err)
		}
		// 域活性自愈：首次启动竞态可能让域中途停掉，DB 状态反映不出来，须探活重启
		if state, err := ctx.Virt.GetDomainState(vm.Name); err == nil && state == virt.StatusShutOff && restarts < 2 {
			restarts++
			log.Printf("[tasks] VM %s 域已停止（启动竞态），自动重启第 %d 次", vm.Name, restarts)
			if err := ctx.Virt.StartDomain(vm.Name); err != nil {
				return "", fmt.Errorf("自动重启失败: %w", err)
			}
			deadline = time.Now().Add(provisionIPTimeout)
		}
		if cur.Status == model.VMStatusError {
			return "", errors.New("虚拟机进入 error 状态")
		}
		if cur.IP != "" {
			return cur.IP, nil
		}
		if cur.Status == model.VMStatusRunning && cur.MACAddress != "" {
			if ip := leaseIPByMAC(ctx, cur.MACAddress); ip != "" {
				return ip, nil
			}
		}
		time.Sleep(5 * time.Second)
	}
	return "", fmt.Errorf("虚拟机未获得 IP（DHCP 超时，已自动重启 %d 次）", restarts)
}

// leaseIPByMAC 从 libvirt DHCP 租约按 MAC 找 IP（与 handler.syncVMIPs / 设计器 leaseIPFor 同源）。
func leaseIPByMAC(ctx *ExecContext, mac string) string {
	leases, err := ctx.Virt.ListDHCPLeases()
	if err != nil {
		log.Printf("[tasks] 查询 DHCP 租约失败: %v", err)
		return ""
	}
	mac = strings.ToLower(mac)
	for _, it := range leases {
		if strings.ToLower(it.MAC) == mac {
			return it.IP
		}
	}
	return ""
}

// upsertVMCredential 写入/覆盖 VM 的托管凭据（密文落库）。
// tasks 包禁 import handler（handler 已 import tasks），故此处自实现一份，
// 与 handler 侧保存凭据的语义必须同步（一 VM 一条，唯一索引 vm_id，端口固定 22）。
// 注：handler.VMCredentialHandler 曾有一个同义的 UpsertForVM，实际零调用，已删除。
func upsertVMCredential(ctx *ExecContext, masterSecret string, vmID uint, user, password string) error {
	if user == "" || password == "" {
		return errors.New("托管凭据需要非空的用户名与口令")
	}
	cipherB64, saltHex, err := secretbox.SealWithMaster(masterSecret, password)
	if err != nil {
		return fmt.Errorf("口令加密失败: %w", err)
	}
	var rec model.VMCredential
	err = ctx.DB.Where("vm_id = ?", vmID).First(&rec).Error
	switch {
	case err == nil:
		return ctx.DB.Model(&rec).Updates(map[string]interface{}{
			"user": user, "port": 22, "password_enc": cipherB64, "salt": saltHex,
		}).Error
	case errors.Is(err, gorm.ErrRecordNotFound):
		rec = model.VMCredential{VMID: vmID, User: user, Port: 22, PasswordEnc: cipherB64, Salt: saltHex}
		return ctx.DB.Create(&rec).Error
	default:
		return err
	}
}
