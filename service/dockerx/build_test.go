package dockerx

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// TestValidateTag 镜像标签白名单。
//
// 风险点：tag 是唯一直接进 docker build argv 的用户输入。exec.CommandContext 走
// 数组参数（无 shell），所以不存在命令注入；但 `-t --help` / `-t -x` 这类以 - 开头的
// 值会被 docker 当成 flag 解析，等于把构建参数的控制权交出去——必须在 exec 之前挡掉。
func TestValidateTag(t *testing.T) {
	good := []string{"app", "app:1.0", "my-app:v1.2.3", "registry.local/team/app:latest", "a_b/c.d:1"}
	for _, tag := range good {
		if err := ValidateTag(tag); err != nil {
			t.Errorf("合法标签 %q 被拒: %v", tag, err)
		}
	}
	bad := []string{
		"", "-x", "--help", "app:", ":1.0", "app:1.0:2.0", "app 1.0", "app:1.0;rm -rf /",
		strings.Repeat("a", 200),
	}
	for _, tag := range bad {
		if err := ValidateTag(tag); err == nil {
			t.Errorf("非法标签 %q 未被拒", tag)
		}
	}
}

// TestBuildRejectsBadInput 参数校验必须在起进程之前完成（Build 的守卫早于 exec）。
// 用不存在的 docker 二进制也走不到启动那步——校验失败应立即返回校验错误。
func TestBuildRejectsBadInput(t *testing.T) {
	d := New()
	dir := t.TempDir()

	if _, err := d.Build(context.Background(), dir, BuildOpts{Tag: "-x"}); err == nil {
		t.Error("以 - 开头的 tag 应被拒")
	} else if !strings.Contains(err.Error(), "标签") {
		t.Errorf("错误文案应指向标签: %v", err)
	}

	for _, df := range []string{"../outside/Dockerfile", "/etc/passwd", "../../x"} {
		if _, err := d.Build(context.Background(), dir, BuildOpts{Tag: "ok:1", Dockerfile: df}); err == nil {
			t.Errorf("逃出上下文的 Dockerfile 路径 %q 应被拒", df)
		}
	}

	// 合法相对路径（含子目录）应通过校验，只在 docker 不存在时才失败——此处只断言
	// 不是「路径非法」这一类错误。
	if _, err := d.Build(context.Background(), dir, BuildOpts{
		Tag: "ok:1", Dockerfile: filepath.Join("sub", "Dockerfile"), Timeout: 1,
	}); err != nil && strings.Contains(err.Error(), "路径非法") {
		t.Errorf("上下文内的相对路径被误拒: %v", err)
	}
}
