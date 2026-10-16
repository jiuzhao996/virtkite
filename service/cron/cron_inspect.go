// 本文件负责「只读巡检并通知」类定时动作：镜像版本巡检（image_version_check，比对
// 本地镜像 digest 与远端，outdated 时推送镜像名列表）与设计器漂移巡检（designer_drift_check，
// 调注入的对比回调，有漂移则通知）。动作分发入口 runAction 在 cron_actions.go。
package cron

import (
	"fmt"
	"log"
	"runtime/debug"
	"strings"

	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/dockerx"
	"github.com/jiuzhao/vmops/service/imgver"
	"github.com/jiuzhao/vmops/service/notify"
)

// runImageVersionCheck 镜像版本巡检（R9）：复用 imgver 的 digest 对比，
// outdated>0 时经 NotifyURL 推送镜像名列表（截前 10）。检查本身只读，失败即报错。
func (s *Scheduler) runImageVersionCheck(st model.ScheduledTask) (string, error) {
	d := dockerx.New()
	runner := &imgver.Runner{
		ListImages: func() ([][2]string, error) {
			imgs, err := d.Images()
			if err != nil {
				return nil, err
			}
			out := make([][2]string, 0, len(imgs))
			for _, im := range imgs {
				out = append(out, [2]string{im.Repository, im.Tag})
			}
			return out, nil
		},
		LocalDigest: func(image string) string {
			info, err := d.Inspect(image)
			if err != nil {
				return ""
			}
			if arr, ok := info["RepoDigests"].([]interface{}); ok {
				for _, dg := range arr {
					if sv, ok := dg.(string); ok && strings.Contains(sv, "@sha256:") {
						return sv[strings.Index(sv, "@sha256:")+len("@sha256:"):]
					}
				}
			}
			return ""
		},
	}
	items, err := runner.CheckAll()
	if err != nil {
		return "", fmt.Errorf("镜像版本检测失败: %w", err)
	}
	outdated := imgver.Outdated(items)
	if len(outdated) == 0 {
		return fmt.Sprintf("巡检 %d 个镜像，全部为最新", len(items)), nil
	}
	shown := outdated
	extra := ""
	if len(outdated) > 10 {
		shown = outdated[:10]
		extra = fmt.Sprintf(" 等 %d 个", len(outdated))
	}
	summary := fmt.Sprintf("巡检 %d 个镜像，%d 个有更新：%s%s", len(items), len(outdated), strings.Join(shown, "、"), extra)
	notifyVersion(summary)
	return summary, nil
}

// runDesignerDrift 设计器漂移巡检（DE2）：调注入的对比回调，有漂移则通知并进摘要。
// 回调未注入（非 routes 装配路径，如单测）时如实报错，不静默成功。
func (s *Scheduler) runDesignerDrift(st model.ScheduledTask) (string, error) {
	if s.DriftCheck == nil {
		return "", fmt.Errorf("漂移巡检未接入（DriftCheck 回调未注入）")
	}
	checked, drifted, summary := s.DriftCheck()
	if drifted > 0 {
		notifyDrift(summary)
	}
	return fmt.Sprintf("已检查 %d 个计划，漂移 %d 个\n%s", checked, drifted, summary), nil
}

// notifyDrift 漂移告警推送（经 NotifyURL；为空则不推）。
func notifyDrift(summary string) {
	url := NotifyURL
	if url == "" {
		return
	}
	text := "[鸢航VirtKite] 架构漂移告警\n" + truncateRunes(summary, notifyErrLimit*4)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[cron] 漂移通知协程 panic: %v\n%s", r, debug.Stack())
			}
		}()
		if err := notify.SendText(url, text); err != nil {
			log.Printf("[cron] 漂移通知推送失败: %v", err)
		}
	}()
}

// notifyVersion 镜像更新通知（经 NotifyURL 推送；为空则不推）。
func notifyVersion(summary string) {
	url := NotifyURL
	if url == "" {
		return
	}
	text := "[鸢航VirtKite] 镜像版本巡检: " + summary
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[cron] 镜像版本通知协程 panic: %v\n%s", r, debug.Stack())
			}
		}()
		if err := notify.SendText(url, text); err != nil {
			log.Printf("[cron] 镜像版本通知推送失败: %v", err)
		}
	}()
}
