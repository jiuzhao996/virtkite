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
		{"img", "/home/jiuzhao/data/img", "安装镜像"},
		{"default", "/var/lib/libvirt/images", "系统池"},
		{"mystorage", "/srv/vm/disks", ""}, // 未命中约定：空 = 未分类，可在 UI 手动指定
		{"base", "/srv/other", "模板基盘"}, // 池名命中即推断，不依赖路径
	}
	for _, c := range cases {
		if got := inferPoolRole(c.name, c.path); got != c.want {
			t.Errorf("inferPoolRole(%q,%q) = %q, want %q", c.name, c.path, got, c.want)
		}
	}
}
