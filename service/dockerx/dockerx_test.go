package dockerx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJSONUnmarshalContainer(t *testing.T) {
	line := `{"ID":"abc123","Names":"web","Image":"nginx:latest","State":"running","Status":"Up 2 hours","Ports":"0.0.0.0:80->80/tcp","CreatedAt":"2026-09-18"}`
	var c Container
	if err := jsonUnmarshal(line, &c); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if c.ID != "abc123" || c.Names != "web" || c.Image != "nginx:latest" || c.State != "running" {
		t.Errorf("字段解析不符: %+v", c)
	}
}

// fakeDocker 在临时目录放一个「记录参数后退出 0」的假 docker，并把该目录置于 PATH 首位，
// 返回记录文件路径。用于免真实 docker 守护进程地断言 CLI 子命令调用。
func fakeDocker(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	logFile := filepath.Join(dir, "args.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + logFile + "\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatalf("写入假 docker 失败: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logFile
}

// TestPauseUnpause 校验 Pause/Unpause 落到 docker pause/unpause 子命令且参数为数组形式
// （CLI 封装约定：禁止 shell 拼接，ID 只能是独立参数）。
func TestPauseUnpause(t *testing.T) {
	logFile := fakeDocker(t)
	d := New()
	if err := d.Pause("abc123"); err != nil {
		t.Fatalf("Pause 失败: %v", err)
	}
	if err := d.Unpause("abc123"); err != nil {
		t.Fatalf("Unpause 失败: %v", err)
	}
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("读取调用记录失败: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "pause abc123") {
		t.Errorf("未调用 docker pause: %q", got)
	}
	if !strings.Contains(got, "unpause abc123") {
		t.Errorf("未调用 docker unpause: %q", got)
	}
}
