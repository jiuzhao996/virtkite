package handler

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestExportAnsibleDump 造一个含 VM/网络/连线的计划，调导出端点验证 inventory 分组与
// site 骨架（临时调试用；产物跑完即清，不污染真实 playbook 库）。
func TestExportAnsibleDump(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_ = os.MkdirAll(designerDir(), 0o755)
	plan := `{
  "id": "zz-export-test",
  "name": "导出测试",
  "nodes": [
    {"id":"n1","kind":"net","ref":"10.0.0.0/24","name":"default"},
    {"id":"n2","kind":"vm","ref":"7","name":"web1","ssh_user":"root","vcpu":2,"memory_mb":2048,"playbooks":["init-node"]},
    {"id":"n3","kind":"vm","ref":"8","name":"db1","vcpu":1,"memory_mb":1024}
  ],
  "links": [
    {"from":"n1","to":"n2"},
    {"from":"n1","to":"n3"}
  ]
}`
	if err := os.WriteFile(filepath.Join(designerDir(), "zz-export-test.json"), []byte(plan), 0o644); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Remove(filepath.Join(designerDir(), "zz-export-test.json"))
		_ = os.Remove(filepath.Join("data", "ansible", "playbooks", "zz-export-test-site.yml"))
		_ = os.Remove(filepath.Join("data", "ansible", "inventory", "zz-export-test.yml"))
	}()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/designer/plans/zz-export-test/export-ansible", nil)
	c.Params = gin.Params{{Key: "id", Value: "zz-export-test"}}
	NewDesignerHandler(nil, nil, nil).ExportAnsible(c)

	var resp struct {
		Data struct {
			Inventory string `json:"inventory"`
			Site      string `json:"site"`
		} `json:"data"`
	}
	t.Logf("raw body: %s", w.Body.String())
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应解析失败: %v\n%s", err, w.Body.String())
	}
	t.Logf("inventory:\n%s", resp.Data.Inventory)
	t.Logf("site:\n%s", resp.Data.Site)

	inv := resp.Data.Inventory
	for _, want := range []string{"all:", "children:", "default:", "web1:", "db1:", "ansible_user: root"} {
		if !strings.Contains(inv, want) {
			t.Errorf("inventory 缺少 %q", want)
		}
	}
	site := resp.Data.Site
	for _, want := range []string{"vmops-playbook: name=zz-export-test-site", "hosts: web1", "hosts: db1", "init-node"} {
		if !strings.Contains(site, want) {
			t.Errorf("site 缺少 %q", want)
		}
	}
	if _, err := os.Stat(filepath.Join("data", "ansible", "playbooks", "zz-export-test-site.yml")); err != nil {
		t.Errorf("site.yml 未落盘: %v", err)
	}
}
