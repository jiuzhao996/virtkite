package handler

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestVolumeRefsInUse 卷「在用」判定的三重口径。
//
// 风险点：删卷守卫（DeleteVolume）用 inUse 决定是否阻止删除，三重引用（挂载 VM /
// 镜像库登记 / 是别的卷的 backing 父盘）任一命中都必须判为在用。少判一类 = 放行误删
// 正在被使用的磁盘，是本项目最不可接受的错误（宁可删不掉，不可损坏在用磁盘）。
func TestVolumeRefsInUse(t *testing.T) {
	cases := []struct {
		name string
		r    VolumeRefs
		want bool
	}{
		{"零引用=不在用", VolumeRefs{}, false},
		{"仅被 VM 挂载", VolumeRefs{VMs: []string{"vm-1"}}, true},
		{"仅镜像库登记", VolumeRefs{Images: []string{"ubuntu"}}, true},
		{"仅作为克隆父盘", VolumeRefs{Children: []string{"c1"}}, true},
		{"三类齐全", VolumeRefs{VMs: []string{"v"}, Images: []string{"i"}, Children: []string{"c"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.r.inUse(); got != tc.want {
				t.Errorf("inUse() = %v, want %v（refs=%+v）", got, tc.want, tc.r)
			}
		})
	}
}

// TestCreatePoolValidation 创建存储池在触碰 libvirt 前的纯校验分支。
//
// 风险点：目录型池的 path 决定该池全部卷的落盘位置，零校验等于让调用方指定宿主机任意目录
// （写到 /etc、/root 等）。name/path 两个白名单必须在 Virt.CreateDirPool 之前 return——
// 零值 StorageHandler（Virt nil）跑到这些 400 即证明未提前解引用 libvirt。
func TestCreatePoolValidation(t *testing.T) {
	h := &StorageHandler{} // Virt/DB 为 nil

	cases := []struct {
		name    string
		body    interface{}
		wantMsg string
	}{
		{"池名含非法字符", map[string]string{"name": "bad/name", "path": "/srv/pool"}, "存储池名称"},
		{"路径非绝对", map[string]string{"name": "pool1", "path": "srv/pool"}, "存储池路径"},
		{"路径含 ..", map[string]string{"name": "pool1", "path": "/srv/../etc"}, "存储池路径"},
		{"路径含结尾斜杠（非规范）", map[string]string{"name": "pool1", "path": "/srv/pool/"}, "存储池路径"},
		{"路径是根目录", map[string]string{"name": "pool1", "path": "/"}, "存储池路径"},
		{"路径含 XML 元字符", map[string]string{"name": "pool1", "path": "/srv/<x>"}, "存储池路径"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := jsonCtx(t, http.MethodPost, "/api/storage/pools", nil, tc.body)
			h.CreatePool(c)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("期望 400，实际 %d，响应体=%s", rec.Code, rec.Body.String())
			}
			body := decodeBody(t, rec)
			if msg, _ := body["message"].(string); !strings.Contains(msg, tc.wantMsg) {
				t.Errorf("文案应含 %q，实际 %q", tc.wantMsg, msg)
			}
		})
	}
}

// TestCreateVolumeValidation 建卷在触碰 libvirt 前的纯校验分支。
//
// 风险点：卷名进 libvirt 卷 XML，格式决定落盘编码（qcow2/raw）。白名单放行非法名或
// 未知格式会构造出非法/不预期的卷。零值 handler 跑到这些 400 即证明校验先于 Virt 调用。
func TestCreateVolumeValidation(t *testing.T) {
	h := &StorageHandler{} // Virt 为 nil

	cases := []struct {
		name    string
		body    interface{}
		wantMsg string
	}{
		{"卷名含斜杠", map[string]string{"name": "a/b", "format": "qcow2"}, "卷名称"},
		{"卷名含 XML 元字符", map[string]string{"name": "a\"b", "format": "qcow2"}, "卷名称"},
		{"未知格式", map[string]string{"name": "disk.qcow2", "format": "vmdk"}, "卷格式"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := jsonCtx(t, http.MethodPost, "/api/storage/pools/p/volumes",
				gin.Params{{Key: "name", Value: "p"}}, tc.body)
			h.CreateVolume(c)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("期望 400，实际 %d，响应体=%s", rec.Code, rec.Body.String())
			}
			body := decodeBody(t, rec)
			if msg, _ := body["message"].(string); !strings.Contains(msg, tc.wantMsg) {
				t.Errorf("文案应含 %q，实际 %q", tc.wantMsg, msg)
			}
		})
	}
}

// TestUpdatePoolMetaValidation 池元数据写入的纯校验分支（角色白名单 + 描述长度）。
//
// 风险点：role 只允许白名单值或空串（空=跟随自动推断），description 上限 500 字符。
// 这些校验在读写 DB 之前 return，故用 DB 为 nil 的零值 handler 可安全断言——
// 若漏判，非法角色会落库、超长描述会被 MySQL 截断成静默脏数据。
func TestUpdatePoolMetaValidation(t *testing.T) {
	h := &StorageHandler{} // DB 为 nil

	t.Run("池名非法被拦", func(t *testing.T) {
		c, rec := rawJSONCtx(http.MethodPut, "/api/storage/pools/x/meta", `{}`,
			gin.Params{{Key: "name", Value: "bad/name"}})
		h.UpdatePoolMeta(c)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("期望 400，实际 %d", rec.Code)
		}
		if msg, _ := decodeBody(t, rec)["message"].(string); !strings.Contains(msg, "存储池名称") {
			t.Errorf("文案不符：%q", msg)
		}
	})

	t.Run("角色不在白名单", func(t *testing.T) {
		c, rec := rawJSONCtx(http.MethodPut, "/api/storage/pools/pool1/meta",
			`{"role":"怪角色"}`, gin.Params{{Key: "name", Value: "pool1"}})
		h.UpdatePoolMeta(c)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("期望 400，实际 %d，响应体=%s", rec.Code, rec.Body.String())
		}
		if msg, _ := decodeBody(t, rec)["message"].(string); !strings.Contains(msg, "角色只允许") {
			t.Errorf("文案不符：%q", msg)
		}
	})

	t.Run("描述超 500 字符", func(t *testing.T) {
		body := `{"role":"","description":"` + strings.Repeat("字", 501) + `"}`
		c, rec := rawJSONCtx(http.MethodPut, "/api/storage/pools/pool1/meta",
			body, gin.Params{{Key: "name", Value: "pool1"}})
		h.UpdatePoolMeta(c)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("期望 400，实际 %d，响应体=%s", rec.Code, rec.Body.String())
		}
		if msg, _ := decodeBody(t, rec)["message"].(string); msg != "描述最长 500 字符" {
			t.Errorf("文案不符：%q", msg)
		}
	})
}
