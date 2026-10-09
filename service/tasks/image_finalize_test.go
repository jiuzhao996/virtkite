package tasks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jiuzhao/vmops/model"
)

// TestRegisterFinalizedImage 覆盖固化后登记模板的三态：新建 / 同路径重复拒绝 / 软删记录恢复。
func TestRegisterFinalizedImage(t *testing.T) {
	db := newProvTestDB(t, &model.Image{})
	ctx := &ExecContext{DB: db}
	disk := filepath.Join(t.TempDir(), "tpl.qcow2")
	if err := os.WriteFile(disk, []byte("x"), 0o644); err != nil {
		t.Fatalf("准备磁盘文件失败: %v", err)
	}

	img, err := registerFinalizedImage(ctx, disk, "tpl", "desc", "Rocky Linux 10")
	if err != nil {
		t.Fatalf("首次登记失败: %v", err)
	}
	if !img.IsTemplate || img.Format != "qcow2" || img.Path != disk {
		t.Errorf("登记结果不符: %+v", img)
	}

	// 同路径重复登记 → 拒绝
	if _, err := registerFinalizedImage(ctx, disk, "tpl2", "", ""); err == nil {
		t.Error("同路径重复登记应被拒绝")
	}

	// 软删后重新登记 → 恢复并刷新为模板
	if err := db.Delete(&model.Image{}, img.ID).Error; err != nil {
		t.Fatalf("软删失败: %v", err)
	}
	img2, err := registerFinalizedImage(ctx, disk, "tpl3", "d2", "Ubuntu 24.04")
	if err != nil {
		t.Fatalf("软删后重新登记失败: %v", err)
	}
	if img2.ID != img.ID {
		t.Errorf("应复用原记录 id=%d，got=%d", img.ID, img2.ID)
	}
	if !img2.IsTemplate || img2.Name != "tpl3" || img2.OSVersion != "Ubuntu 24.04" {
		t.Errorf("恢复后应刷新为模板，got=%+v", img2)
	}
}

// TestRegisterFinalizedImageMissing 路径不存在时应报错（不写脏记录）。
func TestRegisterFinalizedImageMissing(t *testing.T) {
	db := newProvTestDB(t, &model.Image{})
	ctx := &ExecContext{DB: db}
	if _, err := registerFinalizedImage(ctx, filepath.Join(t.TempDir(), "nope.qcow2"), "x", "", ""); err == nil {
		t.Error("不存在的路径应报错")
	}
	var n int64
	db.Model(&model.Image{}).Count(&n)
	if n != 0 {
		t.Errorf("失败不应写入记录，got=%d", n)
	}
}

// TestDiskSizeBytes 覆盖文件大小读取（缺失文件返回 0）。
func TestDiskSizeBytes(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.bin")
	if err := os.WriteFile(p, make([]byte, 1234), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := diskSizeBytes(p); got != 1234 {
		t.Errorf("diskSizeBytes = %d, want 1234", got)
	}
	if got := diskSizeBytes(filepath.Join(dir, "missing")); got != 0 {
		t.Errorf("缺失文件应返回 0，got=%d", got)
	}
}

// TestWriteOptimizeScript 覆盖优化脚本落临时文件：内容与可执行权限。
func TestWriteOptimizeScript(t *testing.T) {
	p, err := writeOptimizeScript()
	if err != nil {
		t.Fatalf("写优化脚本失败: %v", err)
	}
	defer func() { _ = os.Remove(p) }()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读回优化脚本失败: %v", err)
	}
	if !strings.Contains(string(b), "qemu-guest-agent") {
		t.Error("优化脚本应含 qemu-guest-agent")
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o755 {
		t.Errorf("权限应为 0755，got=%v", st.Mode().Perm())
	}
}
