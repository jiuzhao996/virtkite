package handler

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/service/dockerbuild"
)

// withTempBuildDir 把 config.DataDir 指到临时目录（dockerbuild.BaseDir() 随之重定向）。
func withTempBuildDir(t *testing.T) string {
	t.Helper()
	old := config.GlobalConfig
	dir := t.TempDir()
	config.GlobalConfig = &config.Config{DataDir: dir}
	t.Cleanup(func() { config.GlobalConfig = old })
	return filepath.Join(dir, "docker-builds")
}

// TestDockerBuildContextEndpoints 构建上下文 CRUD 与文件管理端点。
//
// 风险点：上下文名进目录名、文件相对路径进磁盘路径——两个用户可控字符串。
// 名白名单 + safePath 守卫任一失守，就是「在 data/ 下任意写文件」。
func TestDockerBuildContextEndpoints(t *testing.T) {
	withTempBuildDir(t)
	h := &DockerHandler{}

	t.Run("建上下文", func(t *testing.T) {
		c, rec := jsonCtx(t, http.MethodPost, "/api/docker/builds", nil, map[string]string{"name": "e2e-ctx"})
		h.CreateBuildContext(c)
		if rec.Code != http.StatusOK {
			t.Fatalf("期望 200，实际 %d：%s", rec.Code, rec.Body.String())
		}
		if _, err := os.Stat(filepath.Join(dockerbuild.BaseDir(), "e2e-ctx")); err != nil {
			t.Fatalf("目录未创建: %v", err)
		}
	})

	t.Run("非法名被拒", func(t *testing.T) {
		for _, name := range []string{"../evil", "-lead", "a b", ""} {
			c, rec := jsonCtx(t, http.MethodPost, "/api/docker/builds", nil, map[string]string{"name": name})
			h.CreateBuildContext(c)
			if rec.Code == http.StatusOK {
				t.Errorf("非法上下文名 %q 未被拒", name)
			}
		}
	})

	t.Run("重名冲突", func(t *testing.T) {
		c, rec := jsonCtx(t, http.MethodPost, "/api/docker/builds", nil, map[string]string{"name": "e2e-ctx"})
		h.CreateBuildContext(c)
		if rec.Code == http.StatusOK {
			t.Error("重名创建未被拒")
		}
	})

	t.Run("写 Dockerfile 并列出", func(t *testing.T) {
		c, rec := jsonCtx(t, http.MethodPut, "/api/docker/builds/e2e-ctx/file",
			gin.Params{{Key: "name", Value: "e2e-ctx"}},
			map[string]string{"path": "Dockerfile", "content": "FROM alpine\n"})
		h.WriteBuildFile(c)
		if rec.Code != http.StatusOK {
			t.Fatalf("期望 200，实际 %d：%s", rec.Code, rec.Body.String())
		}
		c2, rec2 := newTestContext("/api/docker/builds/e2e-ctx/files?path=.", gin.Params{{Key: "name", Value: "e2e-ctx"}})
		h.ListBuildFiles(c2)
		if rec2.Code != http.StatusOK {
			t.Fatalf("期望 200，实际 %d：%s", rec2.Code, rec2.Body.String())
		}
		body := decodeBody(t, rec2)
		items, _ := body["data"].(map[string]interface{})["items"].([]interface{})
		if len(items) != 1 {
			t.Fatalf("文件数期望 1，实际 %d：%v", len(items), items)
		}
	})

	t.Run("子目录读写", func(t *testing.T) {
		c, rec := jsonCtx(t, http.MethodPut, "/api/docker/builds/e2e-ctx/file",
			gin.Params{{Key: "name", Value: "e2e-ctx"}},
			map[string]string{"path": "conf/app.conf", "content": "k=v\n"})
		h.WriteBuildFile(c)
		if rec.Code != http.StatusOK {
			t.Fatalf("期望 200，实际 %d：%s", rec.Code, rec.Body.String())
		}
		c2, rec2 := newTestContext("/api/docker/builds/e2e-ctx/file?path=conf/app.conf", gin.Params{{Key: "name", Value: "e2e-ctx"}})
		h.ReadBuildFile(c2)
		if rec2.Code != http.StatusOK {
			t.Fatalf("期望 200，实际 %d：%s", rec2.Code, rec2.Body.String())
		}
		body := decodeBody(t, rec2)
		if got := body["data"].(map[string]interface{})["content"]; got != "k=v\n" {
			t.Errorf("读回内容不符：%v", got)
		}
		// 列子目录：应看到 conf/ 目录项（带尾斜杠）
		c3, rec3 := newTestContext("/api/docker/builds/e2e-ctx/files?path=.", gin.Params{{Key: "name", Value: "e2e-ctx"}})
		h.ListBuildFiles(c3)
		b3 := decodeBody(t, rec3)
		items, _ := b3["data"].(map[string]interface{})["items"].([]interface{})
		found := false
		for _, it := range items {
			if it.(map[string]interface{})["path"] == "conf/" {
				found = true
			}
		}
		if !found {
			t.Errorf("子目录未以目录项列出：%v", items)
		}
	})

	t.Run("路径穿越被拒", func(t *testing.T) {
		for _, rel := range []string{"../evil", "../../evil", "a/../../../evil", "/../../evil"} {
			c, rec := newTestContext("/api/docker/builds/e2e-ctx/file?path="+rel, gin.Params{{Key: "name", Value: "e2e-ctx"}})
			h.ReadBuildFile(c)
			if rec.Code == http.StatusOK {
				t.Errorf("越界路径 %q 未被拒", rel)
			}
			c2, rec2 := jsonCtx(t, http.MethodPut, "/api/docker/builds/e2e-ctx/file",
				gin.Params{{Key: "name", Value: "e2e-ctx"}},
				map[string]string{"path": rel, "content": "x"})
			h.WriteBuildFile(c2)
			if rec2.Code == http.StatusOK {
				t.Errorf("越界写 %q 未被拒", rel)
			}
		}
		if _, err := os.Stat(filepath.Join(dockerbuild.BaseDir(), "evil")); err == nil {
			t.Error("越界写实际落盘")
		}

		// 绝对路径不构成逃逸：filepath.Join 会把它并进上下文内（写成 <ctx>/etc/passwd），
		// 落点仍在上下文里——这里锁住这个语义，避免日后误改成「直通绝对路径」。
		c, rec := jsonCtx(t, http.MethodPut, "/api/docker/builds/e2e-ctx/file",
			gin.Params{{Key: "name", Value: "e2e-ctx"}},
			map[string]string{"path": "/etc/passwd", "content": "x"})
		h.WriteBuildFile(c)
		if rec.Code != http.StatusOK {
			t.Fatalf("绝对路径应被并入上下文（200），实际 %d：%s", rec.Code, rec.Body.String())
		}
		if _, err := os.Stat(filepath.Join(dockerbuild.BaseDir(), "e2e-ctx", "etc", "passwd")); err != nil {
			t.Errorf("绝对路径未落在上下文内: %v", err)
		}
		if _, err := os.Stat("/etc/passwd.orig"); err == nil {
			t.Error("意外写到了上下文之外")
		}
	})

	t.Run("上传二进制文件", func(t *testing.T) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		_ = mw.WriteField("path", "lib/app.jar")
		fw, _ := mw.CreateFormFile("file", "app.jar")
		_, _ = fw.Write([]byte{0x50, 0x4b, 0x03, 0x04, 0x00}) // PK 头，二进制安全
		_ = mw.Close()

		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/docker/builds/e2e-ctx/upload", &buf)
		c.Request.Header.Set("Content-Type", mw.FormDataContentType())
		c.Params = gin.Params{{Key: "name", Value: "e2e-ctx"}}
		h.UploadBuildFile(c)
		if rec.Code != http.StatusOK {
			t.Fatalf("期望 200，实际 %d：%s", rec.Code, rec.Body.String())
		}
		got, err := os.ReadFile(filepath.Join(dockerbuild.BaseDir(), "e2e-ctx", "lib", "app.jar"))
		if err != nil || len(got) != 5 || got[0] != 0x50 {
			t.Fatalf("上传内容不符: %v %v", err, got)
		}
	})

	t.Run("删文件与删上下文", func(t *testing.T) {
		c, rec := newTestContext("/api/docker/builds/e2e-ctx/file?path=Dockerfile", gin.Params{{Key: "name", Value: "e2e-ctx"}})
		h.DeleteBuildFile(c)
		if rec.Code != http.StatusOK {
			t.Fatalf("期望 200，实际 %d：%s", rec.Code, rec.Body.String())
		}
		// 删不存在的文件 → 非 200（防「假成功」）
		c2, rec2 := newTestContext("/api/docker/builds/e2e-ctx/file?path=Dockerfile", gin.Params{{Key: "name", Value: "e2e-ctx"}})
		h.DeleteBuildFile(c2)
		if rec2.Code == http.StatusOK {
			t.Error("删除不存在的文件返回了成功")
		}
		c3, rec3 := newTestContext("/api/docker/builds/e2e-ctx", gin.Params{{Key: "name", Value: "e2e-ctx"}})
		h.DeleteBuildContext(c3)
		if rec3.Code != http.StatusOK {
			t.Fatalf("期望 200，实际 %d：%s", rec3.Code, rec3.Body.String())
		}
		if _, err := os.Stat(filepath.Join(dockerbuild.BaseDir(), "e2e-ctx")); err == nil {
			t.Error("上下文目录未删除")
		}
		// 删不存在的上下文 → 非 200
		c4, rec4 := newTestContext("/api/docker/builds/e2e-ctx", gin.Params{{Key: "name", Value: "e2e-ctx"}})
		h.DeleteBuildContext(c4)
		if rec4.Code == http.StatusOK {
			t.Error("删除不存在的上下文返回了成功")
		}
	})
}

// TestBuildImageValidatesTag 构建端点的标签校验必须早于任务提交（否则坏 tag 会变成
// 一条注定失败的异步任务，用户要等到任务跑完才知道）。
// 无 docker 环境时 dockerAvailable 会先返回 503——这同样说明「没提交任务」，两种结果都接受。
func TestBuildImageValidatesTag(t *testing.T) {
	withTempBuildDir(t)
	h := &DockerHandler{}
	if err := dockerbuild.Create("ctx"); err != nil {
		t.Fatal(err)
	}
	for _, tag := range []string{"-x", "app:", "app 1"} {
		c, rec := jsonCtx(t, http.MethodPost, "/api/docker/builds/ctx/build",
			gin.Params{{Key: "name", Value: "ctx"}}, map[string]string{"tag": tag})
		h.BuildImage(c)
		if rec.Code == http.StatusAccepted {
			t.Errorf("非法 tag %q 被接受提交了任务", tag)
		}
	}
	// 上下文不存在 → 拒绝
	c, rec := jsonCtx(t, http.MethodPost, "/api/docker/builds/nope/build",
		gin.Params{{Key: "name", Value: "nope"}}, map[string]string{"tag": "ok:1"})
	h.BuildImage(c)
	if rec.Code == http.StatusAccepted {
		t.Error("不存在的上下文被接受提交了任务")
	}
}
