package imgver

import (
	"net/http"
	"strings"
	"testing"
)

// TestRemoteDigestsErrorBranches 补齐 remoteDigests 的两个未被覆盖的错误分支：
// manifest 返回非 200、以及 200 但响应里一个 digest 都没有。
//
// 风险点：CheckAll 对远端查询失败降级为 unknown（不误报 outdated、不拖垮整批）。
// 已有的用例只覆盖了「token 响应异常」这一条失败路径；manifest 阶段的失败若未被
// 正确识别成 error，就会把「远端拉不到 manifest」错当成「和本地一致」→ 要么静默
// 漏报更新，要么拿空 digest 比对造成误报。这里经与 TestCheckAllRemoteError 相同的
// 假 registry 注入，锁定 manifest 阶段的失败一定会走到 unknown + 带失败说明。
func TestRemoteDigestsErrorBranches(t *testing.T) {
	tests := []struct {
		name     string
		manifest func() *http.Response
	}{
		{
			name: "manifest 返回 404",
			manifest: func() *http.Response {
				return newResp(http.StatusNotFound, "", nil)
			},
		},
		{
			name: "manifest 200 但无 Docker-Content-Digest 且 body 无 manifests",
			manifest: func() *http.Response {
				return newResp(http.StatusOK, "", nil)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withFakeRegistry(t, tt.manifest)
			r := &Runner{
				ListImages:  func() ([][2]string, error) { return [][2]string{{"nginx", "latest"}}, nil },
				LocalDigest: func(string) string { return localFull },
			}
			items, err := r.CheckAll()
			if err != nil {
				t.Fatalf("远端失败不应让 CheckAll 整体报错: %v", err)
			}
			if len(items) != 1 || items[0].Status != "unknown" {
				t.Fatalf("期望单条 unknown，实际 %+v", items)
			}
			if !strings.Contains(items[0].Note, "远端查询失败") {
				t.Errorf("unknown 结果应带失败说明，实际 note=%q", items[0].Note)
			}
		})
	}
}
