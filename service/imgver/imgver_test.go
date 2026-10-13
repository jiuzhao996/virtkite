package imgver

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// roundTripFunc 让测试注入假的 registry 响应，无需真实网络。
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// withFakeRegistry 把包级 client 换成假 registry：token 请求固定返回一个合法 token，
// manifest 请求交给 manifestResp 决定。测试结束还原。
//
// 风险点：CheckAll 的 up_to_date/outdated 判定完全依赖对远端 manifest 的解析。
// 此处把网络层替换掉，就能对「token 流 → digest 比对」这段纯逻辑做端到端断言，
// 而不必连真实镜像代理（既慢又不稳定）。
func withFakeRegistry(t *testing.T, manifest func() *http.Response) {
	t.Helper()
	orig := client
	client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/auth/token") {
			return newResp(200, tokenOKBdy, nil), nil
		}
		return manifest(), nil
	})}
	t.Cleanup(func() { client = orig })
}

// newResp 构造一个 http.Response；body 为空串时给个可读的空 body。
func newResp(code int, body string, hdr map[string]string) *http.Response {
	h := http.Header{}
	for k, v := range hdr {
		h.Set(k, v)
	}
	return &http.Response{
		StatusCode: code,
		Header:     h,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

const (
	localHex   = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	indexHex   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	localFull  = "sha256:" + localHex
	indexFull  = "sha256:" + indexHex
	tokenOKBdy = `{"token":"dummy"}`
)

// TestSplitRepoTag 锁死镜像名拆分（repo/tag）。
//
// 风险点：本地镜像名里可能带 registry 主机名与端口（host:5000/path/img）。
// 端口冒号与 tag 冒号同形，用 strings.LastIndex 取最后一个冒号、并排除冒号右侧
// 含 / 的情况，是区分二者的关键。写错会让「无 tag 的带端口镜像名」被误拆成
// repo=host、tag=5000/path/img，远端查询必然 404。
func TestSplitRepoTag(t *testing.T) {
	tests := []struct {
		name     string
		image    string
		wantRepo string
		wantTag  string
	}{
		{"无 tag 默认 latest", "nginx", "nginx", "latest"},
		{"显式 tag", "nginx:1.25", "nginx", "1.25"},
		{"带路径无 tag", "library/nginx", "library/nginx", "latest"},
		{"带路径含 tag", "library/nginx:1.25", "library/nginx", "1.25"},
		{"带端口的 registry 无 tag（端口冒号不应被当 tag 分隔）", "registry.local:5000/team/app", "registry.local:5000/team/app", "latest"},
		{"带端口的 registry 含 tag", "registry.local:5000/team/app:v2", "registry.local:5000/team/app", "v2"},
		{"只有 host:port 无路径（无斜杠时按 name:tag 解析，与 docker 语义一致）", "registry.local:5000", "registry.local", "5000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, tag := SplitRepoTag(tt.image)
			if repo != tt.wantRepo || tag != tt.wantTag {
				t.Errorf("SplitRepoTag(%q) 期望 (%q, %q)，实际 (%q, %q)",
					tt.image, tt.wantRepo, tt.wantTag, repo, tag)
			}
		})
	}
}

// TestShortDigest 锁死 digest 展示截断的边界。
//
// 风险点：digest 全串 71 字符，直接塞进列表会把行撑爆。截断阈值 19 是「sha256:」+
// 前 12 位真实摘要。边界写错（> vs >=）会让 19 位串多/少一个省略号，或截出乱码。
func TestShortDigest(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"短串原样", "sha256:abc", "sha256:abc"},
		{"恰好 19 位不截断", strings.Repeat("x", 19), strings.Repeat("x", 19)},
		{"20 位截为前 19 + 省略号", strings.Repeat("x", 20), strings.Repeat("x", 19) + "…"},
		{"完整 digest 截断", localFull, localFull[:19] + "…"},
		{"空串", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShortDigest(tt.in); got != tt.want {
				t.Errorf("ShortDigest(%q) 期望 %q，实际 %q", tt.in, tt.want, got)
			}
		})
	}
}

// TestOutdated 过滤并格式化 outdated 镜像名。
//
// 风险点：这个列表会被上层拼成「以下镜像有更新」的提示。把 skipped/unknown
// 也算进去会造成误报（本地构建镜像根本没有远端）；格式化用错分隔符（+ 而非 :）
// 会拼出无效镜像名。
func TestOutdated(t *testing.T) {
	items := []Item{
		{Repository: "nginx", Tag: "latest", Status: "outdated"},
		{Repository: "redis", Tag: "7", Status: "up_to_date"},
		{Repository: "local", Tag: "dev", Status: "skipped"},
		{Repository: "busybox", Tag: "1", Status: "outdated"},
	}
	got := Outdated(items)
	if len(got) != 2 || got[0] != "nginx:latest" || got[1] != "busybox:1" {
		t.Fatalf("Outdated 期望 [nginx:latest busybox:1]，实际 %v", got)
	}
	if out := Outdated(nil); out != nil {
		t.Fatalf("空输入应返回 nil，实际 %v", out)
	}
}

// TestCheckAllSkipBranches 覆盖不触网的早退分支：悬空镜像、无本地 digest。
//
// 风险点：这两类镜像没有可比的远端对象，若没在触网前早退，要么拼出 <none>:<none>
// 去查远端（404），要么把「本地构建镜像」误报成 outdated，制造无意义的告警噪音。
// 这里用「LocalDigest 被调用即计数」来确认悬空镜像根本没走到 digest 查询。
func TestCheckAllSkipBranches(t *testing.T) {
	digestCalls := 0
	r := &Runner{
		ListImages: func() ([][2]string, error) {
			return [][2]string{
				{"nginx", "latest"},
				{"<none>", "<none>"},
				{"mybuild", "<none>"},
				{"localbuild", "v1"},
			}, nil
		},
		LocalDigest: func(image string) string {
			digestCalls++
			if image == "localbuild:v1" {
				return "" // 本地构建/导入镜像无 RepoDigest
			}
			return localFull
		},
	}
	// nginx:latest 会走到远端查询——用假 registry 兜住，避免真实网络。
	withFakeRegistry(t, func() *http.Response {
		return newResp(200, "", map[string]string{"Docker-Content-Digest": localFull})
	})

	items, err := r.CheckAll()
	if err != nil {
		t.Fatalf("CheckAll 失败: %v", err)
	}
	if len(items) != 4 {
		t.Fatalf("期望 4 条结果，实际 %d", len(items))
	}
	byKey := map[string]Item{}
	for _, it := range items {
		byKey[it.Repository+"|"+it.Tag] = it
	}
	if it := byKey["<none>|<none>"]; it.Status != "skipped" {
		t.Errorf("悬空镜像应为 skipped，实际 %q", it.Status)
	}
	if it := byKey["mybuild|<none>"]; it.Status != "skipped" {
		t.Errorf("tag 为 <none> 的镜像应为 skipped，实际 %q", it.Status)
	}
	if it := byKey["localbuild|v1"]; it.Status != "skipped" || it.Note == "" {
		t.Errorf("无 RepoDigest 的镜像应为 skipped 且带说明，实际 %+v", it)
	}
	// 悬空的两条不应触发 LocalDigest，只剩 nginx 与 localbuild 各调一次。
	if digestCalls != 2 {
		t.Errorf("LocalDigest 调用次数期望 2（悬空镜像不应触发），实际 %d", digestCalls)
	}
}

// TestCheckAllDigestMatch 覆盖 digest 比对的三条主路径：一致、不一致、索引清单命中。
//
// 风险点：判定「远端有更新」就是拿本地 RepoDigest 去比对远端返回的所有 digest。
// 远端可能是多架构索引（manifest list），本地 digest 藏在 manifests[].digest 里；
// 若只看响应头的 Docker-Content-Digest，多架构镜像会被永远误报成 outdated。
// 三条断言分别守住：一致 up_to_date、不一致 outdated、索引内命中仍算一致。
func TestCheckAllDigestMatch(t *testing.T) {
	tests := []struct {
		name       string
		manifest   func() *http.Response
		wantStatus string
	}{
		{
			name: "头 digest 一致 → up_to_date",
			manifest: func() *http.Response {
				return newResp(200, "", map[string]string{"Docker-Content-Digest": localFull})
			},
			wantStatus: "up_to_date",
		},
		{
			name: "头 digest 不一致 → outdated",
			manifest: func() *http.Response {
				return newResp(200, "", map[string]string{"Docker-Content-Digest": indexFull})
			},
			wantStatus: "outdated",
		},
		{
			name: "多架构索引 manifests 内命中 → up_to_date",
			manifest: func() *http.Response {
				body := `{"manifests":[{"digest":"` + indexFull + `"},{"digest":"` + localFull + `"}]}`
				return newResp(200, body, map[string]string{"Docker-Content-Digest": indexFull})
			},
			wantStatus: "up_to_date",
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
				t.Fatalf("CheckAll 失败: %v", err)
			}
			if len(items) != 1 || items[0].Status != tt.wantStatus {
				t.Fatalf("期望单条 %q，实际 %+v", tt.wantStatus, items)
			}
		})
	}
}

// TestCheckAllRemoteError 远端查询失败时状态应为 unknown 而非崩溃。
//
// 风险点：token 响应异常/网络抖动都要降级成 unknown 并带说明，绝不能把整批检测
// 拖垮（原先 handler 里一个错误会让整个接口 500）。这条守住「失败即跳过」的契约。
func TestCheckAllRemoteError(t *testing.T) {
	// token 响应体不含 token 字段 → remoteDigests 报「token 响应异常」。
	orig := client
	client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/auth/token") {
			return newResp(200, `{}`, nil), nil
		}
		return newResp(200, "", nil), nil
	})}
	t.Cleanup(func() { client = orig })

	r := &Runner{
		ListImages:  func() ([][2]string, error) { return [][2]string{{"nginx", "latest"}}, nil },
		LocalDigest: func(string) string { return localFull },
	}
	items, err := r.CheckAll()
	if err != nil {
		t.Fatalf("远端失败不应让 CheckAll 整体报错，实际 %v", err)
	}
	if len(items) != 1 || items[0].Status != "unknown" {
		t.Fatalf("期望 unknown，实际 %+v", items)
	}
	if !strings.Contains(items[0].Note, "远端查询失败") {
		t.Errorf("unknown 结果应带失败说明，实际 note=%q", items[0].Note)
	}
}
