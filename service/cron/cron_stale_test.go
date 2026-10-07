package cron

import (
	"encoding/json"
	"testing"
	"time"
)

func TestLastDue(t *testing.T) {
	spec, err := ParseCron("0 * * * *") // 每小时整点
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 7, 3, 20, 0, 0, time.Local)
	// 回看窗口 30 分钟（grace=30 → 31 分钟）覆盖不到 03:00（差 20 分钟，能覆盖到）
	due := lastDue(spec, now, 30)
	if due.Hour() != 3 || due.Minute() != 0 {
		t.Errorf("每小时任务应命中 03:00，得到 %s", due.Format("15:04"))
	}
	// grace=5：回看 6 分钟只到 03:14，命中不到 03:00 → 零值
	if d := lastDue(spec, now, 5); !d.IsZero() {
		t.Errorf("窄窗口不应命中，得到 %s", d.Format("15:04"))
	}
	// 每天 03:00 的任务，在 03:10 回看应命中 03:00
	daily, _ := ParseCron("0 3 * * *")
	d := lastDue(daily, time.Date(2026, 10, 7, 3, 10, 0, 0, time.Local), 30)
	if d.Hour() != 3 || d.Minute() != 0 {
		t.Errorf("每日任务应命中当日 03:00，得到 %s", d.Format("01-02 15:04"))
	}
	// 每分钟任务：now 那一分钟即命中
	everyMin, _ := ParseCron("* * * * *")
	em := lastDue(everyMin, now, 1)
	if em != now {
		t.Errorf("每分钟任务应命中当前整分，得到 %s want %s", em.Format("15:04"), now.Format("15:04"))
	}
}

func TestClampInt(t *testing.T) {
	cases := []struct{ v, lo, hi, want int }{
		{5, 0, 3, 3}, {-1, 0, 3, 0}, {2, 0, 3, 2}, {60, 1, 120, 60}, {200, 1, 120, 120},
	}
	for _, c := range cases {
		if got := clampInt(c.v, c.lo, c.hi); got != c.want {
			t.Errorf("clampInt(%d,%d,%d)=%d want %d", c.v, c.lo, c.hi, got, c.want)
		}
	}
}

func TestJSONEscapeString(t *testing.T) {
	// 含引号/换行/反斜杠的摘要替换进 JSON 字符串后必须仍是合法 JSON
	got := jsonEscapeString("行1\n\"引号\"\\反斜杠")
	wrapped := `{"msg":"` + got + `"}`
	var m map[string]string
	if err := json.Unmarshal([]byte(wrapped), &m); err != nil {
		t.Fatalf("转义后不是合法 JSON: %v（串=%q）", err, wrapped)
	}
	if m["msg"] != "行1\n\"引号\"\\反斜杠" {
		t.Errorf("转义往返不一致: %q", m["msg"])
	}
	if jsonEscapeString("") != "" {
		t.Errorf("空串应得空串")
	}
}
