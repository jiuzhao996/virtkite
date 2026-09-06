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
