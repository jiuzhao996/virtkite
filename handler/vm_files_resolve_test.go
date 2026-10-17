package handler

// use_saved 路径回归测试（2026-10-17 E2E 实测发现的 nil 指针）。
//
// 背景：resolve 的命名返回值 vm 要到函数末尾 return &record 才赋值，
// 而 use_saved 分支里曾写成 h.VMCred.ResolveFor(c, vm.ID) —— vm 为 nil，
// 任何 use_saved=true 的请求都会 panic，被 gin Recovery 吞成 500 空响应。
// 本测试锁定「无托管凭据时必须返回 400 + 中文原因，而不是 panic/500」。

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/jiuzhao/vmops/model"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newFilesTestContext(t *testing.T, vmID uint, body string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/vms/"+strconv.FormatUint(uint64(vmID), 10)+"/files/list",
		bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "id", Value: strconv.FormatUint(uint64(vmID), 10)}}
	c.Set("role", "admin") // 跳过资产授权分支，专测 use_saved 逻辑
	return c, w
}

func TestVMFilesResolveUseSavedWithoutCredential(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("内存库失败: %v", err)
	}
	if err := db.AutoMigrate(&model.VM{}, &model.VMCredential{}); err != nil {
		t.Fatal(err)
	}
	vm := model.VM{Name: "e2e-resolve-test", UUID: "uuid-resolve-test", Status: model.VMStatusShutOff}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatal(err)
	}

	h := NewVMFilesHandler(db)
	h.SetVMCredentialHandler(NewVMCredentialHandler(db, "test-master-secret-at-least-32-bytes!!"))

	c, w := newFilesTestContext(t, vm.ID, `{"host":"10.0.0.1","use_saved":true,"path":"/"}`)
	req := &vmFilesReq{}

	// 回归点：这里绝不能 panic；应走「未配置凭据」的 400 分支
	_, _, ok := h.resolve(c, req, "列目录")
	if ok {
		t.Fatal("无托管凭据时 resolve 不应成功")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("期望 400，实际 %d（body=%s）", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "尚未配置保存的凭据") {
		t.Fatalf("错误文案不符：%s", w.Body.String())
	}
}

func TestVMFilesResolveUseSavedNilCredentialHandler(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("内存库失败: %v", err)
	}
	if err := db.AutoMigrate(&model.VM{}, &model.VMCredential{}); err != nil {
		t.Fatal(err)
	}
	vm := model.VM{Name: "e2e-resolve-nil", UUID: "uuid-resolve-nil", Status: model.VMStatusShutOff}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatal(err)
	}

	// 故意不注入凭据托管：必须给明确 503，而不是 nil 解引用
	h := NewVMFilesHandler(db)
	c, w := newFilesTestContext(t, vm.ID, `{"host":"10.0.0.1","use_saved":true,"path":"/"}`)
	req := &vmFilesReq{}

	_, _, ok := h.resolve(c, req, "列目录")
	if ok {
		t.Fatal("凭据托管缺失时 resolve 不应成功")
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("期望 503，实际 %d（body=%s）", w.Code, w.Body.String())
	}
}
