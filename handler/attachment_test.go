package handler

import (
	"strings"
	"testing"
)

// TestSetAttachmentHeader 下载文件名的响应头注入防护。
//
// 风险点：文件名来自虚拟机内的文件路径（Linux 允许 \n、\r、\x00 出现在文件名里）。
// 直接拼进 Content-Disposition 会把控制字符带进响应头；旧实现只替换了双引号。
func TestSetAttachmentHeader(t *testing.T) {
	t.Run("中文文件名按 RFC 2231 编码", func(t *testing.T) {
		c, rec := newTestContext("/api/vms/1/files/download", nil)
		setAttachmentHeader(c, "系统盘.qcow2")
		got := rec.Header().Get("Content-Disposition")
		if !strings.Contains(got, "filename*=") {
			t.Errorf("非 ASCII 名应使用 filename* 编码，实际 %q", got)
		}
		if strings.ContainsAny(got, "\r\n") {
			t.Errorf("响应头含控制字符: %q", got)
		}
	})

	t.Run("换行与 NUL 被剔除，不产生头注入", func(t *testing.T) {
		c, rec := newTestContext("/api/vms/1/files/download", nil)
		setAttachmentHeader(c, "evil\r\nX-Injected: 1\x00.txt")
		got := rec.Header().Get("Content-Disposition")
		if strings.ContainsAny(got, "\r\n\x00") {
			t.Fatalf("响应头仍含控制字符: %q", got)
		}
		if !strings.Contains(got, "filename=") {
			t.Errorf("应保留 attachment filename，实际 %q", got)
		}
	})

	t.Run("空名回退 download", func(t *testing.T) {
		c, rec := newTestContext("/api/vms/1/files/download", nil)
		setAttachmentHeader(c, " \r\n ")
		got := rec.Header().Get("Content-Disposition")
		if !strings.Contains(got, "download") {
			t.Errorf("空文件名应回退 download，实际 %q", got)
		}
	})
}
