package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// parseID 把路径参数字符串解析为数值型主键，非法（空/非数字/溢出/0）时返回 ok=false。
//
// 为什么必须先解析再交给 GORM：GORM 的 BuildCondition 对「非纯数字字符串且未带占位参数」
// 的内联条件，会把该字符串当作原始 SQL 片段直接拼进 WHERE 子句。因此
//
//	db.First(&vm, c.Param("id"))   // ❌ 未解析，存在 SQL 注入面
//	db.First(&vm, id)              // ✅ 已解析为 uint，走参数化
//
// 前者可被最低权限角色（viewer 能访问的 GET 接口）用于布尔盲注读取任意表。
// 本函数不写任何 HTTP 响应，供 WebSocket 等需要自定义错误通道的场景使用。
func parseID(raw string) (uint, bool) {
	n, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || n == 0 {
		return 0, false
	}
	return uint(n), true
}

// paramID 解析路径参数中的数值型主键（如 :id），失败时已写好 400 响应并 Abort，
// 调用方直接 return 即可。普通 HTTP 处理器统一用本函数，不要再直传 c.Param。
func paramID(c *gin.Context, name string) (uint, bool) {
	id, ok := parseID(c.Param(name))
	if !ok {
		Fail(c, http.StatusBadRequest, "ID 参数非法")
		c.Abort()
		return 0, false
	}
	return id, true
}

// parsePageQuery 解析 ?page=&page_size= 分页参数（线上参数名不变），返回钳制后的
// (page, pageSize)。收敛此前六处三种植皮写法的行为差异：
//   - page 缺省/非法归 1；pageSize 缺省/非法归 defaultPageSize；
//   - pageSize 统一上限 maxPageSize——task/crons 原先无上限（page_size=100000 等于
//     关闭分页全表拉取，DB 一次吐几万行直接拖垮响应），补上与服务层 ListPaged
//     同口径的钳制；session/monitor/audit/notification 保留各自原有上限；
//   - limit 作为 page_size 的旧参数别名始终兼容（语义等价，task/crons 的历史
//     调用方仍在用，其余端点无人传该参数，认了也无副作用）。
func parsePageQuery(c *gin.Context, defaultPageSize, maxPageSize int) (page, pageSize int) {
	page = 1
	if n, err := strconv.Atoi(c.Query("page")); err == nil && n > 0 {
		page = n
	}
	pageSize = defaultPageSize
	sizeRaw := c.Query("page_size")
	if sizeRaw == "" {
		sizeRaw = c.Query("limit") // 旧参数兼容：limit 语义等价 page_size
	}
	if n, err := strconv.Atoi(sizeRaw); err == nil && n > 0 && n <= maxPageSize {
		pageSize = n
	}
	return page, pageSize
}
