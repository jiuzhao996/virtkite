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

// renderMenu 渲染菜单文本：「编号. 名称 (IP)」每行一条 + 页码提示 + 操作提示。
// 终端对齐三要素（v3.6 排练发现：原版框线阶梯错位）：
//  1. 每行统一宽度（中文字符显示宽=2，右侧补空格到框宽再补右边框）；
//  2. 全部换行用 \r\n（终端 LF 不回车，纯 \n 会逐行右移成阶梯）；
//  3. 框宽固定 46 显示列，行内容超宽截断（IP 过长不破坏对齐）。
func renderMenu(items []menuVM, page, pages int) string {
	const boxW = 46 // 框体内容宽度（显示列，不含边框字符）
	var b strings.Builder
	// row 拼一行：│ + 内容(补齐/截断到 boxW 显示列) + │ + \r\n
	row := func(content string) {
		w := 0
		var sb strings.Builder
		for _, r := range content {
			rw := 1
			if r > 0x2E80 { // CJK 及全角区显示宽按 2 计
				rw = 2
			}
			if w+rw > boxW { // 超宽截断（显示列口径）
				break
			}
			sb.WriteRune(r)
			w += rw
		}
		for w < boxW {
			sb.WriteByte(' ')
			w++
		}
		b.WriteString("│ " + sb.String() + " │\r\n")
	}
	line := strings.Repeat("─", boxW+2)
	b.WriteString("┌" + line + "┐\r\n")
	row("鸢航 VirtKite 资产菜单（只显示你有权连接的虚拟机）")
	b.WriteString("├" + line + "┤\r\n")
	shown, _ := menuPage(items, page)
	if len(shown) == 0 {
		row("暂无可连接资产：需运行中、有 IP、已托管凭据")
	}
	for i, vm := range shown {
		fmt.Fprintf(&b, "│ %d. %s (%s)", i+1, vm.Name, vm.IP)
		// 手工补齐这一行（内容含用户数据，走同一宽度口径）
		w := 3 + 2 + len(fmt.Sprintf("%d. ", i+1)) + displayWidth(vm.Name) + 2 + displayWidth(vm.IP) + 2
		for w < boxW {
			b.WriteString(" ")
			w++
		}
		b.WriteString(" │\r\n")
	}
	b.WriteString("├" + line + "┤\r\n")
	row(fmt.Sprintf("第 %d/%d 页 ｜ 数字选择 ｜ j 下一页 ｜ q 断开", page, pages))
	b.WriteString("└" + line + "┘\r\n")
	return b.String()
}

// displayWidth 字符串显示宽（CJK=2，其他=1）
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		if r > 0x2E80 {
			w += 2
		} else {
			w++
		}
	}
	return w
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
