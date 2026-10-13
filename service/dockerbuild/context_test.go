package dockerbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jiuzhao/vmops/config"
)

// withTempBuildDir 把 config.DataDir 指到临时目录，BaseDir() 随之重定向。
// 与 handler/docker_build_test.go 的写法一致——BaseDir 是 config.DataPath("docker-builds")，
// 动态读 GlobalConfig，因此测试可以把它指到 t.TempDir() 而不污染仓库 data/。
func withTempBuildDir(t *testing.T) string {
	t.Helper()
	old := config.GlobalConfig
	dir := t.TempDir()
	config.GlobalConfig = &config.Config{DataDir: dir}
	t.Cleanup(func() { config.GlobalConfig = old })
	return filepath.Join(dir, "docker-builds")
}

// TestValidateName 锁死上下文名白名单（作目录名，必须安全）。
//
// 风险点：上下文名会直接作为 data/docker-builds/ 下的目录名，是用户可控字符串。
// 白名单是「safePath 之前」的第一道闸——名字里只要混入 / 或 ..，后续拼路径时就可能
// 落到上下文根之外（在 data/ 下任意建目录）。首字符必须字母/数字，是为了排除
// 以 - . _ 开头被下游 CLI 当成选项/隐藏目录的写法。
func TestValidateName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		valid bool
	}{
		{"单字符数字开头", "a", true},
		{"字母数字混合", "MyCtx009", true},
		{"含下划线点连字符", "a_b.c-d", true},
		{"恰好 64 位（上界）", "a" + strings.Repeat("b", 63), true},
		{"65 位（超上界）", "a" + strings.Repeat("b", 64), false},
		{"空串", "", false},
		{"下划线开头", "_lead", false},
		{"连字符开头", "-lead", false},
		{"点开头（隐藏/相对）", ".lead", false},
		{"含空格", "a b", false},
		{"含斜杠", "a/b", false},
		{"上级目录穿越", "../evil", false},
		{"纯点", "..", false},
		{"非 ASCII（中文）", "中文名", false},
		{"含斜杠的绝对路径", "/etc/cron.d", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateName(tt.input)
			if tt.valid && err != nil {
				t.Errorf("合法名 %q 被拒: %v", tt.input, err)
			}
			if !tt.valid && err == nil {
				t.Errorf("非法名 %q 被放行", tt.input)
			}
		})
	}
}

// TestSafePathTraversal 锁死 safePath 的越界守卫（词法前缀 + 分隔符边界）。
//
// 风险点：safePath 是「读写任意文件」的唯一收口。用户可控的 rel 拼接后必须仍落在
// 上下文目录内。两条历史坑：一是 filepath.Join 会折叠 ../，只做字符前缀判断会被
// /root/ctx/../ctx2 这类兄弟目录绕过（ctx 是 ctx2 的字符串前缀）；二是绝对路径
// 经 Join 会被当作相对段拼到 root 下——必须显式断言它没有逃出。
func TestSafePathTraversal(t *testing.T) {
	base := withTempBuildDir(t)
	root := filepath.Join(base, "ctx")

	tests := []struct {
		name string
		rel  string
		ok   bool
	}{
		{"根", ".", true},
		{"根下文件", "Dockerfile", true},
		{"嵌套文件", "sub/a.txt", true},
		{"多级嵌套", "a/b/c.txt", true},
		{"上级穿越", "../evil", false},
		{"多级上级穿越", "../../etc/passwd", false},
		{"先下后上再越界", "sub/../../evil", false},
		{"兄弟目录名（前缀相同）穿越", "../ctx2/f", false},
		{"绝对路径被收敛进根内", "/etc/passwd", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := safePath("ctx", tt.rel)
			if tt.ok {
				if err != nil {
					t.Fatalf("safePath(ctx, %q) 期望放行，实际报错: %v", tt.rel, err)
				}
				// 无论 rel 怎么写，结果必须落在 root 内（root 自身或 root 的子路径）。
				if got != root && !strings.HasPrefix(got, root+string(os.PathSeparator)) {
					t.Fatalf("safePath(ctx, %q) 逃出上下文：%q 不在 %q 内", tt.rel, got, root)
				}
			} else if err == nil {
				t.Errorf("safePath(ctx, %q) 越界未被拒，返回 %q", tt.rel, got)
			}
		})
	}

	// 非法上下文名必须在 safePath 层就被拒，不能先拼路径再判断。
	if _, err := safePath("../evil", "x"); err == nil {
		t.Error("非法上下文名未在 safePath 被拒")
	}
}

// TestContextCRUDAndFiles 覆盖上下文与文件的增删读写全链路。
//
// 风险点：这一组把「目录存在性/父目录自动创建/1MB 读上限/根目录不可删」等边界
// 逐条钉死。1MB 上限是防「误点一个大文件把进程内存打爆」；根目录不可删是防
// DeleteFile(name, ".") 把整上下文连同后续文件一起抹掉。
func TestContextCRUDAndFiles(t *testing.T) {
	base := withTempBuildDir(t)

	// BaseDir 初始不存在时 List 必须返回空而非报错。
	if list, err := List(); err != nil || len(list) != 0 {
		t.Fatalf("空目录 List 期望 (0, nil)，实际 (%d, %v)", len(list), err)
	}

	if err := Create("ok"); err != nil {
		t.Fatalf("Create 合法名失败: %v", err)
	}
	if err := Create("ok"); err == nil {
		t.Error("同名 Create 未被拒")
	}
	if err := Create("bad/name"); err == nil {
		t.Error("非法名 Create 未被拒")
	}

	if dir, err := ContextDir("ok"); err != nil || dir != filepath.Join(base, "ok") {
		t.Fatalf("ContextDir 返回 (%q, %v)", dir, err)
	}
	if _, err := ContextDir("missing"); err == nil {
		t.Error("不存在的上下文 ContextDir 未报错")
	}

	// 写文件自动建父目录。
	if err := WriteFile("ok", "sub/a.txt", "hello"); err != nil {
		t.Fatalf("WriteFile 嵌套失败: %v", err)
	}
	got, err := ReadFile("ok", "sub/a.txt")
	if err != nil || got != "hello" {
		t.Fatalf("ReadFile 期望 hello，实际 (%q, %v)", got, err)
	}

	// 根下写 Dockerfile，List 应识别 HasDocker=true。
	if err := WriteFile("ok", "Dockerfile", "FROM scratch"); err != nil {
		t.Fatalf("写 Dockerfile 失败: %v", err)
	}
	list, err := List()
	if err != nil || len(list) != 1 {
		t.Fatalf("List 期望 1 个上下文，实际 (%d, %v)", len(list), err)
	}
	if !list[0].HasDocker {
		t.Error("List 未识别根下 Dockerfile")
	}
	if list[0].FileCount != 2 { // Dockerfile + sub/（只列一层）
		t.Errorf("FileCount 期望 2（根下 Dockerfile 与 sub 目录），实际 %d", list[0].FileCount)
	}

	// ListFiles 根：目录带尾部 /，结果按路径排序。
	files, err := ListFiles("ok", ".")
	if err != nil || len(files) != 2 {
		t.Fatalf("ListFiles 期望 2 项，实际 (%d, %v)", len(files), err)
	}
	if files[0].Path != "Dockerfile" || files[1].Path != "sub/" {
		t.Errorf("ListFiles 路径/排序不符：%+v", files)
	}

	// 读目录、读不存在、读超 1MB 均应报错。
	if _, err := ReadFile("ok", "sub"); err == nil {
		t.Error("ReadFile 读到目录未报错")
	}
	if _, err := ReadFile("ok", "nope.txt"); err == nil {
		t.Error("ReadFile 读不存在文件未报错")
	}
	big := strings.Repeat("x", (1<<20)+1)
	if err := WriteFile("ok", "big.txt", big); err != nil {
		t.Fatalf("WriteFile 大文件失败: %v", err)
	}
	if _, err := ReadFile("ok", "big.txt"); err == nil {
		t.Error("ReadFile 超过 1MB 未报错（内存保护失效）")
	}

	// SaveUpload 二进制安全落盘并返回字节数。
	n, err := SaveUpload("ok", "bin/blob.dat", strings.NewReader("payload!"))
	if err != nil || n != int64(len("payload!")) {
		t.Fatalf("SaveUpload 期望 (8, nil)，实际 (%d, %v)", n, err)
	}

	// 根目录不可删、不存在不可删。
	if err := DeleteFile("ok", "."); err == nil {
		t.Error("DeleteFile 删根目录未被拒")
	}
	if err := DeleteFile("ok", "nope"); err == nil {
		t.Error("DeleteFile 删不存在文件未报错")
	}
	if err := DeleteFile("ok", "sub"); err != nil {
		t.Errorf("DeleteFile 删目录失败: %v", err)
	}

	// ListFiles 落到不存在的上下文应报错。
	if _, err := ListFiles("missing", "."); err == nil {
		t.Error("ListFiles 不存在的上下文未报错")
	}

	if err := Remove("ok"); err != nil {
		t.Fatalf("Remove 失败: %v", err)
	}
	if err := Remove("ok"); err == nil {
		t.Error("Remove 不存在的上下文未报错")
	}
}

// TestRelToFallback 覆盖 relTo 无法求相对路径时退回原串的分支。
//
// 风险点：relTo 把绝对路径转成相对上下文的展示路径（分隔符统一 /）。正常都在
// 同一根下不会出错，但一旦传入与 root 分属不同命名空间（一个相对一个绝对）的
// abs，filepath.Rel 会报错——此时必须原样返回而不是返回空串，否则文件列表里
// 会凭空出现一行空路径。
func TestRelToFallback(t *testing.T) {
	if got := relTo("relative-root", "/abs/path"); got != "/abs/path" {
		t.Errorf("relTo 求相对失败时应原样返回，实际 %q", got)
	}
	if got := relTo("/root", "/root/a/b.txt"); got != "a/b.txt" {
		t.Errorf("relTo 正常相对路径期望 a/b.txt，实际 %q", got)
	}
}
