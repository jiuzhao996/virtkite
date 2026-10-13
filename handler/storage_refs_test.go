package handler

import (
	"strings"
	"testing"
)

// TestVolumeInUseReason 删卷守卫的中文拒绝原因（纯函数）。
func TestVolumeInUseReason(t *testing.T) {
	t.Run("无引用不产生原因", func(t *testing.T) {
		if r := volumeInUseReason("a.qcow2", &VolumeRefs{}); r != "" {
			// inUse 为 false 时 handler 不会调用本函数，但守住边界：不应 panic
			t.Logf("无引用返回: %q", r)
		}
	})
	t.Run("仅虚拟机挂载", func(t *testing.T) {
		r := volumeInUseReason("disk.qcow2", &VolumeRefs{VMs: []string{"vm-1", "vm-2"}})
		if !contains(r, "虚拟机", "vm-1、vm-2", "disk.qcow2", "阻止删除") {
			t.Errorf("文案缺少关键信息: %q", r)
		}
	})
	t.Run("仅镜像库登记", func(t *testing.T) {
		r := volumeInUseReason("base.qcow2", &VolumeRefs{Images: []string{"Ubuntu-22.04"}})
		if !contains(r, "镜像库", "Ubuntu-22.04") {
			t.Errorf("文案缺少关键信息: %q", r)
		}
	})
	t.Run("仅增量克隆父盘", func(t *testing.T) {
		r := volumeInUseReason("parent.qcow2", &VolumeRefs{Children: []string{"child-1", "child-2", "child-3"}})
		if !contains(r, "增量克隆父盘", "3 个子卷", "child-1") {
			t.Errorf("文案缺少关键信息: %q", r)
		}
	})
	t.Run("三类引用齐全", func(t *testing.T) {
		r := volumeInUseReason("x.qcow2", &VolumeRefs{
			VMs:      []string{"vm-a"},
			Images:   []string{"img-b"},
			Children: []string{"c1"},
		})
		for _, kw := range []string{"挂载", "镜像库", "增量克隆", "；"} {
			if !contains(r, kw) {
				t.Errorf("文案缺 %q: %q", kw, r)
			}
		}
	})
}

func contains(s string, kws ...string) bool {
	for _, kw := range kws {
		if !strings.Contains(s, kw) {
			return false
		}
	}
	return true
}

// TestInferPoolRole 池角色推断：按本平台目录约定给缺省角色，未知目录返回空串（前端展示"未分类"）。
func TestInferPoolRole(t *testing.T) {
	cases := []struct {
		name, path, want string
	}{
		{"base", "/home/jiuzhao/storage/base", "模板基盘"},
		{"images", "/home/jiuzhao/storage/images", "系统盘"},
		{"exten", "/home/jiuzhao/storage/exten", "数据盘"},
		{"iso", "/home/jiuzhao/storage/iso", "安装镜像"},
		{"default", "/var/lib/libvirt/images", ""}, // /var/lib 出厂池不再启用，无语义角色
		{"mystorage", "/srv/vm/disks", ""},         // 未命中约定：空 = 未分类，可在 UI 手动指定
		{"base", "/srv/other", "模板基盘"},             // 池名命中即推断，不依赖路径
	}
	for _, c := range cases {
		if got := inferPoolRole(c.name, c.path); got != c.want {
			t.Errorf("inferPoolRole(%q,%q) = %q, want %q", c.name, c.path, got, c.want)
		}
	}
}

// TestInferPoolDesc 池缺省描述的推断。
//
// 风险点：描述是纯平台概念（libvirt 池 XML 没有这个字段），不补缺省时卡片上就是空白——
// 此前只能显示「点击编辑添加描述」诱导用户去点，整行因此成了横跨卡片的误触热区
// （想开抽屉却弹出编辑框）。缺省文案内置后，用户不必为「让卡片好看点」去编辑任何东西。
func TestInferPoolDesc(t *testing.T) {
	cases := []struct {
		name, path   string
		wantNonEmpty bool
	}{
		{"base", "/home/jiuzhao/storage/base", true},
		{"images", "/home/jiuzhao/storage/images", true},
		{"exten", "/home/jiuzhao/storage/exten", true},
		{"iso", "/home/jiuzhao/storage/iso", true},
		// 按路径兜底（池名被改过但目录仍是平台约定）
		{"custom-name", "/data/storage/base", true},
		// 未知池：返回空串，由前端显示占位符（不是错误）
		{"whatever", "/mnt/random", false},
	}
	for _, tc := range cases {
		got := inferPoolDesc(tc.name, tc.path)
		if tc.wantNonEmpty && got == "" {
			t.Errorf("inferPoolDesc(%q,%q) 应有缺省描述", tc.name, tc.path)
		}
		if !tc.wantNonEmpty && got != "" {
			t.Errorf("inferPoolDesc(%q,%q) 未知池应返回空串，实际 %q", tc.name, tc.path, got)
		}
	}
	// 四个已知角色文案必须互不相同（否则推断串味，卡片看起来都一样）
	seen := map[string]bool{}
	for _, n := range []string{"base", "images", "exten", "iso"} {
		d := inferPoolDesc(n, "")
		if seen[d] {
			t.Errorf("池 %s 的描述与其它池重复：%q", n, d)
		}
		seen[d] = true
	}
}
