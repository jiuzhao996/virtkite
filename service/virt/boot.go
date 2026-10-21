package virt

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ErrInvalidBootOrder 启动顺序参数不合法（handler 据此返回 400 而非 500）。
var ErrInvalidBootOrder = errors.New("启动顺序不合法")

// allowedBootDevices 与 libvirt <boot dev=...> 的合法取值一致。
var allowedBootDevices = map[string]bool{"hd": true, "cdrom": true, "network": true}

// SetBootOrder 设置虚拟机引导顺序（对应 virsh edit 调整 <os> 下的 <boot> 顺序）。
//
// ISO 建机时顺序固定为 cdrom→hd；系统安装完成后若仍让光驱优先，重启会再次进入
// 安装程序。本方法让用户在详情页把 hd 调到首位，只改持久定义（下次启动生效）。
func (v *Virt) SetBootOrder(domain string, devices []string) error {
	normalized, err := normalizeBootDevices(devices)
	if err != nil {
		return err
	}
	raw, err := v.GetDomainXML(domain)
	if err != nil {
		return err
	}
	newXML, err := setBootOrderXML(raw, normalized)
	if err != nil {
		return fmt.Errorf("修改启动顺序失败: %w", err)
	}
	if err := v.UpdateDomainXML(domain, newXML); err != nil {
		return fmt.Errorf("保存启动顺序失败: %w", err)
	}
	return nil
}

// normalizeBootDevices 校验并归一化启动设备列表（小写、去重、白名单）。
func normalizeBootDevices(devices []string) ([]string, error) {
	if len(devices) == 0 {
		return nil, fmt.Errorf("%w: 至少保留一个启动设备", ErrInvalidBootOrder)
	}
	seen := make(map[string]bool, len(devices))
	out := make([]string, 0, len(devices))
	for _, d := range devices {
		d = strings.ToLower(strings.TrimSpace(d))
		if !allowedBootDevices[d] {
			return nil, fmt.Errorf("%w: 不支持的启动设备 %q（仅支持 hd / cdrom / network）", ErrInvalidBootOrder, d)
		}
		if seen[d] {
			return nil, fmt.Errorf("%w: 启动设备 %q 重复", ErrInvalidBootOrder, d)
		}
		seen[d] = true
		out = append(out, d)
	}
	return out, nil
}

// setBootOrderXML 用给定顺序替换 <os> 内的全部 <boot> 元素，其余 XML 原样保留。
// 纯函数便于单测。
func setBootOrderXML(raw string, devices []string) (string, error) {
	dec := xml.NewDecoder(strings.NewReader(raw))
	var out bytes.Buffer
	enc := xml.NewEncoder(&out)

	var (
		inOS      bool
		sawOS     bool
		skipDepth int // >0 表示正在丢弃旧 <boot> 的整个子树
	)

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if skipDepth > 0 {
				skipDepth++
				continue
			}
			if t.Name.Local == "os" {
				inOS = true
				sawOS = true
			}
			if inOS && t.Name.Local == "boot" {
				skipDepth = 1 // 旧顺序整体丢弃，最后统一写成新顺序
				continue
			}
			if err := enc.EncodeToken(t); err != nil {
				return "", err
			}

		case xml.EndElement:
			if skipDepth > 0 {
				skipDepth--
				continue
			}
			if inOS && t.Name.Local == "os" {
				for _, d := range devices {
					boot := xml.StartElement{
						Name: xml.Name{Local: "boot"},
						Attr: []xml.Attr{{Name: xml.Name{Local: "dev"}, Value: d}},
					}
					if err := enc.EncodeToken(boot); err != nil {
						return "", err
					}
					if err := enc.EncodeToken(boot.End()); err != nil {
						return "", err
					}
				}
				inOS = false
			}
			if err := enc.EncodeToken(t); err != nil {
				return "", err
			}

		case xml.CharData:
			if skipDepth > 0 {
				continue
			}
			if err := enc.EncodeToken(t); err != nil {
				return "", err
			}

		case xml.Comment, xml.ProcInst, xml.Directive:
			if skipDepth > 0 {
				continue
			}
			if err := enc.EncodeToken(t); err != nil {
				return "", err
			}
		}
	}

	if !sawOS {
		return "", errors.New("域 XML 缺少 <os> 节点")
	}
	if err := enc.Flush(); err != nil {
		return "", err
	}
	return out.String(), nil
}
