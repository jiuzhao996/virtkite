// Package dbx 收敛「DB 写失败被静默吞掉」这一类缺陷：提供统一的持久化写入 helper，
// 失败即重试并全程留痕，让状态漂移至少能在日志里被看见。
//
// 背景：项目多处曾写成 `_ = db.Model(&x).Update(...).Error`，或只在 handler 里
// `LogError(c, err)` 后继续当成功往下走。DB 抖动一次就会出现「系统表面正常、实际状态
// 已经漂移、且没有任何告警」——任务永久 running、控制台会话永远 active、
// 回收站状态与 libvirt 实际状态不一致，都属于这一类。
//
// 本包不替调用方决定「写失败要不要影响响应」：能据此改变行为的用 Persist 拿回 error，
// 无处可补的用 PersistBestEffort（留痕已在包内完成）。
package dbx

import (
	"errors"
	"fmt"
	"log"
	"time"

	"gorm.io/gorm"
)

const (
	// maxAttempts 写入最大尝试次数（首次 + 2 次重试）。
	maxAttempts = 3
	// retryBackoff 退避基数：第 n 次失败后睡 n*基数（100ms/200ms），三次累计约 300ms，
	// 与 service/tasks 终态写入的既有节奏一致——既覆盖绝大多数 DB 瞬时抖动，
	// 也不会把调用方（HTTP 请求 / WS 关闭路径）拖住。
	retryBackoff = 100 * time.Millisecond
)

// errNilDB DB 句柄为空时的哨兵错误：直接返回而不执行 fn，
// 避免调用方漏注入 DB 时 fn 内部 nil 指针 panic 把进程带走。
var errNilDB = errors.New("DB 句柄为空")

// Persist 执行一次幂等写入 fn：失败重试 3 次（退避 100ms/200ms），
// 每次失败与最终失败都打日志，三次全败返回包装后的错误（错误链用 %w 保留）。
//
// ⚠ 幂等要求（务必读）：fn 只能是「等值写入」——按主键 UPDATE 成固定值
// （如 status='closed'、last_login=now），重复执行结果一致，重试才安全。
// 不保证幂等的操作（INSERT、计数累加、CAS、发消息、改外部系统状态）不要传进来：
// 重试会让它们执行多次。本项目现有落点均为等值写入（GORM Model + Update/Updates）。
//
// scene 只用于日志区分场景（如「控制台会话收口」），不参与 SQL。
//
// 返回值：全部失败时返回最后一次错误（已用 `[persist] !!! ` 前缀打过醒目日志）。
// 调用方若能据此改变行为（如回给前端「部分成功」）就处理它；
// 若确实无处可补，请改用 PersistBestEffort，不要把这个返回值丢给 `_ =`——
// 那正是本包要消灭的写法。
func Persist(db *gorm.DB, scene string, fn func() error) error {
	if db == nil {
		log.Printf("[persist] !!! 写入最终失败（DB 句柄为空，未执行写入）场景=%s err=%v", scene, errNilDB)
		return errNilDB
	}
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		lastErr = fn()
		if lastErr == nil {
			if attempt > 1 {
				log.Printf("[persist] 写入重试成功 场景=%s 第 %d 次", scene, attempt)
			}
			return nil
		}
		log.Printf("[persist] 写入失败 场景=%s 第 %d/%d 次 err=%v", scene, attempt, maxAttempts, lastErr)
		if attempt < maxAttempts {
			time.Sleep(retryBackoff * time.Duration(attempt))
		}
	}
	// 醒目留痕：状态已漂移且无告警入口，只能靠这条日志（建议接日志告警匹配 "[persist] !!!"）
	log.Printf("[persist] !!! 写入最终失败（%d 次均失败，状态可能已漂移）场景=%s err=%v",
		maxAttempts, scene, lastErr)
	return fmt.Errorf("持久化失败[场景=%s]: %w", scene, lastErr)
}

// PersistBestEffort 与 Persist 完全相同的重试与留痕，但不返回错误。
//
// 适用「写失败也无处可补」的 best-effort 场景：WS 关闭路径的会话收口、
// 登录成功后的审计时间回写、连通性探测的状态回写——这些点位给返回值只会引出
// 一堆没人处理的 error（或被 `_ =` 吞回老问题），留痕留在包内更可靠。
//
// 注意：它不会因为写失败改变调用方的成败语义（例如登录失败时间写不进去也照样放行）。
func PersistBestEffort(db *gorm.DB, scene string, fn func() error) {
	// 留痕已在 Persist 内按次数与最终结果全程完成，此处确无补救动作，显式丢弃返回值
	_ = Persist(db, scene, fn)
}
