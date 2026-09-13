package handler

import (
	"testing"
	"time"
)

// TestVMGrantActive 授权有效期判定（借鉴堡垒机 4A：过期即视为不存在）。
func TestVMGrantActive(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.Local)
	future := now.Add(time.Hour)
	past := now.Add(-time.Hour)
	cases := []struct {
		name      string
		expiresAt *time.Time
		want      bool
	}{
		{"nil = 长期有效", nil, true},
		{"未到期有效", &future, true},
		{"已过期失效", &past, false},
		{"边界：到期时刻之后 1ns 即失效", &now, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := vmGrantActive(tc.expiresAt, now); got != tc.want {
				t.Errorf("vmGrantActive(%v) = %v, 期望 %v", tc.expiresAt, got, tc.want)
			}
		})
	}
}
