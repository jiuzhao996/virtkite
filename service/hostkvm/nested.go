// Package hostkvm 封装宿主机 KVM 内核层能力（当前：嵌套虚拟化开关），
// 供平台在设置页查看/开启「VM 里再跑 VM」所需的 kvm_intel/kvm_amd nested 参数。
//
// 权限现实：写 modprobe.d 与重载内核模块都需 root，故经 sudo -n（与 guestmount 同立场）。
package hostkvm

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// nestedConfPath 持久化配置（重启后仍生效）。
const nestedConfPath = "/etc/modprobe.d/vmops-kvm-nested.conf"

// NestedStatus 嵌套虚拟化状态。
type NestedStatus struct {
	Supported bool   `json:"supported"` // 内核是否加载了 kvm_intel / kvm_amd
	Module    string `json:"module"`    // kvm_intel / kvm_amd
	Enabled   bool   `json:"enabled"`   // 当前是否已开启
	Value     string `json:"value"`     // 参数原始值（Y/N/1/0）
	Persisted bool   `json:"persisted"` // 是否已写入 modprobe.d（重启后自动开启）
	Reason    string `json:"reason"`    // 不可用原因（如未加载 KVM 模块）
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// Detect 探测嵌套虚拟化状态（读 /sys/module/<mod>/parameters/nested）。
func Detect() *NestedStatus {
	mod := ""
	switch {
	case dirExists("/sys/module/kvm_intel"):
		mod = "kvm_intel"
	case dirExists("/sys/module/kvm_amd"):
		mod = "kvm_amd"
	default:
		return &NestedStatus{Supported: false, Reason: "未检测到 kvm_intel / kvm_amd 内核模块（宿主未启用 KVM？）"}
	}
	st := &NestedStatus{Supported: true, Module: mod}
	if b, err := os.ReadFile("/sys/module/" + mod + "/parameters/nested"); err == nil {
		v := strings.TrimSpace(string(b))
		st.Value = v
		st.Enabled = v == "Y" || v == "y" || v == "1"
	}
	if _, err := os.Stat(nestedConfPath); err == nil {
		st.Persisted = true
	}
	return st
}

// sudoRun 以 sudo -n 执行命令（免密 sudo；-n 禁止交互挂死）。
func sudoRun(timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sudo", append([]string{"-n"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return "", fmt.Errorf("%s: %w", msg, err)
		}
		return "", fmt.Errorf("sudo %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

// Set 开启/关闭嵌套虚拟化：写持久化 modprobe.d，并尝试运行时重载模块（需所有 VM 关机）。
// 返回一段人类可读的说明（重载失败不致命——持久化已写入，重启后自动生效）。
func Set(enable bool) (*NestedStatus, string, error) {
	st := Detect()
	if !st.Supported {
		return st, "", fmt.Errorf("宿主未加载 KVM 内核模块，无法设置嵌套虚拟化")
	}
	val := "0"
	if enable {
		val = "1"
	}
	// 1) 持久化（重启后自动生效）
	content := fmt.Sprintf("# 由 vmops 生成：嵌套虚拟化（VM 里再跑 VM）\noptions %s nested=%s\n", st.Module, val)
	if _, err := sudoRun(30*time.Second, "sh", "-c", fmt.Sprintf("printf '%%s' %s > %s", shellQuote(content), nestedConfPath)); err != nil {
		return st, "", fmt.Errorf("写入 modprobe.d 失败: %w", err)
	}
	// 2) 运行时重载（kvm 模块被运行中的 VM 占用时会失败，属预期）
	note := ""
	if _, err := sudoRun(60*time.Second, "modprobe", "-r", st.Module); err != nil {
		note = "已写入持久化配置；但当前有虚拟机在运行，kvm 模块无法卸载，运行时暂未生效——关机后重试或重启宿主机即生效"
	} else {
		if _, err := sudoRun(60*time.Second, "modprobe", st.Module, "nested="+val); err != nil {
			note = "已写入持久化配置；运行时重载失败，重启宿主机后生效"
		} else {
			note = "已生效"
		}
	}
	return Detect(), note, nil
}

// shellQuote 单引号包裹（内容含换行与特殊字符）。
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
