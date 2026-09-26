package jumpd

import (
	"strings"
	"testing"

	"github.com/jiuzhao/vmops/model"
)

// mkVM 造菜单测试用的 VM 行
func mkVM(id uint, name, ip, status string) model.VM {
	return model.VM{ID: id, Name: name, IP: ip, Status: status}
}

func TestFilterMenuAssets(t *testing.T) {
	vms := []model.VM{
		mkVM(1, "node1", "192.168.122.10", model.VMStatusRunning),
		mkVM(2, "node2", "192.168.122.11", model.VMStatusShutOff), // 非运行中
		mkVM(3, "node3", "192.168.122.12", model.VMStatusPaused),
		mkVM(4, "node4", "192.168.122.13", model.VMStatusError),
		mkVM(5, "node5", "", model.VMStatusRunning),          // 无 IP
		mkVM(6, "node6", "192.168.122.15", model.VMStatusRunning),
		mkVM(7, "node7", "192.168.122.16", model.VMStatusRunning),
	}
	auth := map[uint]bool{1: true, 6: true}          // 7 未授权
	cred := map[uint]bool{1: true, 5: true, 7: true} // 6 未托管凭据

	// 非 admin：授权 ∩ running ∩ 有 IP ∩ 有凭据 → 只有 node1
	got := filterMenuAssets(vms, auth, cred, false)
	if len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("非 admin 应只剩 node1，得到 %+v", got)
	}

	// admin：running ∩ 有 IP ∩ 有凭据（不要求授权）→ node1 + node7
	got = filterMenuAssets(vms, auth, cred, true)
	if len(got) != 2 || got[0].ID != 1 || got[1].ID != 7 {
		t.Fatalf("admin 应为 node1+node7，得到 %+v", got)
	}

	// 结果按名称排序稳定
	vms2 := []model.VM{mkVM(9, "zebra", "192.168.122.20", model.VMStatusRunning), mkVM(8, "alpha", "192.168.122.21", model.VMStatusRunning)}
	all := map[uint]bool{8: true, 9: true}
	got = filterMenuAssets(vms2, all, all, true)
	if len(got) != 2 || got[0].Name != "alpha" || got[1].Name != "zebra" {
		t.Fatalf("结果应按名称排序，得到 %+v", got)
	}
}

func TestMenuPage(t *testing.T) {
	// 25 台 → 3 页（9+9+7）
	items := make([]menuVM, 0, 25)
	for i := 1; i <= 25; i++ {
		items = append(items, menuVM{ID: uint(i), Name: "vm-" + string(rune('a'+i%26)) + itoa(i), IP: "192.168.122.1"})
	}
	shown, pages := menuPage(items, 1)
	if pages != 3 {
		t.Fatalf("25 台应 3 页，得到 %d", pages)
	}
	if len(shown) != menuPageSize {
		t.Fatalf("第 1 页应满 %d 台，得到 %d", menuPageSize, len(shown))
	}
	if shown[0].ID != 1 || shown[8].ID != 9 {
		t.Fatalf("第 1 页应是前 9 台，得到首 %d 末 %d", shown[0].ID, shown[8].ID)
	}
	shown, _ = menuPage(items, 3)
	if len(shown) != 7 || shown[6].ID != 25 {
		t.Fatalf("第 3 页应 7 台且末台 ID=25，得到 %d 台", len(shown))
	}

	// 渲染文本：「编号. 名称 (IP)」+ 页码提示
	text := renderMenu(items, 1, 3)
	for _, want := range []string{"1. ", "(192.168.122.1)", "1/3"} {
		if !strings.Contains(text, want) {
			t.Fatalf("菜单文本缺 %q：\n%s", want, text)
		}
	}
}

func TestHandleMenuKey(t *testing.T) {
	items := make([]menuVM, 0, 12)
	for i := 1; i <= 12; i++ {
		items = append(items, menuVM{ID: uint(i), Name: "vm", IP: "192.168.122.1"})
	}
	shown, pages := menuPage(items, 1)

	// 数字 1-9 → select + 对应 VMID
	for n := byte('1'); n <= '9'; n++ {
		act := handleMenuKey(n, 1, pages, shown)
		if act.kind != "select" {
			t.Fatalf("键 %c 应为 select，得到 %q", n, act.kind)
		}
		wantID := shown[n-'1'].ID
		if act.vmID != wantID {
			t.Fatalf("键 %c 应选 VMID=%d，得到 %d", n, wantID, act.vmID)
		}
	}

	// j 翻页、末页回首页；q 退出；非法键 stay
	if act := handleMenuKey('j', 1, pages, shown); act.kind != "next" || act.page != 2 {
		t.Fatalf("j 应翻到第 2 页，得到 %+v", act)
	}
	if act := handleMenuKey('j', pages, pages, shown); act.kind != "next" || act.page != 1 {
		t.Fatalf("末页按 j 应回第 1 页，得到 %+v", act)
	}
	if act := handleMenuKey('q', 1, pages, shown); act.kind != "quit" {
		t.Fatalf("q 应退出，得到 %+v", act)
	}
	for _, bad := range []byte{'0', 'a', 'A', ' '} {
		if act := handleMenuKey(bad, 1, pages, shown); act.kind != "stay" || act.vmID != 0 {
			t.Fatalf("非法键 %q 应 stay 且无目标，得到 %+v", bad, act)
		}
	}
}

// itoa 简单整数转字符串（测试辅助）
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
