package tasks

import (
	"strings"
	"testing"

	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/virt"
)

// TestBuildDeletePreview 删除预检（A）：用真实 libvirt 域枚举卷与快照；
// DB 传 nil（守卫数据不可用）时验证 fail-safe 方向——全部卷标为「保留」，
// 绝不出现「预览说删、真删却因守卫保留」的反向偏差。
func TestBuildDeletePreview(t *testing.T) {
	v := virt.New()
	names := []string{}
	if domains, err := v.ListAllDomainDiskSources(); err == nil {
		for name := range domains {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		t.Skip("本机无 libvirt 域，跳过")
	}
	// 挑一台有磁盘的域
	var target string
	srcs, _ := v.ListAllDomainDiskSources()
	for _, n := range names {
		if len(srcs[n]) > 0 {
			target = n
			break
		}
	}
	if target == "" {
		t.Skip("无带磁盘的域，跳过")
	}

	vm := model.VM{Name: target}
	pv := BuildDeletePreview(nil, v, vm) // db=nil → 守卫不可用 → 保守保留
	if pv.VMName != target {
		t.Errorf("VMName 应为 %s，实际 %s", target, pv.VMName)
	}
	if len(pv.Volumes) == 0 {
		t.Fatalf("应枚举到至少一个卷（域磁盘源）")
	}
	for _, vol := range pv.Volumes {
		if !vol.Keep {
			t.Errorf("守卫数据不可用时卷 %s 必须标为保留（fail-safe），实际可删", vol.Volume)
		}
		if !strings.Contains(vol.Reason, "守卫数据不可用") {
			t.Errorf("卷 %s 的保留原因应说明守卫不可用，实际 %q", vol.Volume, vol.Reason)
		}
	}
	if pv.DeleteGB != 0 {
		t.Errorf("fail-safe 下删除容量应为 0，实际 %.2f", pv.DeleteGB)
	}
	t.Logf("目标=%s 卷=%d 保留容量=%.2fGB 快照=%d 状态=%s",
		target, len(pv.Volumes), pv.KeepGB, len(pv.Snapshots), pv.RunState)
}

// TestVolumeGuardConsistency 守卫判定与执行器口径一致：非池路径 → 保留。
func TestVolumeGuardConsistency(t *testing.T) {
	g := &VolumeGuard{PoolPath: "/pool", imagePaths: map[string]bool{"/pool/base.img": true}, guardsReady: true}
	cases := []struct{ src, wantSub string }{
		{"/elsewhere/foo.qcow2", "不在存储池"},
		{"/pool/base.img", "共享基镜像"},
		{"/pool/vm.qcow2", ""},
	}
	for _, c := range cases {
		got := g.KeepReason(c.src)
		if c.wantSub == "" && got != "" {
			t.Errorf("%s 应可删，实际保留原因 %q", c.src, got)
		}
		if c.wantSub != "" && !strings.Contains(got, c.wantSub) {
			t.Errorf("%s 保留原因应含 %q，实际 %q", c.src, c.wantSub, got)
		}
	}
	// 守卫数据不可用：任何卷都保留
	bad := &VolumeGuard{guardsReady: false}
	if r := bad.KeepReason("/pool/x.qcow2"); !strings.Contains(r, "守卫数据不可用") {
		t.Errorf("守卫不可用时应保留，实际 %q", r)
	}
}
