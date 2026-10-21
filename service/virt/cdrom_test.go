package virt

import (
	"strings"
	"testing"
)

const kylinDomainXML = `<domain type='kvm'>
  <name>Kylin</name>
  <os>
    <type arch='x86_64' machine='pc-i440fx'>hvm</type>
    <boot dev='cdrom'/>
    <boot dev='hd'/>
  </os>
  <devices>
    <disk type='file' device='cdrom'>
      <driver name='qemu' type='raw'/>
      <source file='/home/jiuzhao/storage/iso/Kylin.iso'/>
      <target dev='hda' bus='ide'/>
      <readonly/>
    </disk>
    <disk type='file' device='disk'>
      <driver name='qemu' type='qcow2'/>
      <source file='/home/jiuzhao/storage/images/Kylin.qcow2'/>
      <target dev='vda' bus='virtio'/>
    </disk>
  </devices>
</domain>`

// TestEjectCDROMXML 覆盖「安装完成后弹出 ISO」的 XML 变换。
//
// 风险点：这是用户实际踩到的坑——ISO 建机后光驱仍挂着安装盘，重启会再次进入
// 安装程序。弹介质只删 <source>、保留空光驱；引导顺序不动（空光驱会被 BIOS 跳过），
// 顺序调整交给 SetBootOrder。
func TestEjectCDROMXML(t *testing.T) {
	got, ejected, err := ejectCDROMXML(kylinDomainXML, "hda")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if !ejected {
		t.Fatal("目标光驱有介质，ejected 应为 true")
	}
	if strings.Contains(got, "Kylin.iso") {
		t.Errorf("ISO 源仍存在：\n%s", got)
	}
	// 数据盘不能被误删
	if !strings.Contains(got, "Kylin.qcow2") {
		t.Errorf("数据盘 source 被误删：\n%s", got)
	}
	// 光驱设备本身保留（空光驱后续可再挂载）
	if !strings.Contains(got, `device="cdrom"`) {
		t.Errorf("cdrom 设备本身应保留：\n%s", got)
	}
	// 引导顺序保持原样：弹介质不是调顺序
	if !strings.Contains(got, `dev="cdrom"`) || !strings.Contains(got, `dev="hd"`) {
		t.Errorf("弹介质不应改动引导顺序：\n%s", got)
	}
}

func TestEjectCDROMXMLNoMedia(t *testing.T) {
	ejectedXML, _, err := ejectCDROMXML(kylinDomainXML, "hda")
	if err != nil {
		t.Fatal(err)
	}
	// 第二次弹出：源已不存在，应返回 ejected=false（handler 据此给 409）
	if _, ejected, err := ejectCDROMXML(ejectedXML, "hda"); err != nil || ejected {
		t.Fatalf("重复弹出应返回 ejected=false，实际 ejected=%v err=%v", ejected, err)
	}
}

func TestEjectCDROMXMLTargetMismatch(t *testing.T) {
	got, ejected, err := ejectCDROMXML(kylinDomainXML, "hdb")
	if err != nil {
		t.Fatal(err)
	}
	if ejected {
		t.Error("target 不匹配时不应判定为已弹出")
	}
	if !strings.Contains(got, "Kylin.iso") {
		t.Errorf("target 不匹配时不应删除其他光驱的介质：\n%s", got)
	}
}
