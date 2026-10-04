package cron

import (
	"strings"
	"testing"
)

// TestSummarizeAnsibleRun 覆盖定时 playbook 的执行历史摘要生成。
//
// 风险点：摘要写入 cron_runs.output 并决定任务成败口径——主机有 failed/unreachable
// 时任务必须判失败（notifyFailure 才会触发），只看 ansible 退出码会漏掉「RECAP 里有
// failed 但整体 202 成功」的半失败场景。
func TestSummarizeAnsibleRun(t *testing.T) {
	s := &Scheduler{}

	// 全绿：摘要带逐主机计数，err 为 nil
	recap := `{"e2e-web4":{"ok":3,"changed":0,"failed":0,"unreachable":0},"web-2":{"ok":1,"changed":1,"failed":0,"unreachable":0}}`
	summary, err := s.summarizeAnsibleRun("sysctl-tuning", `{"module":"","playbook":"sysctl-tuning","targets":2,"recap":`+recap+`}`)
	if err != nil {
		t.Fatalf("全绿 recap 不应报错: %v", err)
	}
	if !strings.Contains(summary, "sysctl-tuning") || !strings.Contains(summary, "e2e-web4 ok=3") || !strings.Contains(summary, "web-2 ok=1 changed=1") {
		t.Errorf("摘要应含 playbook 名与逐主机计数: %s", summary)
	}

	// 半失败：有 failed/unreachable 台数 → 任务判失败（触发失败通知），摘要仍返回
	_, err = s.summarizeAnsibleRun("sysctl-tuning", `{"recap":{"web-2":{"ok":1,"changed":0,"failed":2,"unreachable":1}}}`)
	if err == nil {
		t.Error("failed/unreachable>0 应判失败")
	} else if !strings.Contains(err.Error(), "3 台主机失败") {
		t.Errorf("错误应含失败台数（failed+unreachable 合计 2+1=3）: %v", err)
	}

	// 空结果：不算失败，摘要也不崩（理论不可达，防御 executor 行为变化）
	if summary, err := s.summarizeAnsibleRun("x", ""); err != nil || !strings.Contains(summary, "x") {
		t.Errorf("空结果应得无崩溃摘要: %q, %v", summary, err)
	}
}
