package handler

// 容器动作错误映射单测（2026-10-19 补 Docker 域时加）。
//
// 背景：docker CLI 的报错是英文，经 ErrorResponse 的 friendlyMessage 因不含中文
// 会统一变成「操作失败」——用户点「启动」一个已删除的容器时完全看不出原因。
// classifyDockerError 把常见模式映射成中文 + 合适状态码。

import (
	"net/http"
	"testing"
)

func TestClassifyDockerError(t *testing.T) {
	cases := []struct {
		name   string
		msg    string
		status int
		want   string
		hit    bool
	}{
		{
			name:   "容器不存在 → 404",
			msg:    "Error response from daemon: No such container: abc123",
			status: http.StatusNotFound,
			want:   "容器不存在（可能已被删除，请刷新列表）",
			hit:    true,
		},
		{
			name:   "重复启动 → 409",
			msg:    "Error response from daemon: container abc is already running",
			status: http.StatusConflict,
			want:   "容器已在运行中",
			hit:    true,
		},
		{
			name:   "停止未运行容器 → 409",
			msg:    "Error response from daemon: container abc is not running",
			status: http.StatusConflict,
			want:   "容器未在运行",
			hit:    true,
		},
		{
			name:   "恢复未暂停容器 → 409",
			msg:    "Error response from daemon: container abc is not paused",
			status: http.StatusConflict,
			want:   "容器未暂停",
			hit:    true,
		},
		{
			name:   "暂停已暂停容器 → 409",
			msg:    "Error response from daemon: container abc is paused",
			status: http.StatusConflict,
			want:   "容器已暂停，请先恢复",
			hit:    true,
		},
		{
			name:   "重名冲突 → 409",
			msg:    "Error response from daemon: Conflict. The container name \"/web\" is already in use",
			status: http.StatusConflict,
			want:   "名称已被占用",
			hit:    true,
		},
		{
			name: "未知错误不命中（调用方回落 500 + 日志）",
			msg:  "some unexpected daemon failure",
			hit:  false,
		},
		{
			name:   "大小写不敏感",
			msg:    "NO SUCH CONTAINER: x",
			status: http.StatusNotFound,
			want:   "容器不存在（可能已被删除，请刷新列表）",
			hit:    true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, text, hit := classifyDockerError(tc.msg)
			if hit != tc.hit {
				t.Fatalf("命中判断错误：got=%v want=%v", hit, tc.hit)
			}
			if !hit {
				return
			}
			if status != tc.status {
				t.Errorf("状态码 got=%d want=%d", status, tc.status)
			}
			if tc.want != "" && text != tc.want {
				t.Errorf("文案 got=%q want=%q", text, tc.want)
			}
		})
	}
}
