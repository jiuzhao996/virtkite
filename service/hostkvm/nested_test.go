package hostkvm

import "testing"

// TestDetect 探测函数在任意环境下都不 panic，且字段自洽（本机为 KVM 宿主时应 supported）。
func TestDetect(t *testing.T) {
	st := Detect()
	if st == nil {
		t.Fatal("Detect 不应返回 nil")
	}
	if !st.Supported && st.Reason == "" {
		t.Error("不支持时应给出原因")
	}
	if st.Supported && st.Module == "" {
		t.Error("支持时应给出内核模块名")
	}
	if st.Enabled && st.Value == "" {
		t.Error("已开启时应有原始值")
	}
}
