package virt

import "testing"

// TestMemoryToMiB 锁死 libvirt 内存单位到 MiB 的换算表。
//
// 风险点：memoryToMiB 喂 DomainDetail.MemoryMB（导入已有域时的内存展示）。
// libvirt 的 <memory unit="..."> 单位串五花八门（KiB/MiB/GiB 与 KB/MB/GB 混用、
// 大小写不定、缺省即 KiB），任何一档写错都会让「这台机器多少内存」显示错一个量级，
// 而且不报错。用例同时锁住大小写归一与未知单位的兜底（按 KiB 处理，宁可小一个量级
// 也不能 panic 或返回 0）。
//
// 注意 libvirt XML 语义：KiB/MiB/GiB 是 2 的幂、KB/MB/GB 是 10 的幂（1000 进制）。
// 本函数把 "mb" 与 "mib" 同等对待（按 2 的幂），与 libvirt 严格语义略有出入；
// 这里**锁定现状**而非修正——修正会改动线上已导入机器的内存显示，须单独评估。
// 本用例的价值是：任何改动都会让测试失败，逼出一次有意识的决策。
func TestMemoryToMiB(t *testing.T) {
	tests := []struct {
		name string
		v    uint64
		unit string
		want int
	}{
		{"KiB 大写", 1024, "KiB", 1},
		{"kib 小写", 2048, "kib", 2},
		{"KB", 1024, "KB", 1},
		{"kb 小写", 4096, "kb", 4},
		{"空单位按 KiB 处理", 1024, "", 1},
		{"MiB 原值", 1024, "MiB", 1024},
		{"mb 小写", 2048, "mb", 2048},
		{"GiB 乘 1024", 1, "GiB", 1024},
		{"gb 小写", 2, "gb", 2048},
		{"未知单位兜底按 KiB", 1024, "weird", 1},
		{"零值", 0, "MiB", 0},
		{"零值未知单位", 0, "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := memoryToMiB(tt.v, tt.unit); got != tt.want {
				t.Errorf("memoryToMiB(%d, %q) 期望 %d，实际 %d", tt.v, tt.unit, tt.want, got)
			}
		})
	}
}
