package handler

import (
	"strings"
	"testing"
)

// TestParseLsOutput `ls -la --time-style=+%s` 输出解析（纯函数）：列布局、符号链接、脏行过滤。
func TestParseLsOutput(t *testing.T) {
	out := strings.Join([]string{
		"total 20",
		"drwxr-xr-x 1 root root  4096 1694000000 .",
		"drwxr-xr-x 1 root root  4096 1694000000 ..",
		"drwxr-xr-x 2 root root  4096 1694000100 mydir",
		"-rw-r--r-- 1 root root   220 1694000200 .bashrc",
		"lrwxrwxrwx 1 root root     7 1694000300 link -> /etc/hosts",
		"-rw-r--r-- 1 root root 12345 1694000400 file with space.txt",
		"",
	}, "\n")

	items := parseLsOutput(out)
	if len(items) != 4 {
		t.Fatalf("应解析出 4 个条目（跳过 total/./..），得到 %d: %+v", len(items), items)
	}

	t.Run("目录条目", func(t *testing.T) {
		it := items[0]
		if it.Name != "mydir" || !it.IsDir || it.Size != 4096 || it.Modified != 1694000100 {
			t.Fatalf("目录条目解析错误: %+v", it)
		}
		if it.Mode != "drwxr-xr-x" {
			t.Fatalf("mode = %q, 期望 drwxr-xr-x", it.Mode)
		}
	})

	t.Run("普通文件", func(t *testing.T) {
		it := items[1]
		if it.Name != ".bashrc" || it.IsDir || it.Size != 220 {
			t.Fatalf("文件条目解析错误: %+v", it)
		}
	})

	t.Run("符号链接只取链接名并判为非目录", func(t *testing.T) {
		it := items[2]
		if it.Name != "link" || it.IsDir {
			t.Fatalf("符号链接解析错误: %+v", it)
		}
	})

	t.Run("文件名含空格", func(t *testing.T) {
		it := items[3]
		if it.Name != "file with space.txt" || it.Size != 12345 {
			t.Fatalf("含空格文件名解析错误: %+v", it)
		}
	})
}

// TestParseLsOutputEmpty 空输出/垃圾行返回非 nil 空切片（JSON 序列化为 [] 而非 null）。
func TestParseLsOutputEmpty(t *testing.T) {
	for name, out := range map[string]string{
		"空输出":     "",
		"仅 total": "total 0\n",
		"垃圾行":     "Permission denied\nshort line\n",
	} {
		t.Run(name, func(t *testing.T) {
			items := parseLsOutput(out)
			if items == nil {
				t.Fatal("必须返回非 nil 切片（保证 JSON 输出 [] 而非 null）")
			}
			if len(items) != 0 {
				t.Fatalf("期望空结果, 得到 %+v", items)
			}
		})
	}
}

// TestParseLsLine 非法行必须安全拒绝（ok=false），不得 panic。
func TestParseLsLine(t *testing.T) {
	bad := []string{
		"",                       // 空
		"short",                  // 列数不足
		"-rw-r--r-- 1 root root", // 列数不足
		"-rw-r--r-- 1 root root abc 1694000000 x", // 大小非数字
		"-rw-r--r-- 1 root root 12 xyz x",         // 时间戳非数字
	}
	for _, line := range bad {
		if _, ok := parseLsLine(line); ok {
			t.Errorf("非法行 %q 不应解析成功", line)
		}
	}
}
