package virt

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ErrNoMedia 目标光驱上没有可弹出的介质（handler 据此返回 409）。
var ErrNoMedia = errors.New("光驱上没有可弹出的介质")

// EjectCDROM 弹出虚拟光驱介质（对应 virsh change-media <domain> <target> --eject --config）。
//
// 只删除 <source>、保留空光驱设备，不改 <boot> 顺序：介质弹出后即使 cdrom 仍排在
// 引导顺序首位，BIOS 也会跳过空光驱进入硬盘，不会再回到安装程序。
// 系统装完后若希望显式把硬盘调为第一启动项，用 SetBootOrder。
//
// 只改持久定义（define）：运行中的域当前仍保留介质，下次重启按新定义走硬盘。
func (v *Virt) EjectCDROM(domain, target string) error {
	raw, err := v.GetDomainXML(domain)
	if err != nil {
		return err
	}
	newXML, ejected, err := ejectCDROMXML(raw, target)
	if err != nil {
		return fmt.Errorf("弹出光驱介质失败（对应 virsh change-media --eject）: %w", err)
	}
	if !ejected {
		return fmt.Errorf("%w（光驱 %s）", ErrNoMedia, target)
	}
	if err := v.UpdateDomainXML(domain, newXML); err != nil {
		return fmt.Errorf("弹出介质后保存配置失败: %w", err)
	}
	return nil
}

// ejectCDROMXML 删除指定光驱的 <source>。纯函数便于单测。
// ejected 表示确实移除了介质（本来就空的光驱返回 false）。
//
// 光驱子树必须先缓冲再判断：真实 XML 里 <source> 排在 <target> 之前，
// 边读边判会因尚未看到 target 而漏删。
func ejectCDROMXML(raw, target string) (string, bool, error) {
	dec := xml.NewDecoder(strings.NewReader(raw))
	var out bytes.Buffer
	enc := xml.NewEncoder(&out)

	var (
		pending      []xml.Token // 正在缓冲的 cdrom <disk> 子树
		pendingDepth int         // 该 disk 的深度
		depth        int         // 当前元素深度（StartElement 后 +1）
		ejected      bool
	)

	flushDisk := func() error {
		matched, hasMedia := false, false
		for _, tok := range pending {
			se, ok := tok.(xml.StartElement)
			if !ok {
				continue
			}
			switch se.Name.Local {
			case "target":
				if attrValue(se, "dev") == target {
					matched = true
				}
			case "source":
				// 空 <source/> 或没有 file 属性表示光驱本来就空，不算可弹出介质
				if attrValue(se, "file") != "" {
					hasMedia = true
				}
			}
		}
		drop := matched && hasMedia
		if drop {
			ejected = true
		}
		if err := writeDiskTokens(enc, pending, drop); err != nil {
			return err
		}
		pending = nil
		return nil
	}

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", false, err
		}

		// 缓冲模式：整棵 cdrom disk 子树原样收集，结束再决定是否删 source
		if pending != nil {
			pending = append(pending, tok)
			switch tok.(type) {
			case xml.StartElement:
				depth++
			case xml.EndElement:
				if depth == pendingDepth {
					depth--
					if err := flushDisk(); err != nil {
						return "", false, err
					}
				} else {
					depth--
				}
			}
			continue
		}

		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if t.Name.Local == "disk" && strings.EqualFold(attrValue(t, "device"), "cdrom") {
				pending = []xml.Token{t}
				pendingDepth = depth
				continue
			}
			if err := enc.EncodeToken(t); err != nil {
				return "", false, err
			}

		case xml.EndElement:
			depth--
			if err := enc.EncodeToken(t); err != nil {
				return "", false, err
			}

		default:
			if err := enc.EncodeToken(tok); err != nil {
				return "", false, err
			}
		}
	}
	if err := enc.Flush(); err != nil {
		return "", false, err
	}
	return out.String(), ejected, nil
}

// writeDiskTokens 输出缓冲的 disk 子树；dropSource 时丢弃其中的 <source> 元素（含子树）。
func writeDiskTokens(enc *xml.Encoder, toks []xml.Token, dropSource bool) error {
	skip := 0
	for _, tok := range toks {
		switch t := tok.(type) {
		case xml.StartElement:
			if skip > 0 {
				skip++
				continue
			}
			if dropSource && t.Name.Local == "source" {
				skip = 1
				continue
			}
			if err := enc.EncodeToken(t); err != nil {
				return err
			}
		case xml.EndElement:
			if skip > 0 {
				skip--
				continue
			}
			if err := enc.EncodeToken(t); err != nil {
				return err
			}
		default:
			if skip > 0 {
				continue
			}
			if err := enc.EncodeToken(tok); err != nil {
				return err
			}
		}
	}
	return nil
}

// attrValue 取元素属性的值（不区分大小写匹配属性名），不存在时返回空串。
func attrValue(e xml.StartElement, name string) string {
	for _, a := range e.Attr {
		if strings.EqualFold(a.Name.Local, name) {
			return a.Value
		}
	}
	return ""
}
