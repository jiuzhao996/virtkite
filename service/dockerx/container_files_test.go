package dockerx

import "testing"

// TestSafeContainerPath 容器内路径校验：必须是绝对路径、无控制字符。
//
// 风险点：路径最终作为独立参数传给 docker exec（无 shell），不存在命令注入；
// 但 ls 会把以 - 开头的操作数当 flag 解析，故必须以 / 开头，且换行会污染行解析。
func TestSafeContainerPath(t *testing.T) {
	good := []string{"/", "/etc", "/etc/hosts", "/a b/c", "/路径"}
	for _, p := range good {
		if !SafeContainerPath(p) {
			t.Errorf("合法路径 %q 被拒", p)
		}
	}
	bad := []string{"", "etc/hosts", "-la", "./x", "/a\nb", "/a\rb", "/x\x00y"}
	for _, p := range bad {
		if SafeContainerPath(p) {
			t.Errorf("非法路径 %q 未被拒", p)
		}
	}
}

// TestParseContainerLS 解析 ls -la 输出：跳过 total 行与 . / .. 项，
// 正确处理目录/普通文件/符号链接/含空格文件名。
func TestParseContainerLS(t *testing.T) {
	out := "total 24\n" +
		"drwxr-xr-x    2 root     root          4096 Jan  1 00:00 bin\n" +
		"-rw-r--r--    1 root     root           123 Jan  1 00:00 file.txt\n" +
		"lrwxrwxrwx    1 root     root            11 Jan  1 00:00 lib -> usr/lib\n" +
		"drwxr-xr-x    2 root     root          4096 Jan  1 00:00 my dir\n" +
		"drwxr-xr-x    2 root     root          4096 Jan  1 00:00 .\n" +
		"drwxr-xr-x    2 root     root          4096 Jan  1 00:00 ..\n"

	files := parseContainerLS(out)
	if len(files) != 4 {
		t.Fatalf("条目数期望 4（跳过 total 与 . / ..），实际 %d：%+v", len(files), files)
	}

	byName := map[string]ContainerFile{}
	for _, f := range files {
		byName[f.Name] = f
	}

	if bin, ok := byName["bin"]; !ok || !bin.IsDir || bin.Mode != "drwxr-xr-x" {
		t.Errorf("bin 目录解析错误：%+v", bin)
	}
	if f, ok := byName["file.txt"]; !ok || f.IsDir || f.Size != 123 {
		t.Errorf("file.txt 解析错误：%+v", f)
	}
	if l, ok := byName["lib"]; !ok || !l.IsLink || l.Link != "usr/lib" {
		t.Errorf("符号链接解析错误：%+v", l)
	}
	if d, ok := byName["my dir"]; !ok || !d.IsDir {
		t.Errorf("含空格文件名解析错误：%+v", byName)
	}
}

// TestParseContainerLSBusyboxStyle busybox ls 用更少的列间距，仍应正确解析。
func TestParseContainerLSBusyboxStyle(t *testing.T) {
	out := "drwxr-xr-x    1 root     root          4096 Oct 13 10:00 conf\n" +
		"-rw-r--r--    1 root     root            88 Oct 13 10:00 app.ini\n"
	files := parseContainerLS(out)
	if len(files) != 2 {
		t.Fatalf("条目数期望 2，实际 %d", len(files))
	}
	if files[0].Name != "conf" || !files[0].IsDir {
		t.Errorf("conf 解析错误：%+v", files[0])
	}
	if files[1].Name != "app.ini" || files[1].Size != 88 {
		t.Errorf("app.ini 解析错误：%+v", files[1])
	}
}
