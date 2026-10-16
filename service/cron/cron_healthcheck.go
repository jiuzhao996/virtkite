// 本文件负责「容器健康巡检」定时动作（container_healthcheck）的执行实现：
// docker ps 取运行中容器 → inspect 批量取健康状态 → unhealthy 且连续失败达门限则自动
// 重启 → 审计落行 + 通知。动作分发入口 runAction 在 cron_actions.go，调度与落库在 cron.go。
package cron

import (
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"runtime/debug"
	"strings"
	"time"

	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/notify"
)

// runContainerHealthcheck 容器健康巡检：params JSON 可选 {"notify":true}。
// 流程：docker ps 取运行中容器 → 单次 docker inspect 批量取 State.Health →
// 门限 unhealthy 且 FailingStreak>=3 → docker restart → 通知 + 审计 → 摘要。
// 选 cron action 而非后台常驻 loop：调度/历史/手动触发/启停/失败通知五件套白拿，
// 「看得见的机制」优于「看不见的魔法」；健康检查秒级完成，不转任务管线（避免每分钟
// 一条任务把 TaskList 刷成噪音——这与 ansible_playbook 转任务的取舍有意不同）。
func (s *Scheduler) runContainerHealthcheck(st model.ScheduledTask) (string, error) {
	notifyOn := true
	if strings.TrimSpace(st.Params) != "" {
		var params struct {
			Notify *bool `json:"notify"`
		}
		if err := json.Unmarshal([]byte(st.Params), &params); err == nil && params.Notify != nil {
			notifyOn = *params.Notify
		}
	}

	running, err := s.listRunningContainers()
	if err != nil {
		return "", fmt.Errorf("列举容器失败: %w", err)
	}
	if len(running) == 0 {
		return "无运行中容器，无需巡检", nil
	}
	healths, err := s.inspectContainerHealth(running)
	if err != nil {
		return "", fmt.Errorf("查询容器健康状态失败: %w", err)
	}

	var restarted []string
	for _, h := range healths {
		// FailingStreak>=3 门闩：docker 侧已连续失败 3 次才动手，防抖防重启风暴
		if h.Status != "unhealthy" || h.FailingStreak < 3 {
			continue
		}
		if err := s.restartContainer(h.ID); err != nil {
			log.Printf("[cron] 健康自愈重启失败 container=%s(%s): %v", h.Name, h.ID, err)
			continue
		}
		restarted = append(restarted, h.Name)
		log.Printf("[cron] 容器健康自愈：已重启 %s(%s) 连续失败 %d 次", h.Name, h.ID, h.FailingStreak)
		s.auditHealthRestart(h, st)
	}

	summary := fmt.Sprintf("巡检 %d 个运行中容器，unhealthy %d 个，已重启 %d 个",
		len(running), countUnhealthy(healths), len(restarted))
	if len(restarted) > 0 {
		summary += "：" + strings.Join(restarted, "、")
		if notifyOn {
			notifyHealth(st.Name, summary)
		}
	}
	return summary, nil
}

// containerHealth 单容器健康状态（docker inspect --format '{{json .State.Health}}'）。
type containerHealth struct {
	ID            string
	Name          string
	Status        string // healthy / unhealthy / starting / none
	FailingStreak int
}

func countUnhealthy(hs []containerHealth) int {
	n := 0
	for _, h := range hs {
		if h.Status == "unhealthy" {
			n++
		}
	}
	return n
}

// listRunningContainers 返回运行中容器的 id/name（docker ps --format json）。
func (s *Scheduler) listRunningContainers() ([]containerBasic, error) {
	out, err := exec.Command("docker", "ps", "--format", "{{json .}}").Output()
	if err != nil {
		return nil, err
	}
	var list []containerBasic
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var c struct {
			ID    string `json:"ID"`
			Names string `json:"Names"`
		}
		if json.Unmarshal([]byte(line), &c) != nil {
			continue
		}
		list = append(list, containerBasic{ID: c.ID, Name: strings.TrimPrefix(c.Names, "/")})
	}
	return list, nil
}

// containerBasic 巡检所需的容器最小信息。
type containerBasic struct {
	ID   string
	Name string
}

// inspectContainerHealth 单次进程批量取健康状态（docker inspect --format '{{json .State.Health}}' id1 id2…）。
// 无健康检查的容器（State.Health 为 null）返回 Status=none，不参与重启判定。
func (s *Scheduler) inspectContainerHealth(containers []containerBasic) ([]containerHealth, error) {
	args := []string{"inspect", "--format", "{{json .State.Health}}"}
	nameByID := map[string]string{}
	for _, c := range containers {
		args = append(args, c.ID)
		nameByID[c.ID[:12]] = c.Name
	}
	out, err := exec.Command("docker", args...).Output()
	if err != nil {
		return nil, err
	}
	var result []containerHealth
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "null" {
			continue
		}
		var h struct {
			Status        string `json:"Status"`
			FailingStreak int    `json:"FailingStreak"`
		}
		if json.Unmarshal([]byte(line), &h) != nil {
			continue
		}
		result = append(result, containerHealth{Status: h.Status, FailingStreak: h.FailingStreak})
	}
	// docker inspect 多目标输出顺序与入参一致：按序回填 id/name
	for i := range result {
		if i < len(containers) {
			result[i].ID = containers[i].ID
			result[i].Name = containers[i].Name
		}
	}
	return result, nil
}

// restartContainer 重启容器（等价 docker restart）。
func (s *Scheduler) restartContainer(id string) error {
	return exec.Command("docker", "restart", id).Run()
}

// auditHealthRestart 容器自愈动作落审计（照 jumpd 手动落行先例：后台动作不经 HTTP 中间件）。
func (s *Scheduler) auditHealthRestart(h containerHealth, st model.ScheduledTask) {
	audit := model.AuditLog{
		Username:   "system",
		Action:     "container_healthcheck",
		ObjectType: "docker",
		Detail:     fmt.Sprintf("健康自愈：重启容器 %s（连续失败 %d 次，计划任务「%s」）", h.Name, h.FailingStreak, st.Name),
		Status:     "ok",
		CreatedAt:  time.Now(),
	}
	if err := s.DB.Create(&audit).Error; err != nil {
		log.Printf("[cron] 健康自愈审计落行失败 container=%s: %v", h.Name, err)
	}
}

// notifyHealth 健康自愈通知（经 NotifyURL 推送；为空则不推）。
func notifyHealth(taskName, summary string) {
	url := NotifyURL
	if url == "" {
		return
	}
	text := fmt.Sprintf("[鸢航VirtKite] 容器健康自愈: %s %s", taskName, summary)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[cron] 健康自愈通知协程 panic: %v\n%s", r, debug.Stack())
			}
		}()
		if err := notify.SendText(url, text); err != nil {
			log.Printf("[cron] 健康自愈通知推送失败: %v", err)
		}
	}()
}
