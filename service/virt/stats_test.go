package virt

import (
	"math"
	"testing"
)

// TestDiffRate 锁死字节速率差分的边界行为。
//
// 风险点：diffRate 喂的是仪表盘「虚拟机明细」的磁盘/网络速率曲线（GetDomainStats）。
// 它吃的是 libvirt 的**累计计数器**，做两次采样差分：
//   - 计数器回绕或下降（VM 重建、virtio 统计重置）若不归零，会算出天文数字速率；
//   - Δt<=0（同一毫秒内两次采样）不设防则 float→uint64 遇到 +Inf 属未定义转换，
//     不同平台给出不同值（可能是 0，也可能是 2^63），曲线会突然刺一根天柱。
//
// 这里逐条断言归零契约，防止将来为「支持更多指标」把归零判断删掉。
func TestDiffRate(t *testing.T) {
	tests := []struct {
		name      string
		cur, prev uint64
		dt        float64
		want      uint64
	}{
		{"正常差分（1s 涨 1024B）", 1024, 0, 1, 1024},
		{"正常差分（2s 涨 2048B → 1024B/s）", 2048, 1024, 2, 512},
		{"计数器下降（回绕/重建）归零", 100, 200, 1, 0},
		{"无变化归零", 100, 100, 1, 0},
		{"不足 1B/s 截断为 0", 1, 0, 3, 0},
		{"dt=0 归零（防 +Inf 未定义转换）", 5, 0, 0, 0},
		{"dt<0 归零", 5, 0, -1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := diffRate(tt.cur, tt.prev, tt.dt); got != tt.want {
				t.Errorf("diffRate(%d, %d, %v) 期望 %d，实际 %d", tt.cur, tt.prev, tt.dt, tt.want, got)
			}
		})
	}
}

// TestDiffCpuPercent 锁死 CPU 占用百分比的计算与归零。
//
// 风险点：cpuPercent = Δcputime(ns) / (hostCPUs × Δt × 1e9) × 100。
// 三处除法都可能出事——Δcputime 回绕、Δt<=0、hostCPUs<=0（runtime.NumCPU 理论上
// 不会为 0，但契约要显式）。任一处不归零，前端 CPU 卡会显示 NaN 或离谱的百分比。
func TestDiffCpuPercent(t *testing.T) {
	const eps = 1e-9
	tests := []struct {
		name      string
		cur, prev uint64
		hostCPUs  int
		dt        float64
		want      float64
	}{
		{"单核跑满 1s = 100%", 1e9, 0, 1, 1, 100},
		{"2 核中 1 核跑满 1s = 50%", 1e9, 0, 2, 1, 50},
		{"2 核全跑满 1s = 100%", 2e9, 0, 2, 1, 100},
		{"单核跑满 2s = 100%", 2e9, 0, 1, 2, 100},
		{"半核占用 = 50%", 5e8, 0, 1, 1, 50},
		{"计数器下降归零", 0, 1e9, 1, 1, 0},
		{"dt=0 归零", 1e9, 0, 1, 0, 0},
		{"dt<0 归零", 1e9, 0, 1, -1, 0},
		{"hostCPUs=0 归零", 1e9, 0, 0, 1, 0},
		{"hostCPUs<0 归零", 1e9, 0, -1, 1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := diffCpuPercent(tt.cur, tt.prev, tt.hostCPUs, tt.dt)
			if math.Abs(got-tt.want) > eps {
				t.Errorf("diffCpuPercent(%d, %d, %d, %v) 期望 %v，实际 %v",
					tt.cur, tt.prev, tt.hostCPUs, tt.dt, tt.want, got)
			}
		})
	}
}
