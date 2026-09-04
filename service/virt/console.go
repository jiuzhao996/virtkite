package virt

import (
	"fmt"
	"io"

	"github.com/digitalocean/go-libvirt"
)

// OpenConsole 通过 libvirt 流式打开域的串口控制台（对应 virsh console，免 IP）。
// in 为写入控制台的输入流（本端 → guest）；out 为控制台输出接收流（guest → 本端，libvirt 会持续写入）。
// dev 为空时连接默认控制台设备。
func (v *Virt) OpenConsole(name, dev string, in io.Reader, out io.Writer) error {
	l, err := v.getConn()
	if err != nil {
		return err
	}
	dom, err := l.DomainLookupByName(name)
	if err != nil {
		return fmt.Errorf("虚拟机 %s 不存在: %w", name, err)
	}

	var devOpt libvirt.OptString
	if dev != "" {
		devOpt = libvirt.OptString{dev}
	}

	flags := uint32(libvirt.DomainConsoleForce)
	if err := l.DomainOpenConsoleBidirectional(dom, devOpt, in, out, flags); err != nil {
		return fmt.Errorf("打开控制台失败: %w", err)
	}
	return nil
}
