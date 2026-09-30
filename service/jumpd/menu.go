package jumpd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jiuzhao/vmops/model"
)

// 资产菜单纯函数层：过滤/分页/搜索/渲染/按键解析全部无 DB、无网络副作用。
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

// renderMenu 渲染资产列表（koko 形态对标）：
// 一行提示 + 表格（ID/名称/地址）+ 页脚信息条；搜索词高亮过滤在 loadMenuAssets 之外由
// server 传入 keyword 后走 filterByKeyword。终端对齐三要素（排练教训）：
//  1. 列宽固定（显示列口径，CJK=2），行内容补齐/截断；
//  2. 全部换行 \r\n；
//  3. 无装饰性横幅——首行即提示条（koko 惯例）。
func renderMenu(items []menuVM, page, pages int, keyword string) string {
	var b strings.Builder
	// 提示条（koko 惯例：进来就是可用信息，无产品横幅）
	tip := "提示: 编号登录 ｜ / 搜索 ｜ Esc 清搜索 ｜ p/n 翻页 ｜ q 退出"
	if keyword != "" {
		tip = fmt.Sprintf("搜索 \"%s\"：共 %d 台 ｜ Esc 返回全量", keyword, len(items))
	}
	b.WriteString(tip + "\r\n\r\n")

	// 表格：列宽 = 名称 20 / 地址 16（显示列）
	nameW, ipW := 20, 16
	// 表头
	b.WriteString(fmt.Sprintf("  %-4s %-*s %-*s\r\n", "ID", nameW, "名称", ipW, "地址"))
	b.WriteString("  " + strings.Repeat("-", 4+nameW+ipW+2) + "\r\n")
	shown, _ := menuPage(items, page)
	if len(shown) == 0 {
		b.WriteString("  （无匹配资产：需运行中、有 IP、已托管凭据" + map[bool]string{true: "、匹配搜索", false: ""}[keyword != ""] + "）\r\n")
	}
	for i, vm := range shown {
		b.WriteString(fmt.Sprintf("  %-4d %-*s %-*s\r\n", i+1, nameW, truncateWidth(vm.Name, nameW), ipW, truncateWidth(vm.IP, ipW)))
	}
	// 页脚信息条
	b.WriteString("\r\n")
	b.WriteString(fmt.Sprintf("  页码 %d/%d ｜ 共 %d 台 ｜ [p]上一页 [n]下一页\r\n", page, pages, len(items)))
	return b.String()
}

// truncateWidth 按显示宽截断（CJK=2）
func truncateWidth(s string, max int) string {
	w := 0
	for i, r := range s {
		rw := 1
		if r > 0x2E80 {
			rw = 2
		}
		if w+rw > max {
			return s[:i]
		}
		w += rw
	}
	return s
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

// menuAction 按键解析结果：select 选中 / next 翻下页 / prev 翻上页 / quit 退出 / stay 无效键
type menuAction struct {
	kind string
	vmID uint
	page int
}

// handleMenuKey 解析单个按键：'1'-'9' 选中当前页对应行；'n' 下页（循环）；'p' 上页（循环）；
// 'q' 退出；其余键 stay（不产生拨号目标——连接目标唯一来源是菜单选中项）。
func handleMenuKey(key byte, page, pages int, shown []menuVM) menuAction {
	switch {
	case key >= '1' && key <= '9':
		idx := int(key - '1')
		if idx < len(shown) {
			return menuAction{kind: "select", vmID: shown[idx].ID, page: page}
		}
		return menuAction{kind: "stay", page: page}
	case key == 'n':
		next := page + 1
		if next > pages {
			next = 1
		}
		return menuAction{kind: "next", page: next}
	case key == 'p':
		prev := page - 1
		if prev < 1 {
			prev = pages
		}
		return menuAction{kind: "prev", page: prev}
	case key == 'q':
		return menuAction{kind: "quit", page: page}
	}
	return menuAction{kind: "stay", page: page}
}
