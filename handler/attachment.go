package handler

import (
	"mime"
	"strings"

	"github.com/gin-gonic/gin"
)

// setAttachmentHeader 安全地设置下载响应头。
//
// 文件名可能来自虚拟机内的文件（Linux 允许文件名包含换行等控制字符）。
// 直接拼 `attachment; filename="<name>"` 会把这些字符带进响应头，存在
// 响应头注入/浏览器解析异常的风险。这里：
//   - 去掉所有控制字符（<0x20 与 DEL）；
//   - 用 mime.FormatMediaType 生成，非 ASCII 名自动按 RFC 2231 编码。
func setAttachmentHeader(c *gin.Context, name string) {
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" {
		name = "download"
	}
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
}
