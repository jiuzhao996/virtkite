package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/service/ansible"
)

// withTempPlaybookDir 把 config.DataDir 指到临时目录（playbookDir() 随之重定向），
// 测试结束恢复原值——同包其它测试若依赖默认 DataDir 不受影响。
func withTempPlaybookDir(t *testing.T) string {
	t.Helper()
	old := config.GlobalConfig
	dir := t.TempDir()
	config.GlobalConfig = &config.Config{DataDir: dir}
	t.Cleanup(func() { config.GlobalConfig = old })
	return filepath.Join(dir, "ansible", "playbooks")
}

// writePlaybookDirFixture 造一个目录形态 playbook（web-stack/）。
func writePlaybookDirFixture(t *testing.T, root string) {
	t.Helper()
	files := map[string]string{
		"web-stack/site.yml":                      "# vmops-playbook: name=web-stack | desc=示例\n---\n- hosts: all\n  roles: [nginx]\n",
		"web-stack/roles/nginx/tasks/main.yml":    "---\n- name: t\n  ansible.builtin.command: /bin/true\n",
		"web-stack/roles/nginx/handlers/main.yml": "---\n- name: h\n  ansible.builtin.command: /bin/true\n",
	}
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// jsonCtx 造一个带 JSON body 的测试上下文（PUT/POST 用）。
func jsonCtx(t *testing.T, method, target string, params gin.Params, body interface{}) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, target, bytes.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = params
	return c, rec
}

// TestPlaybookFilesEndpoints 目录形态 playbook 的文件管理端点：清单 / 读 / 写 / 删。
//
// 风险点：这些端点把 HTTP query 里的相对路径直接拼到磁盘（唯一的路径注入面），
// 且目录形态 playbook 的主文件解析错一处就会出现「编辑 A 却写进 B」的静默错位。
func TestPlaybookFilesEndpoints(t *testing.T) {
	root := withTempPlaybookDir(t)
	writePlaybookDirFixture(t, root)
	h := &AnsibleHandler{}

	t.Run("清单递归列出 role 内文件", func(t *testing.T) {
		c, rec := newTestContext("/api/ansible/playbooks/web-stack/files", gin.Params{{Key: "id", Value: "web-stack"}})
		h.ListPlaybookFiles(c)
		if rec.Code != http.StatusOK {
			t.Fatalf("期望 200，实际 %d：%s", rec.Code, rec.Body.String())
		}
		body := decodeBody(t, rec)
		items, _ := body["data"].(map[string]interface{})["items"].([]interface{})
		if len(items) != 3 {
			t.Fatalf("文件数期望 3，实际 %d（%v）", len(items), items)
		}
	})

	t.Run("读文件", func(t *testing.T) {
		c, rec := newTestContext("/api/ansible/playbooks/web-stack/file?path=roles/nginx/tasks/main.yml",
			gin.Params{{Key: "id", Value: "web-stack"}})
		h.ReadPlaybookFile(c)
		if rec.Code != http.StatusOK {
			t.Fatalf("期望 200，实际 %d：%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("路径穿越被拒", func(t *testing.T) {
		for _, rel := range []string{"../../etc/passwd", "../x.yml", "roles/../../../etc/passwd"} {
			c, rec := newTestContext("/api/ansible/playbooks/web-stack/file?path="+rel,
				gin.Params{{Key: "id", Value: "web-stack"}})
			h.ReadPlaybookFile(c)
			if rec.Code == http.StatusOK {
				t.Errorf("越界路径 %q 未被拒", rel)
			}
		}
	})

	t.Run("非法 id 被拒", func(t *testing.T) {
		c, rec := newTestContext("/api/ansible/playbooks/..%2Fetc/files", gin.Params{{Key: "id", Value: "../etc"}})
		h.ListPlaybookFiles(c)
		if rec.Code == http.StatusOK {
			t.Errorf("非法 id 未被拒：%s", rec.Body.String())
		}
	})

	t.Run("写文件（含整目录语法校验）", func(t *testing.T) {
		if _, err := ansible.Detect(); err != nil {
			t.Skipf("宿主机未安装 ansible，跳过：%v", err)
		}
		c, rec := jsonCtx(t, http.MethodPut, "/api/ansible/playbooks/web-stack/file",
			gin.Params{{Key: "id", Value: "web-stack"}},
			map[string]string{"path": "roles/nginx/vars/main.yml", "content": "---\nport: 80\n"})
		h.WritePlaybookFile(c)
		if rec.Code != http.StatusOK {
			t.Fatalf("期望 200，实际 %d：%s", rec.Code, rec.Body.String())
		}
		got, err := os.ReadFile(filepath.Join(root, "web-stack", "roles", "nginx", "vars", "main.yml"))
		if err != nil || string(got) != "---\nport: 80\n" {
			t.Fatalf("文件未按预期落盘: %v %q", err, got)
		}
	})

	t.Run("写非法路径被拒", func(t *testing.T) {
		c, rec := jsonCtx(t, http.MethodPut, "/api/ansible/playbooks/web-stack/file",
			gin.Params{{Key: "id", Value: "web-stack"}},
			map[string]string{"path": "../evil.yml", "content": "x"})
		h.WritePlaybookFile(c)
		if rec.Code == http.StatusOK {
			t.Error("越界写未被拒")
		}
		if _, err := os.Stat(filepath.Join(root, "evil.yml")); err == nil {
			t.Error("越界写实际落盘")
		}
	})

	t.Run("删文件", func(t *testing.T) {
		c, rec := newTestContext("/api/ansible/playbooks/web-stack/file?path=roles/nginx/handlers/main.yml",
			gin.Params{{Key: "id", Value: "web-stack"}})
		h.DeletePlaybookFile(c)
		if rec.Code != http.StatusOK {
			t.Fatalf("期望 200，实际 %d：%s", rec.Code, rec.Body.String())
		}
		if _, err := os.Stat(filepath.Join(root, "web-stack", "roles", "nginx", "handlers", "main.yml")); err == nil {
			t.Error("文件未被删除")
		}
	})
}

// TestListPlaybooksIncludesDir 目录形态 playbook 必须出现在清单里并带 is_dir 标记，
// 否则前端「文件」按钮没有渲染依据（功能等于不可达）。
func TestListPlaybooksIncludesDir(t *testing.T) {
	root := withTempPlaybookDir(t)
	writePlaybookDirFixture(t, root)
	if err := os.WriteFile(filepath.Join(root, "single.yml"), []byte("# vmops-playbook: name=single\n---\n- hosts: all\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 空目录（无任何 yml）应被跳过
	if err := os.MkdirAll(filepath.Join(root, "empty-dir"), 0o755); err != nil {
		t.Fatal(err)
	}

	h := &AnsibleHandler{}
	c, rec := newTestContext("/api/ansible/playbooks", nil)
	h.ListPlaybooks(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d：%s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	items, _ := body["data"].(map[string]interface{})["items"].([]interface{})
	if len(items) != 2 {
		t.Fatalf("清单条目期望 2（single + web-stack），实际 %d：%v", len(items), items)
	}
	byID := map[string]map[string]interface{}{}
	for _, it := range items {
		m := it.(map[string]interface{})
		byID[m["id"].(string)] = m
	}
	if m, ok := byID["web-stack"]; !ok {
		t.Fatal("目录形态 playbook 未出现在清单")
	} else {
		if m["is_dir"] != true {
			t.Errorf("web-stack is_dir 应为 true：%v", m["is_dir"])
		}
		if m["name"] != "web-stack" {
			t.Errorf("头部元数据未解析：%v", m["name"])
		}
	}
	if m, ok := byID["single"]; !ok || m["is_dir"] == true {
		t.Errorf("单文件条目异常：%v", byID["single"])
	}
}

// TestDeletePlaybookDir 目录形态删除必须整目录移除，且不能顺手把同名单文件也删掉。
func TestDeletePlaybookDir(t *testing.T) {
	root := withTempPlaybookDir(t)
	writePlaybookDirFixture(t, root)
	h := &AnsibleHandler{}

	c, rec := newTestContext("/api/ansible/playbooks/web-stack", gin.Params{{Key: "id", Value: "web-stack"}})
	h.DeletePlaybook(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d：%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "web-stack")); err == nil {
		t.Error("目录形态 playbook 未被删除")
	}

	// 不存在的 id → 404（而不是 200 的假成功）
	c2, rec2 := newTestContext("/api/ansible/playbooks/nope", gin.Params{{Key: "id", Value: "nope"}})
	h.DeletePlaybook(c2)
	if rec2.Code != http.StatusNotFound {
		t.Errorf("删除不存在的 playbook 期望 404，实际 %d：%s", rec2.Code, rec2.Body.String())
	}
}
