package tasks

import (
	"strings"
	"testing"
)

// TestParseRecap 覆盖 PLAY RECAP 段解析。
//
// 风险点：解析结果直接进任务 Result 的 recap 字段，前端按主机渲染 ok/changed/failed
// 矩阵——解析错一台，矩阵就整体失真；同时 ansible 输出有大量装饰行（ cowsay 对话框、
// 空行、PLAY 横幅），解析必须只认「host : ok=N」形态。
func TestParseRecap(t *testing.T) {
	text := strings.Join([]string{
		"PLAY RECAP",
		"e2e-web4                   : ok=5    changed=4    unreachable=0    failed=0    skipped=0    rescued=0    ignored=0",
		"web-2                      : ok=1    changed=0    unreachable=1    failed=2    skipped=0    rescued=0    ignored=0",
	}, "\n")
	got := parseRecap(text)
	if len(got) != 2 {
		t.Fatalf("应解析出 2 台主机，得到 %d: %#v", len(got), got)
	}
	c := got["e2e-web4"]
	if c["ok"] != 5 || c["changed"] != 4 || c["failed"] != 0 {
		t.Errorf("e2e-web4 计数不符: %#v", c)
	}
	c2 := got["web-2"]
	if c2["ok"] != 1 || c2["unreachable"] != 1 || c2["failed"] != 2 {
		t.Errorf("web-2 计数不符: %#v", c2)
	}

	// 非 recap 行（横幅/空行/无 ok= 的行）一律忽略
	if got := parseRecap("PLAY [init] ************\n\nTASK [x] ***********"); len(got) != 0 {
		t.Errorf("装饰行不应被解析: %#v", got)
	}
	if got := parseRecap(""); len(got) != 0 {
		t.Errorf("空输入应得空表: %#v", got)
	}
}

// TestUintSliceParam 覆盖 targets 参数解析：json 反序列化的数字是 float64，
// 必须无损转 uint；负数/非整型/缺键都要拒绝而非静默截断。
func TestUintSliceParam(t *testing.T) {
	got, err := uintSliceParam(map[string]interface{}{"targets": []interface{}{float64(89), float64(90)}}, "targets")
	if err != nil || len(got) != 2 || got[0] != 89 || got[1] != 90 {
		t.Errorf("合法 float64 列表应无损转换: %v, %v", got, err)
	}
	if _, err := uintSliceParam(map[string]interface{}{"targets": []interface{}{float64(-1)}}, "targets"); err == nil {
		t.Error("负数应拒绝")
	}
	if _, err := uintSliceParam(map[string]interface{}{"targets": []interface{}{"89"}}, "targets"); err == nil {
		t.Error("字符串元素应拒绝")
	}
	if _, err := uintSliceParam(map[string]interface{}{}, "targets"); err == nil {
		t.Error("缺键应拒绝")
	}
}
