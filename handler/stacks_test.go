package handler

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/dockerx"
	"github.com/jiuzhao/vmops/service/tasks"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newStackTestContext 构造一个最小可用的 gin 测试上下文（含 Params/role/JSON body）。
func newStackTestContext(method, target, body string, params gin.Params, role string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	if body != "" {
		c.Request = httptest.NewRequest(method, target, strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
	} else {
		c.Request = httptest.NewRequest(method, target, nil)
	}
	c.Params = params
	if role != "" {
		c.Set("role", role)
	}
	return c, w
}

// stackTestDB 内存库 + 栈版本表，并把 DataDir 指向临时目录（避免污染仓库 data/）。
func stackTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("内存库失败: %v", err)
	}
	if err := db.AutoMigrate(&model.StackVersion{}); err != nil {
		t.Fatalf("AutoMigrate 失败: %v", err)
	}
	prev := config.GlobalConfig
	config.GlobalConfig = &config.Config{DataDir: t.TempDir()}
	t.Cleanup(func() { config.GlobalConfig = prev })
	return db
}

// TestStackRollbackValidation 回滚端点的输入校验分支：非管理员/非法 ID/缺版本/跨栈/不存在。
func TestStackRollbackValidation(t *testing.T) {
	db := stackTestDB(t)
	h := &StackHandler{DB: db, Docker: dockerx.New()}

	// 造「已部署」栈 web：部署副本文件存在（handler 用 os.Stat 判是否已部署）。
	dir := config.DataPath("stacks", "web")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := tasks.RecordStackVersion(db, "web", "content-A", "部署", "alice", nil, nil); err != nil {
		t.Fatal(err)
	}
	other, _, err := tasks.RecordStackVersion(db, "other", "content-B", "部署", "bob", nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		role string
		id   string
		body string
		want int
	}{
		{"非管理员 403", "operator", "web", `{"version_id":1}`, 403},
		{"非法栈 ID 400", "admin", "bad id", `{"version_id":1}`, 400},
		{"缺失 version_id 400", "admin", "web", `{}`, 400},
		{"跨栈版本 404", "admin", "web", fmt.Sprintf(`{"version_id":%d}`, other.ID), 404},
		{"不存在的版本 404", "admin", "web", `{"version_id":999999}`, 404},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, w := newStackTestContext("POST", "/api/stacks/"+url.PathEscape(tc.id)+"/rollback", tc.body,
				gin.Params{{Key: "id", Value: tc.id}}, tc.role)
			h.Rollback(c)
			if w.Code != tc.want {
				t.Fatalf("期望 HTTP %d，got %d（body=%s）", tc.want, w.Code, w.Body.String())
			}
		})
	}

	// 未部署栈（DataDir 下无该目录）→ 404
	c, w := newStackTestContext("POST", "/api/stacks/ghost/rollback", `{"version_id":1}`,
		gin.Params{{Key: "id", Value: "ghost"}}, "admin")
	h.Rollback(c)
	if w.Code != 404 {
		t.Fatalf("未部署栈应 404，got %d（body=%s）", w.Code, w.Body.String())
	}
}

// TestStackVersionsEndpoint 版本列表端点：返回本栈历史（版本号降序），非法 ID 拒绝。
func TestStackVersionsEndpoint(t *testing.T) {
	db := stackTestDB(t)
	h := &StackHandler{DB: db, Docker: dockerx.New()}

	if _, _, err := tasks.RecordStackVersion(db, "web", "A", "部署", "alice", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := tasks.RecordStackVersion(db, "web", "B", "升级", "bob", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := tasks.RecordStackVersion(db, "web2", "X", "部署", "carol", nil, nil); err != nil {
		t.Fatal(err)
	}

	c, w := newStackTestContext("GET", "/api/stacks/web/versions", "", gin.Params{{Key: "id", Value: "web"}}, "operator")
	h.Versions(c)
	if w.Code != 200 {
		t.Fatalf("期望 200，got %d（body=%s）", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			Total int `json:"total"`
			Items []struct {
				Version  int    `json:"version"`
				Content  string `json:"content"`
				Username string `json:"username"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Data.Total != 2 || len(resp.Data.Items) != 2 {
		t.Fatalf("web 应有 2 个版本，got total=%d len=%d", resp.Data.Total, len(resp.Data.Items))
	}
	if resp.Data.Items[0].Version != 2 || resp.Data.Items[0].Content != "B" {
		t.Fatalf("应按版本降序（最新 v2/B 在前），got %+v", resp.Data.Items[0])
	}

	// 非法栈 ID → 400
	c2, w2 := newStackTestContext("GET", "/api/stacks/bad%20id/versions", "", gin.Params{{Key: "id", Value: "bad id"}}, "operator")
	h.Versions(c2)
	if w2.Code != 400 {
		t.Fatalf("非法栈 ID 应 400，got %d", w2.Code)
	}
}
