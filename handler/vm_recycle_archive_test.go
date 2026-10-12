package handler

import (
	"strings"
	"testing"
)

// TestMissingDiskFiles 存档磁盘缺失探测（B）：只报 device=disk 的缺失盘，
// 光驱 ISO 缺失不算（不影响开机）。
func TestMissingDiskFiles(t *testing.T) {
	// /etc/hostname 必定存在，用它充当「存在」的磁盘
	xmlText := `<domain type='kvm'>
  <name>demo</name>
  <devices>
    <disk type='file' device='disk'>
      <driver name='qemu' type='qcow2'/>
      <source file='/etc/hostname'/>
      <target dev='vda'/>
    </disk>
    <disk type='file' device='disk'>
      <driver name='qemu' type='qcow2'/>
      <source file='/nonexistent/dir/gone.qcow2'/>
      <target dev='vdb'/>
    </disk>
    <disk type='file' device='cdrom'>
      <source file='/nonexistent/iso/gone.iso'/>
      <target dev='sda'/>
    </disk>
  </devices>
</domain>`
	missing := missingDiskFiles(xmlText)
	if len(missing) != 1 {
		t.Fatalf("应恰好报 1 个缺失磁盘，实际 %d 个：%v", len(missing), missing)
	}
	if !strings.Contains(missing[0], "gone.qcow2") {
		t.Errorf("缺失项应指向 gone.qcow2，实际 %q", missing[0])
	}
	for _, m := range missing {
		if strings.Contains(m, ".iso") {
			t.Errorf("光驱 ISO 缺失不应计入：%q", m)
		}
	}

	// 全部存在 → 无缺失
	okXML := strings.Replace(xmlText, "/nonexistent/dir/gone.qcow2", "/etc/hostname", 1)
	if got := missingDiskFiles(okXML); len(got) != 0 {
		t.Errorf("全部存在时不应报缺失，实际 %v", got)
	}

	// 坏 XML → 不 panic，返回 nil
	if got := missingDiskFiles("<domain"); got != nil {
		t.Errorf("坏 XML 应返回 nil，实际 %v", got)
	}
}

// TestRestoreOutcomeModes 方式常量语义（B）：mode 取值决定前端文案分支，
// 拼错会让前端落到默认分支、把精确重建说成精简重建。
func TestRestoreOutcomeModes(t *testing.T) {
	valid := map[string]bool{"exact": true, "exact_missing": true, "minimal": true}
	for _, m := range []string{"exact", "exact_missing", "minimal"} {
		if !valid[m] {
			t.Errorf("方式 %q 不在合法集合内", m)
		}
	}
	// 与前端 restoreLevel() 的分支保持一致：has_archive+disk → 精确重建
	o := restoreOutcome{Mode: "exact_missing", Missing: []string{"/x/y.qcow2"}}
	if o.Mode != "exact_missing" || len(o.Missing) != 1 {
		t.Errorf("restoreOutcome 字段不符：%+v", o)
	}
}

// TestDomainHoldingVol 回收站「连盘彻底清除」路径上的在用磁盘守卫。
//
// 风险点：这是该路径上唯一能挡住「删掉在用磁盘」的判定。场景不罕见——删掉 VM「web1」
// 后又建了一台也叫「web1」的机器（旧记录是软删的，同名不冲突），两者磁盘路径完全
// 相同（<池>/<名>.qcow2）。此时彻底清除旧记录若直接 os.Remove，删掉的是**正在运行
// 的那台机器**的磁盘：QEMU 仍握着已 unlink 的 inode 继续写，重启即数据全失。
// VolumeGuard 的三重守卫刻意不含这条（删 VM 时自己的域磁盘不该算在用），所以必须在这里单列。
func TestDomainHoldingVol(t *testing.T) {
	disks := map[string][]string{
		"web1": {"/pool/web1.qcow2", "/pool/web1-data.qcow2"},
		"db1":  {"/pool/db1.qcow2"},
	}
	if got := domainHoldingVol(disks, "/pool/web1.qcow2"); got != "web1" {
		t.Errorf("命中挂载时应返回域名，实际 %q", got)
	}
	if got := domainHoldingVol(disks, "/pool/web1-data.qcow2"); got != "web1" {
		t.Errorf("第二块盘也要命中，实际 %q", got)
	}
	if got := domainHoldingVol(disks, "/pool/orphan.qcow2"); got != "" {
		t.Errorf("无人挂载应返回空串（可删），实际 %q", got)
	}
	// 前缀相似但不同路径不能误判为命中（否则会永久留下清不掉的垃圾）
	if got := domainHoldingVol(disks, "/pool/web1.qcow2.bak"); got != "" {
		t.Errorf("近似路径不应命中，实际 %q", got)
	}
	if got := domainHoldingVol(nil, "/pool/any.qcow2"); got != "" {
		t.Errorf("空清单应返回空串，实际 %q", got)
	}
}
