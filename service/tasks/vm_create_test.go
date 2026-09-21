package tasks

import "testing"

// diskVolumeName 是新建卷与云镜像增量盘共用的卷名约定（B3a 批次抽出），
// execDeleteVM 的兜底清理与回收站恢复重建都依赖这套名字，约定变了三处必须同步。
func TestDiskVolumeName(t *testing.T) {
	cases := []struct {
		name   string
		vm     string
		i      int
		custom string
		want   string
	}{
		{"首盘默认用 VM 名", "web-01", 0, "", "web-01"},
		{"第二块盘 d2", "web-01", 1, "", "web-01-d2"},
		{"第三块盘 d3", "web-01", 2, "", "web-01-d3"},
		{"自定义卷名优先", "web-01", 0, "data-vol", "data-vol"},
		{"自定义卷名在后续盘同样生效", "web-01", 3, "log-vol", "log-vol"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := diskVolumeName(c.vm, c.i, c.custom); got != c.want {
				t.Errorf("diskVolumeName(%q, %d, %q) = %q，期望 %q", c.vm, c.i, c.custom, got, c.want)
			}
		})
	}
}
