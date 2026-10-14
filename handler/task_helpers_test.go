package handler

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
)

// TestIsTerminalTaskStatus 任务终态判定。
//
// 风险点：日志 WS（StreamLogs）用本函数决定「到终态即回放快照后关连接」。
// 若把 pending/running 误判为终态，刚提交的任务日志会瞬间被判定结束、连接关闭，
// 前端永远看不到执行输出；反过来漏判 success/failed/cancelled 则连接永不关闭、
// 浏览器挂着一个不会自愈的僵尸 WS（只在下一轮 2s 轮询里才发现）——两向都是真实故障，
// 所以三个终态常量与两个过程态都必须逐值钉死，不能靠「看起来对」。
func TestIsTerminalTaskStatus(t *testing.T) {
	cases := []struct {
		name   string
		status string
		want   bool
	}{
		{"success 是终态", model.TaskStatusSuccess, true},
		{"failed 是终态", model.TaskStatusFailed, true},
		{"cancelled 是终态", model.TaskStatusCancelled, true},
		{"pending 不是终态", model.TaskStatusPending, false},
		{"running 不是终态", model.TaskStatusRunning, false},
		{"空串不是终态", "", false},
		{"未知状态不是终态", "weird", false},
		{"大小写敏感：Success 不命中", "Success", false},
		{"带空格不命中（常量值是裸词）", " success", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTerminalTaskStatus(tc.status); got != tc.want {
				t.Errorf("isTerminalTaskStatus(%q) = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}

// TestTaskWSResource 任务日志 WS 的票据资源标识。
//
// 风险点：AuthMiddleware 校验票据时按路径算出的资源标识（wsticket.TaskResource）
// 必须与签发侧（本函数）逐字一致，否则所有任务日志 WS 连接都会被「票据与目标不匹配」
// 拦下。两者都产 "task:" 前缀 + 十进制 id；id 格式化必须无前导零、无符号。
func TestTaskWSResource(t *testing.T) {
	cases := []struct {
		id   uint
		want string
	}{
		{1, "task:1"},
		{42, "task:42"},
		{20250906, "task:20250906"},
	}
	for _, tc := range cases {
		if got := taskWSResource(tc.id); got != tc.want {
			t.Errorf("taskWSResource(%d) = %q, want %q", tc.id, got, tc.want)
		}
	}
	// 与 wsticket 侧同构前缀，防两处漂移（改一处漏另一处会全线拒连）
	if !strings.HasPrefix(taskWSResource(7), "task:") {
		t.Errorf("资源前缀应为 task:，实际 %q", taskWSResource(7))
	}
}

// TestEntriesSince 从 log_entries 原文筛选 seq>since 的条目。
//
// 风险点：日志 WS 按 seq 增量推送，客户端凭 since 去重。筛错（含等于 since、
// 或丢条目）会让前端重复渲染或漏帧；原文非 JSON 数组（老任务纯文本 result）必须
// 返回 nil 让上层回退纯文本渲染，而不是 panic 或塞入垃圾。
func TestEntriesSince(t *testing.T) {
	raw := `[{"seq":1,"kind":"stdout","text":"a"},` +
		`{"seq":2,"kind":"stderr","text":"b"},` +
		`{"seq":5,"kind":"info","text":"c"}]`

	t.Run("since=0 返回全部", func(t *testing.T) {
		got := entriesSince(raw, 0)
		if len(got) != 3 {
			t.Fatalf("期望 3 条，实际 %d", len(got))
		}
	})
	t.Run("严格大于 since（等于的不重发）", func(t *testing.T) {
		got := entriesSince(raw, 2)
		if len(got) != 1 || got[0].Seq != 5 {
			t.Fatalf("期望仅 seq=5，实际 %+v", got)
		}
	})
	t.Run("since 超过最大 seq 返回空", func(t *testing.T) {
		if got := entriesSince(raw, 5); len(got) != 0 {
			t.Fatalf("期望空，实际 %+v", got)
		}
	})
	t.Run("空串（无结构化日志）返回 nil", func(t *testing.T) {
		if got := entriesSince("", 0); got != nil {
			t.Fatalf("期望 nil，实际 %+v", got)
		}
	})
	t.Run("纯文本 result（非 JSON 数组）返回 nil", func(t *testing.T) {
		if got := entriesSince("构建完成", 0); got != nil {
			t.Fatalf("期望 nil，实际 %+v", got)
		}
	})
	t.Run("非法 JSON 数组返回 nil", func(t *testing.T) {
		if got := entriesSince("[{bad", 0); got != nil {
			t.Fatalf("期望 nil，实际 %+v", got)
		}
	})
}

// TestLastSeq 增量游标推进：返回条目里的最大 seq，且不小于传入 since。
//
// 风险点：游标只增不减——若实现里漏了「不小于 since」，一批旧条目会把游标回拨，
// 导致后续重复推送已发过的条目（前端日志出现重复段）。
func TestLastSeq(t *testing.T) {
	entries := []model.TaskLogEntry{{Seq: 3}, {Seq: 7}, {Seq: 5}}
	if got := lastSeq(entries, 0); got != 7 {
		t.Errorf("期望最大 seq=7，实际 %d", got)
	}
	if got := lastSeq(entries, 9); got != 9 {
		t.Errorf("游标不应回拨：since=9 应保持 9，实际 %d", got)
	}
	if got := lastSeq(nil, 4); got != 4 {
		t.Errorf("空条目应保持 since，实际 %d", got)
	}
}

// TestCancelTaskRejectsInvalidID 取消任务端点在触碰任务系统前先过 paramID。
//
// 风险点：取消走 paramID（防 GORM 内联条件注入），非法主键必须在 h.Tasks 被解引用
// 前就写 400 并 return——零值 TaskHandler（Tasks 为 nil）能跑到底正说明非法 ID
// 没有流入后续逻辑，否则这里会 nil 解引用 panic。
func TestCancelTaskRejectsInvalidID(t *testing.T) {
	cases := []string{"abc", "0", "-1", "1 OR 1=1"}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			c, rec := newTestContext("/api/tasks/x/cancel", gin.Params{{Key: "id", Value: raw}})
			h := &TaskHandler{} // Tasks 为 nil：非法 ID 应在触碰它之前被拦
			h.CancelTask(c)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("期望 400，实际 %d，响应体=%s", rec.Code, rec.Body.String())
			}
			if !c.IsAborted() {
				t.Error("非法 ID 未中断请求链")
			}
		})
	}
}
