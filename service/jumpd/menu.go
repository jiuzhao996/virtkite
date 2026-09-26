package jumpd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jiuzhao/vmops/model"
)

// 资产菜单纯函数层：过滤/分页/渲染/按键解析全部无 DB、无网络副作用。
// ⚠️ 菜单映射只携带 VM ID——连接前的授权/状态/IP 重验在 server.go 现查现判
// （Review Focus #1：菜单展示到按键选择之间存在时间窗，授权可能已过期）。

// menuPageSize 菜单每页台数（编号 1-9 对应）
const menuPageSize = 9

// menuVM 菜单层只认的白名单字段
type menuVM struct {
	ID   uint
	Name string
	IP   string
}

// filterMenuAssets 按四条件过滤：授权（admin 豁免）∩ running ∩ 有 IP ∩ 已托管凭据。
// 授权判定口径与 handler/vm_grant.go 的 grantedVMIDs 一致（此处拿到的是已算好的集合）；
// 结果按名称排序保证菜单顺序稳定。
func filterMenuAssets(vms []model.VM, authorized map[uint]bool, hasCred map[uint]bool, isAdmin bool) []menuVM {
	out := make([]menuVM, 0, len(vms))
	for _, vm := range vms {
		if vm.Status != model.VMStatusRunning {
			continue
		}
		if strings.TrimSpace(vm.IP) == "" {
			continue
		}
		if !isAdmin && !authorized[vm.ID] {
			continue
		}
		if !hasCred[vm.ID] {
			continue // 未托管 SSH 凭据的机器连不上，不进菜单（fail-closed）
		}
		out = append(out, menuVM{ID: vm.ID, Name: vm.Name, IP: vm.IP})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// menuPage 取第 page 页（1 起）；返回本页条目与总页数
func menuPage(items []menuVM, page int) ([]menuVM, int) {
	pages := (len(items) + menuPageSize - 1) / menuPageSize
	if pages == 0 {
		pages = 1
	}
	if page < 1 {
		page = 1
	}
	if page > pages {
		page = pages
	}
	lo := (page - 1) * menuPageSize
	hi := lo + menuPageSize
	if hi > len(items) {
		hi = len(items)
	}
	return items[lo:hi], pages
}

// renderMenu 渲染菜单文本：「编号. 名称 (IP)」每行一条 + 页码提示 + 操作提示
func renderMenu(items []menuVM, page, pages int) string {
	var b strings.Builder
	b.WriteString("┌─────────────────────────────────────────────┐\n")
	b.WriteString("│  鸢航 VirtKite 资产菜单（仅显示你有权连接的虚拟机）\n")
	b.WriteString("├─────────────────────────────────────────────┤\n")
	shown, _ := menuPage(items, page)
	if len(shown) == 0 {
		b.WriteString("  暂无可连接资产：需运行中、已获取 IP 且已在 Web 端托管 SSH 凭据\n")
	}
	for i, vm := range shown {
		fmt.Fprintf(&b, "  %d. %s (%s)\n", i+1, vm.Name, vm.IP)
	}
	b.WriteString("├─────────────────────────────────────────────┤\n")
	fmt.Fprintf(&b, "  第 %d/%d 页 ｜ 数字选择 ｜ j 下一页 ｜ q 断开退出\n", page, pages)
	b.WriteString("└─────────────────────────────────────────────┘\n")
	return b.String()
}

// menuAction 按键解析结果：select 选中 / next 翻页 / quit 退出 / stay 无效键重显
type menuAction struct {
	kind string
	vmID uint
	page int
}

// handleMenuKey 解析单个按键：'1'-'9' 选中当前页对应行；'j' 翻下页（末页回首页）；
// 'q' 退出；其余键一律 stay（不产生任何拨号目标——连接目标唯一来源是菜单选中项）。
func handleMenuKey(key byte, page, pages int, shown []menuVM) menuAction {
	switch {
	case key >= '1' && key <= '9':
		idx := int(key - '1')
		if idx < len(shown) {
			return menuAction{kind: "select", vmID: shown[idx].ID, page: page}
		}
		return menuAction{kind: "stay", page: page}
	case key == 'j':
		next := page + 1
		if next > pages {
			next = 1
		}
		return menuAction{kind: "next", page: next}
	case key == 'q':
		return menuAction{kind: "quit", page: page}
	}
	return menuAction{kind: "stay", page: page}
}
