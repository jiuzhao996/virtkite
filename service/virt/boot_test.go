package virt

import (
	"errors"
	"strings"
	"testing"
)

// TestSetBootOrderXML 覆盖「调整启动顺序」的 XML 变换。
//
// 风险点：ISO 建机后 cdrom 在 hd 之前，装完系统重启会再进安装器。
// 这里必须做到「旧 <boot> 全部替换为新顺序」，而不是追加（追加会让两个顺序并存）。
func TestSetBootOrderXML(t *testing.T) {
	got, err := setBootOrderXML(kylinDomainXML, []string{"hd", "cdrom"})
	if err != nil {
		t.Fatalf("变换失败: %v", err)
	}
	hd := strings.Index(got, `dev="hd"`)
	cd := strings.Index(got, `dev="cdrom"`)
	if hd < 0 || cd < 0 {
		t.Fatalf("缺少引导项：hd=%d cdrom=%d\n%s", hd, cd, got)
	}
	if hd > cd {
		t.Errorf("hd 应排在 cdrom 之前：\n%s", got)
	}
	if n := strings.Count(got, "<boot "); n != 2 {
		t.Errorf("引导项数量应为 2，实际 %d（旧顺序未清干净或重复写入）：\n%s", n, got)
	}
	// 非 os 内容不能被破坏
	if !strings.Contains(got, "Kylin.qcow2") || !strings.Contains(got, "Kylin.iso") {
		t.Errorf("磁盘设备被误删：\n%s", got)
	}
}

// TestParseBootOrderXML 覆盖「读取当前启动顺序」——固化前提示"光驱优先"靠它。
//
// 风险点：顺序必须保持 XML 里的**出现顺序**（读成集合就判断不出谁在前）；
// 且与 setBootOrderXML 互为逆操作（改完能读回新顺序）。
func TestParseBootOrderXML(t *testing.T) {
	got := parseBootOrderXML(kylinDomainXML)
	want := []string{"cdrom", "hd"}
	if len(got) != len(want) {
		t.Fatalf("引导项数量 = %d，期望 %d：%v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 项 = %q，期望 %q（必须保持出现顺序）", i, got[i], want[i])
		}
	}

	// 与 SetBootOrder 互为逆操作
	adj, err := setBootOrderXML(kylinDomainXML, []string{"hd", "cdrom"})
	if err != nil {
		t.Fatal(err)
	}
	if g := parseBootOrderXML(adj); len(g) != 2 || g[0] != "hd" || g[1] != "cdrom" {
		t.Errorf("调整后读取 = %v，期望 [hd cdrom]", g)
	}

	// 没写 <boot> 时返回空切片（BIOS 默认探测，不该被当成"光驱优先"）
	if g := parseBootOrderXML("<domain><os><type arch='x86_64'>hvm</type></os></domain>"); len(g) != 0 {
		t.Errorf("无 <boot> 时应返回空，实际 %v", g)
	}
}

func TestSetBootOrderXMLSingleDevice(t *testing.T) {
	got, err := setBootOrderXML(kylinDomainXML, []string{"hd"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, `dev="cdrom"`) {
		t.Errorf("只保留 hd 时不应残留 cdrom 引导项：\n%s", got)
	}
	if n := strings.Count(got, "<boot "); n != 1 {
		t.Errorf("引导项数量应为 1，实际 %d：\n%s", n, got)
	}
}

func TestNormalizeBootDevices(t *testing.T) {
	cases := []struct {
		name    string
		in      []string
		want    []string
		wantErr bool
	}{
		{"正常顺序原样保留", []string{"hd", "cdrom"}, []string{"hd", "cdrom"}, false},
		{"大写与空格归一化", []string{" HD ", "CdRom"}, []string{"hd", "cdrom"}, false},
		{"空列表拒绝", nil, nil, true},
		{"非法设备拒绝", []string{"usb"}, nil, true},
		{"重复设备拒绝", []string{"hd", "hd"}, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeBootDevices(tc.in)
			if tc.wantErr {
				if !errors.Is(err, ErrInvalidBootOrder) {
					t.Fatalf("应返回 ErrInvalidBootOrder，实际 %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("归一化结果错误：期望 %v，实际 %v", tc.want, got)
			}
		})
	}
}

func TestSetBootOrderXMLMissingOS(t *testing.T) {
	if _, err := setBootOrderXML(`<domain><name>x</name></domain>`, []string{"hd"}); err == nil {
		t.Fatal("缺少 <os> 时应报错")
	}
}
