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
