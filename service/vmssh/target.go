package vmssh

import (
	"fmt"
	"net"
	"strings"
)

// ValidateTarget 校验一次性 SSH 拨号目标（终端桥 / 文件管理 / 异步任务共用同一把尺子）。
//
// 背景：SSH 拨号参数历史上完全来自浏览器请求体且零校验，任何登录用户都能驱动服务器
// 向任意地址发起 SSH 连接 —— 平台等于免费的跳板机、内网端口扫描器与口令爆破器
// （源 IP 还全算在服务器上）。本函数把外连目标收敛到「平台管理的虚拟机」范围内。
//
// 约束由强到弱：
//  1. 平台已记录该 VM 的 IP（recordedIP 非空）→ 目标必须与之精确一致；
//  2. 未记录 IP（无 guest agent 时的常态）→ 只接受私有网段的 IP 字面量
//     （IPv4 为 RFC1918 三段，IPv6 为 ULA fd00::/8，net.IP.IsPrivate 同时覆盖两者），
//     并排除环回（否则可 SSH 进宿主机自身）、链路本地、组播与未指定地址；
//     不接受主机名，避免 DNS 解析到公网或 DNS rebinding 绕过；
//  3. 端口必须落在 1-65535。
//
// 放在本包而非 handler 层的理由：拨号目标校验是 vmssh 的调用前置条件，
// 异步任务 executor（service/tasks，不能反向依赖 handler）也必须能复用它，
// 否则「提交点校验过」就成了唯一防线，而任务 payload 是持久化数据、可被历史任务带回来。
//
// 返回的错误文案会直接回显到前端（终端 WS 帧 / HTTP 400），因此一律为中文且不含内部细节。
func ValidateTarget(recordedIP, host string, port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("端口不合法（须在 1-65535 之间）")
	}

	// 平台已知该虚拟机地址时，只允许连它自己
	if recorded := strings.TrimSpace(recordedIP); recorded != "" {
		if host != recorded {
			return fmt.Errorf("只能连接该虚拟机自身地址 %s", recorded)
		}
		return nil
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("目标必须是 IP 地址（不支持主机名）")
	}
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return fmt.Errorf("该地址不允许作为终端目标（环回 / 链路本地 / 组播）")
	}
	if !ip.IsPrivate() {
		// net.IP.IsPrivate 对 IPv4 判 RFC1918、对 IPv6 判 fc00::/7（含 fd00::/8 ULA），
		// 文案与实现对齐（此前只写 IPv4 网段，曾误导排查）
		return fmt.Errorf("仅允许 IPv4 私有网段或 IPv6 ULA（fd00::/8）地址")
	}
	return nil
}
