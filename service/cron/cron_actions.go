// 本文件是计划任务「动作」的分发中枢：定义动作类型白名单（model.ScheduledTask.Action
// 的合法取值）与 runAction 按动作类型分发。各动作的具体执行实现按关注点拆到同包多文件：
// 快照与数据库备份见 cron_backup.go，容器健康巡检见 cron_healthcheck.go，
// 定时 playbook 见 cron_ansible.go，镜像版本巡检与设计器漂移巡检见 cron_inspect.go。
// 调度与落库（什么时候跑、跑完怎么记）在 cron.go。
package cron

import (
	"fmt"

	"github.com/jiuzhao/vmops/model"
)

// 动作类型白名单（model.ScheduledTask.Action 的合法取值）。
const (
	ActionVMSnapshot           = "vm_snapshot"           // 定时对指定虚拟机打快照
	ActionDBBackup             = "db_backup"             // 定时备份数据库（mysqldump）
	ActionAnsiblePlaybook      = "ansible_playbook"      // 定时执行 playbook（P4-S3：调度×引擎缝合）
	ActionContainerHealthcheck = "container_healthcheck" // 容器健康巡检：unhealthy 自动重启（R8）
	ActionImageVersionCheck    = "image_version_check"   // 镜像版本巡检：outdated 时通知（R9）
	// ActionDesignerDrift 设计器漂移巡检（DE2）：快照 vs 现实，差异进 cron_runs + 通知
	ActionDesignerDrift = "designer_drift_check"
)

// runAction 按动作类型分发执行，返回摘要与错误。
func (s *Scheduler) runAction(st model.ScheduledTask) (string, error) {
	switch st.Action {
	case ActionVMSnapshot:
		return s.runVMSnapshot(st)
	case ActionDBBackup:
		return s.runDBBackup(st)
	case ActionAnsiblePlaybook:
		return s.runAnsiblePlaybook(st)
	case ActionContainerHealthcheck:
		return s.runContainerHealthcheck(st)
	case ActionImageVersionCheck:
		return s.runImageVersionCheck(st)
	case ActionDesignerDrift:
		return s.runDesignerDrift(st)
	default:
		return "", fmt.Errorf("未知动作类型 %q", st.Action)
	}
}
