package handler

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/service/ansible"
)

// withTempAnsibleDir 把 config.DataDir 指到临时目录并返回 (playbooks 目录, roles 目录)。
func withTempAnsibleDir(t *testing.T) (string, string) {
	t.Helper()
	old := config.GlobalConfig
	dir := t.TempDir()
	config.GlobalConfig = &config.Config{DataDir: dir}
	t.Cleanup(func() { config.GlobalConfig = old })
	return filepath.Join(dir, "ansible", "playbooks"), filepath.Join(dir, "ansible", "roles")
}

// TestRoleEndpoints 共享 role 库的 CRUD 与文件管理端点。
//
// 风险点：role 名进目录名、文件相对路径进磁盘路径——两个用户可控字符串，
// 名字白名单 + safeDirPath 守卫任一失守就是「在 data/ 下任意写文件」。
func TestRoleEndpoints(t *testing.T) {
	_, roles := withTempAnsibleDir(t)
	h := &AnsibleHandler{}

	t.Run("新建 role（骨架）", func(t *testing.T) {
		c, rec := jsonCtx(t, http.MethodPost, "/api/ansible/roles", nil,
			map[string]string{"name": "base-role", "desc": "基线 role"})
		h.CreateRole(c)
		if rec.Code != http.StatusOK {
			t.Fatalf("期望 200，实际 %d：%s", rec.Code, rec.Body.String())
		}
		if _, err := os.Stat(filepath.Join(roles, "base-role", "tasks", "main.yml")); err != nil {
			t.Fatalf("骨架未生成: %v", err)
		}
	})

	t.Run("非法名被拒", func(t *testing.T) {
		for _, name := range []string{"../evil", "-lead", "a b", ""} {
			c, rec := jsonCtx(t, http.MethodPost, "/api/ansible/roles", nil, map[string]string{"name": name})
			h.CreateRole(c)
			if rec.Code == http.StatusOK {
				t.Errorf("非法 role 名 %q 未被拒", name)
			}
		}
	})

	t.Run("重名被拒", func(t *testing.T) {
		c, rec := jsonCtx(t, http.MethodPost, "/api/ansible/roles", nil, map[string]string{"name": "base-role"})
		h.CreateRole(c)
		if rec.Code == http.StatusOK {
			t.Error("重名新建未被拒")
		}
	})

	t.Run("清单带描述与内置标记", func(t *testing.T) {
		c, rec := newTestContext("/api/ansible/roles", nil)
		h.ListRoles(c)
		if rec.Code != http.StatusOK {
			t.Fatalf("期望 200，实际 %d：%s", rec.Code, rec.Body.String())
		}
		body := decodeBody(t, rec)
		items, _ := body["data"].(map[string]interface{})["items"].([]interface{})
		if len(items) != 1 {
			t.Fatalf("条目数=%d，期望 1：%v", len(items), items)
		}
		m := items[0].(map[string]interface{})
		if m["name"] != "base-role" || m["desc"] != "基线 role" {
			t.Errorf("条目字段不符：%v", m)
		}
		if m["built_in"] != false {
			t.Errorf("临时库里的 role 不该标为内置：%v", m["built_in"])
		}
	})

	t.Run("文件读写与越界拦截", func(t *testing.T) {
		c, rec := jsonCtx(t, http.MethodPut, "/api/ansible/roles/base-role/file",
			gin.Params{{Key: "name", Value: "base-role"}},
			map[string]string{"path": "templates/motd.j2", "content": "hi {{ ansible_hostname }}\n"})
		h.WriteRoleFile(c)
		if rec.Code != http.StatusOK {
			t.Fatalf("期望 200，实际 %d：%s", rec.Code, rec.Body.String())
		}
		c2, rec2 := newTestContext("/api/ansible/roles/base-role/file?path=templates/motd.j2",
			gin.Params{{Key: "name", Value: "base-role"}})
		h.ReadRoleFile(c2)
		if rec2.Code != http.StatusOK {
			t.Fatalf("期望 200，实际 %d：%s", rec2.Code, rec2.Body.String())
		}
		got := decodeBody(t, rec2)["data"].(map[string]interface{})["content"]
		if got != "hi {{ ansible_hostname }}\n" {
			t.Errorf("读回内容不符：%v", got)
		}

		for _, rel := range []string{"../evil.yml", "../../evil.yml", "tasks/../../../evil.yml"} {
			c3, rec3 := newTestContext("/api/ansible/roles/base-role/file?path="+rel,
				gin.Params{{Key: "name", Value: "base-role"}})
			h.ReadRoleFile(c3)
			if rec3.Code == http.StatusOK {
				t.Errorf("越界读 %q 未被拒", rel)
			}
			c4, rec4 := jsonCtx(t, http.MethodPut, "/api/ansible/roles/base-role/file",
				gin.Params{{Key: "name", Value: "base-role"}},
				map[string]string{"path": rel, "content": "x"})
			h.WriteRoleFile(c4)
			if rec4.Code == http.StatusOK {
				t.Errorf("越界写 %q 未被拒", rel)
			}
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(roles), "evil.yml")); err == nil {
			t.Error("越界写实际落盘")
		}
	})

	t.Run("删文件与删 role", func(t *testing.T) {
		c, rec := newTestContext("/api/ansible/roles/base-role/file?path=templates/motd.j2",
			gin.Params{{Key: "name", Value: "base-role"}})
		h.DeleteRoleFile(c)
		if rec.Code != http.StatusOK {
			t.Fatalf("期望 200，实际 %d：%s", rec.Code, rec.Body.String())
		}
		// 删不存在的文件不能假成功
		c2, rec2 := newTestContext("/api/ansible/roles/base-role/file?path=templates/motd.j2",
			gin.Params{{Key: "name", Value: "base-role"}})
		h.DeleteRoleFile(c2)
		if rec2.Code == http.StatusOK {
			t.Error("删除不存在的文件返回了成功")
		}
		// 根目录不可删
		c3, rec3 := newTestContext("/api/ansible/roles/base-role/file?path=.",
			gin.Params{{Key: "name", Value: "base-role"}})
		h.DeleteRoleFile(c3)
		if rec3.Code == http.StatusOK {
			t.Error("删除 role 根目录返回了成功")
		}

		c4, rec4 := newTestContext("/api/ansible/roles/base-role", gin.Params{{Key: "name", Value: "base-role"}})
		h.DeleteRole(c4)
		if rec4.Code != http.StatusOK {
			t.Fatalf("期望 200，实际 %d：%s", rec4.Code, rec4.Body.String())
		}
		if _, err := os.Stat(filepath.Join(roles, "base-role")); err == nil {
			t.Error("role 目录未删除")
		}
		c5, rec5 := newTestContext("/api/ansible/roles/base-role", gin.Params{{Key: "name", Value: "base-role"}})
		h.DeleteRole(c5)
		if rec5.Code == http.StatusOK {
			t.Error("删除不存在的 role 返回了成功")
		}
	})
}

// TestEnsureSeedRolesSetsSharedDir 种子落盘必须同时把库路径注入 ansible 包。
//
// 风险点：这个包级 var 是「保存前 --syntax-check」与「执行时 roles_path」的唯一共同来源，
// 漏注入的表现是「保存时校验不过、执行时却能跑」的割裂——排查起来极绕。
func TestEnsureSeedRolesSetsSharedDir(t *testing.T) {
	_, roles := withTempAnsibleDir(t)
	old := ansible.SharedRolesDir
	t.Cleanup(func() { ansible.SharedRolesDir = old })
	EnsureSeedRoles()
	// 注入的必须是绝对路径：ansible 解析配置里的相对路径是相对「配置文件所在目录」的，
	// 相对值会让共享 role 在执行时指向 <run 目录>/data/ansible/roles（实测踩过）
	if got := ansible.SharedRolesDir; got != roles {
		t.Errorf("共享 role 库路径未注入或未绝对化：got=%q want=%q", got, roles)
	} else if !filepath.IsAbs(got) {
		t.Errorf("共享 role 库路径必须是绝对路径：%q", got)
	}
	if st, err := os.Stat(roles); err != nil || !st.IsDir() {
		t.Errorf("库目录未创建: %v", err)
	}
}
