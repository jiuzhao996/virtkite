package apps

import (
	"encoding/json"
	"strings"
	"testing"
)

// expectIDs 内置目录必须齐备的应用 ID（应用商店功能的最小集合）。
var expectIDs = []string{
	"nginx", "mysql", "redis", "php", "nodejs",
	"python3", "docker-engine", "wordpress", "portainer", "lnmp",
}

// TestAllCatalogIntegrity 目录完整性：ID 无重复且齐全、元数据非空、脚本非空且满足脚本约定。
func TestAllCatalogIntegrity(t *testing.T) {
	list := All()
	if len(list) == 0 {
		t.Fatal("应用目录为空")
	}
	seen := make(map[string]bool, len(list))
	for _, app := range list {
		if app.ID == "" {
			t.Fatalf("存在空 ID 的应用: %+v", app)
		}
		if seen[app.ID] {
			t.Errorf("应用 ID 重复: %s", app.ID)
		}
		seen[app.ID] = true

		if app.Name == "" {
			t.Errorf("应用 %s 缺少名称", app.ID)
		}
		if app.Desc == "" {
			t.Errorf("应用 %s 缺少描述", app.ID)
		}
		switch app.Category {
		case "web", "database", "cache", "runtime", "cms", "ops":
		default:
			t.Errorf("应用 %s 的分类 %q 不在白名单内", app.ID, app.Category)
		}

		if strings.TrimSpace(app.Detect) == "" {
			t.Errorf("应用 %s 缺少已装检测命令", app.ID)
		}
		if strings.TrimSpace(app.Install) == "" {
			t.Errorf("应用 %s 缺少安装脚本", app.ID)
		}
		// 脚本正文不能含反引号（Go raw string 字面量限制，出现即说明写法有误）
		if strings.Contains(app.Detect+app.Install, "`") {
			t.Errorf("应用 %s 的脚本包含反引号", app.ID)
		}
		// Detect 必须单行：app_install executor 会包一层 `if <Detect>; then ...` 判别已装/未装
		if strings.Contains(app.Detect, "\n") {
			t.Errorf("应用 %s 的 Detect 必须是单行命令", app.ID)
		}
		// 安装脚本统一以 set -e 开头（忽略注释行），任一步失败必须立即退出
		body := strings.TrimSpace(app.Install)
		if !strings.HasPrefix(body, "#") || !strings.Contains(body, "set -e") {
			t.Errorf("应用 %s 的安装脚本缺少 set -e", app.ID)
		}
	}

	for _, id := range expectIDs {
		if !seen[id] {
			t.Errorf("内置目录缺少应用: %s", id)
		}
	}
}

// TestAllNotLeakScripts 目录 JSON 序列化不泄露脚本正文（Detect/Install 为 json:"-"）。
func TestAllNotLeakScripts(t *testing.T) {
	for _, app := range All() {
		b, err := json.Marshal(app)
		if err != nil {
			t.Fatalf("序列化应用 %s 失败: %v", app.ID, err)
		}
		s := string(b)
		if strings.Contains(s, app.Detect) || strings.Contains(s, app.Install) {
			t.Errorf("应用 %s 的目录输出泄露了脚本正文", app.ID)
		}
	}
}

// TestGet 按 ID 查询：命中与未命中。
func TestGet(t *testing.T) {
	app, ok := Get("nginx")
	if !ok {
		t.Fatal("Get(\"nginx\") 应命中")
	}
	if app.ID != "nginx" || app.Name == "" || app.Install == "" {
		t.Errorf("Get 返回的应用字段不完整: %+v", app)
	}

	if _, ok := Get("not-exist"); ok {
		t.Error("Get(\"not-exist\") 不应命中")
	}
	if _, ok := Get(""); ok {
		t.Error("Get(\"\") 不应命中")
	}
}

// TestAllReturnsCopy All 返回拷贝：调用方改动不得污染包内目录。
func TestAllReturnsCopy(t *testing.T) {
	list := All()
	list[0].ID = "tampered"
	if _, ok := Get("tampered"); ok {
		t.Error("All 返回的切片与包内目录共享存储，调用方改动会污染目录")
	}
}
