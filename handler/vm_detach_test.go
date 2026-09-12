package handler

import (
	"strings"
	"testing"
)

// TestDetachVolKeepReason 分离磁盘后删卷守卫的保留原因判定（纯函数）。
// 与 shouldKeepVol / StorageHandler.DeleteVolume 守卫同一立场：宁可留文件，不可损坏共享数据。
func TestDetachVolKeepReason(t *testing.T) {
	src := "/home/jiuzhao/storage/images/node1.qcow2"

	t.Run("无任何引用返回空串可删", func(t *testing.T) {
		if r := detachVolKeepReason(src, nil, nil, nil); r != "" {
			t.Errorf("无引用应可删，得到原因: %q", r)
		}
	})

	t.Run("镜像库登记的共享镜像", func(t *testing.T) {
		r := detachVolKeepReason(src, map[string]bool{src: true}, nil, nil)
		if !contains(r, "镜像库", "共享镜像") {
			t.Errorf("文案缺少关键信息: %q", r)
		}
	})

	t.Run("增量克隆父盘", func(t *testing.T) {
		r := detachVolKeepReason(src, nil, []string{"node1-clone", "node2"}, nil)
		if !contains(r, "增量克隆父盘", "2 个子卷", "node1-clone") {
			t.Errorf("文案缺少关键信息: %q", r)
		}
	})

	t.Run("仍被其他虚拟机挂载", func(t *testing.T) {
		r := detachVolKeepReason(src, nil, nil, []string{"other-vm"})
		if !contains(r, "其他虚拟机挂载", "other-vm") {
			t.Errorf("文案缺少关键信息: %q", r)
		}
	})

	t.Run("守卫优先级镜像库最先", func(t *testing.T) {
		// 三类引用同时命中时只报优先级最高的镜像库原因（对应需求场景 a 优先）
		r := detachVolKeepReason(src, map[string]bool{src: true}, []string{"child"}, []string{"vm-x"})
		if !contains(r, "镜像库") || strings.Contains(r, "增量克隆") {
			t.Errorf("镜像库守卫应最先命中: %q", r)
		}
		r2 := detachVolKeepReason(src, nil, []string{"child"}, []string{"vm-x"})
		if !contains(r2, "增量克隆父盘") || strings.Contains(r2, "挂载") {
			t.Errorf("backing 守卫应先于挂载守卫: %q", r2)
		}
	})

	t.Run("集合为 nil 不 panic", func(t *testing.T) {
		// nil map 读安全、nil slice len 为 0：守卫必须容忍调用方未收集到数据的情形
		if r := detachVolKeepReason("/x/y.qcow2", nil, nil, nil); r != "" {
			t.Errorf("nil 入参应判可删: %q", r)
		}
	})
}
