// 本文件负责「定时执行 playbook」动作（ansible_playbook）的执行实现：把计划任务转成
// ansible_run 异步任务（凭据/inventory/输出全在任务管线），提交后轮询终态并把 RECAP
// 计数汇总进执行历史。动作分发入口 runAction 在 cron_actions.go，调度与落库在 cron.go。
package cron

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jiuzhao/vmops/model"
)

// AnsiblePlaybookMaxWait 定时 playbook 执行的兜底等待：转任务异步跑，这里轮询终态。
const AnsiblePlaybookMaxWait = 25 * time.Minute

// AnsiblePlaybookIDRe playbook 文件 id 白名单（handler 校验与包内执行共用同一规约）。
var AnsiblePlaybookIDRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

// runAnsiblePlaybook 定时执行 playbook（P4-S3）：params 形如
// {"playbook":"sysctl-tuning","targets":[89,90]} 或 {"playbook":"...","group_id":3}
// （group_id 走主机组，成员动态生效——改组即生效，不再存字面 targets）。
// 转 ansible_run 异步任务执行（凭据/inventory/输出全在任务管线），提交后轮询终态并汇总 RECAP 进执行历史。
// 调度与引擎各司其职：cron 回答「什么时候跑」，ansible_run 回答「怎么跑」。
func (s *Scheduler) runAnsiblePlaybook(st model.ScheduledTask) (string, error) {
	if s.TaskMgr == nil {
		return "", fmt.Errorf("任务系统未接入（TaskMgr 为空）")
	}
	var params struct {
		Playbook string `json:"playbook"`
		Targets  []uint `json:"targets"`
		GroupID  uint   `json:"group_id"`
	}
	if err := json.Unmarshal([]byte(st.Params), &params); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}
	if params.Playbook == "" || (len(params.Targets) == 0 && params.GroupID == 0) {
		return "", fmt.Errorf("参数不完整（需要 playbook 与 targets 或 group_id）")
	}
	payload := map[string]interface{}{"playbook": params.Playbook}
	if len(params.Targets) > 0 {
		payload["targets"] = params.Targets
	}
	if params.GroupID > 0 {
		payload["group_id"] = params.GroupID
	}

	username := "cron"
	task, err := s.TaskMgr.Submit("ansible_run",
		fmt.Sprintf("计划任务 %s（playbook %s）", st.Name, params.Playbook),
		payload, nil, username, "", nil)
	if err != nil {
		return "", fmt.Errorf("提交 ansible_run 任务失败: %w", err)
	}

	// 轮询终态（execute 持有 execMu，此处分钟级等待会让并发的其他计划任务排队——
	// 当前规模单机 few jobs，可接受；作业多了再改非阻塞）
	deadline := time.Now().Add(AnsiblePlaybookMaxWait)
	for {
		time.Sleep(5 * time.Second)
		var t model.Task
		if qerr := s.DB.First(&t, task.ID).Error; qerr != nil {
			return "", fmt.Errorf("读取任务状态失败: %w", qerr)
		}
		switch t.Status {
		case model.TaskStatusSuccess:
			return s.summarizeAnsibleRun(params.Playbook, t.Result)
		case model.TaskStatusCancelled:
			// 取消也是终态：不处理会一直轮询到 25 分钟超时，期间占着 execMu
			return "", fmt.Errorf("playbook %s 执行被取消", params.Playbook)
		case model.TaskStatusFailed:
			msg := t.Error
			if msg == "" {
				msg = "任务失败（无错误详情）"
			}
			return "", fmt.Errorf("playbook %s 执行失败: %s", params.Playbook, msg)
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("等待任务 %d 超时（%s）", task.ID, AnsiblePlaybookMaxWait)
		}
	}
}

// summarizeAnsibleRun 从任务 Result JSON 提取 RECAP 计数做执行历史摘要。
func (s *Scheduler) summarizeAnsibleRun(playbook, result string) (string, error) {
	var res struct {
		Recap map[string]map[string]int `json:"recap"`
	}
	failedTotal := 0
	hostLines := make([]string, 0, len(res.Recap))
	if result != "" {
		if err := json.Unmarshal([]byte(result), &res); err == nil {
			hosts := make([]string, 0, len(res.Recap))
			for h := range res.Recap {
				hosts = append(hosts, h)
			}
			sort.Strings(hosts)
			for _, h := range hosts {
				c := res.Recap[h]
				hostLines = append(hostLines, fmt.Sprintf("%s ok=%d changed=%d failed=%d unreachable=%d",
					h, c["ok"], c["changed"], c["failed"], c["unreachable"]))
				failedTotal += c["failed"] + c["unreachable"]
			}
		}
	}
	summary := fmt.Sprintf("playbook %s 执行完成（task 已入库）", playbook)
	if len(hostLines) > 0 {
		summary += ": " + strings.Join(hostLines, "; ")
	}
	if failedTotal > 0 {
		return summary, fmt.Errorf("playbook %s 有 %d 台主机失败", playbook, failedTotal)
	}
	return summary, nil
}
