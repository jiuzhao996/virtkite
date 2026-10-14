package handler

import (
	"net/http"
	"strings"
	"testing"
)

// TestCreateNetworkValidation 建网在触碰 libvirt 前的纯校验分支。
//
// 风险点：网络名进 libvirt 网络 XML、gateway 进 <ip address>、bridge 名进 <bridge name>——
// 任一未校验都构成 XML 注入/非法定义面。三层校验（名称白名单 → gateway 合法 IPv4 →
// bridge 模式必须给宿主网桥）都必须在 Virt.DefineNetwork 之前 return；零值 NetworkHandler
// （Virt nil）跑到这些 400 即证明校验前置。validIPv4 本身的遍历已在 validate_test.go，
// 这里补的是它被 CreateNetwork 正确接线。
func TestCreateNetworkValidation(t *testing.T) {
	h := &NetworkHandler{} // Virt 为 nil：只测不定义网络的校验分支

	cases := []struct {
		name    string
		body    interface{}
		wantMsg string
	}{
		{"缺 name：binding required 拦截",
			map[string]string{}, "参数错误"},
		{"网络名含空格与符号",
			map[string]string{"name": "bad name!"}, "网络名称"},
		{"网络名含中文",
			map[string]string{"name": "网络一"}, "网络名称"},
		{"网关非法 IPv4",
			map[string]string{"name": "net1", "mode": "nat", "gateway": "999.1.1.1"}, "网关必须"},
		{"网关是 IPv6（应被 validIPv4 拒）",
			map[string]string{"name": "net1", "mode": "nat", "gateway": "::1"}, "网关必须"},
		{"桥接模式未指定宿主网桥",
			map[string]string{"name": "net1", "mode": "bridge"}, "桥接网络需指定宿主机网桥名"},
		{"未知转发模式",
			map[string]string{"name": "net1", "mode": "weird"}, "不支持的转发模式"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := jsonCtx(t, http.MethodPost, "/api/networks", nil, tc.body)
			h.CreateNetwork(c)
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
