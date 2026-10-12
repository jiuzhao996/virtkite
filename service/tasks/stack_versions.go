package tasks

// stack_versions.go 部署栈 compose 版本历史（栈回滚的数据层）。
//
// 历史存哪：单表 stack_versions（model.StackVersion）。为什么放 service/tasks 而不是
// 新开一个包：栈的部署/升级/回滚都归本包（stack_tasks.go），版本历史是它们的共同副产物；
// handler/stacks.go 的同步 Deploy 也复用本文件的 RecordStackVersion，保证「handler 记一套、
// task 记一套」两份实现不漂移。

import (
	"errors"
	"fmt"
	"log"

	"github.com/jiuzhao/vmops/model"
	"gorm.io/gorm"
)

const (
	// stackVersionKeepN 每栈保留的历史版本数上限（超出即裁剪最旧）。
	// 取 10：对齐 Coolify 的 docker_images_to_keep 默认值 10。compose 文件是 KB 级，
	// 10 份/栈的存储可忽略；10 个版本足以覆盖「改坏 → 回滚 → 再改」的常见迭代节奏。
	stackVersionKeepN = 10
	// stackVersionListLimit 单次列表返回上限（防脏数据堆积时一次拉爆响应）。
	stackVersionListLimit = 50
)

// RecordStackVersion 追加一条栈版本快照并裁剪保留数（stackVersionKeepN）。
//
// 去重：若 content 与「该栈当前最新版本」完全一致，则不新增（重复 redeploy/upgrade 不产生
// 噪音行），直接返回既有最新版本（added=false）。
//
// 版本号 = 当前最大版本号 + 1（各栈独立）。同一栈的写操作由栈级锁串行化
// （service/dockerx 的 stacklock），不会并发插入；即便偶发并发，最多产生两条同号版本，
// 不影响回滚（回滚按行 id 定位目标，不按版本号）。
//
// 返回 (版本记录, 是否新增, error)。
func RecordStackVersion(db *gorm.DB, stackID, content, summary, username string, userID *uint, rollbackFrom *int) (*model.StackVersion, bool, error) {
	if db == nil {
		return nil, false, errors.New("数据库未就绪")
	}
	if stackID == "" {
		return nil, false, errors.New("栈 ID 为空")
	}

	var latest model.StackVersion
	err := db.Where("stack_id = ?", stackID).Order("version DESC, id DESC").First(&latest).Error
	switch {
	case err == nil:
		if latest.Content == content {
			// 内容与最新版本一致：不重复记录（no-op 部署/升级/回滚）。
			return &latest, false, nil
		}
	case !errors.Is(err, gorm.ErrRecordNotFound):
		return nil, false, fmt.Errorf("读取最新版本失败: %w", err)
	}

	ver := &model.StackVersion{
		StackID:      stackID,
		Version:      latest.Version + 1, // 无历史时 latest.Version 为 0 → 从 1 起
		Content:      content,
		Summary:      summary,
		Username:     username,
		UserID:       userID,
		RollbackFrom: rollbackFrom,
	}
	if err := db.Create(ver).Error; err != nil {
		return nil, false, fmt.Errorf("写入版本记录失败: %w", err)
	}
	if err := trimStackVersions(db, stackID); err != nil {
		// 裁剪失败不影响本次记录（已入库），仅留痕：下次记录时会再裁一次。
		log.Printf("[tasks] 栈版本裁剪失败 stack=%s err=%v", stackID, err)
	}
	return ver, true, nil
}

// trimStackVersions 裁剪：只保留该栈最近 stackVersionKeepN 个版本，删更旧的。
// 先查出要保留的行 id 再按「不在保留集」删除——两段式在 MySQL 上比同表子查询删除更直白，
// 也便于单测断言裁剪边界。
func trimStackVersions(db *gorm.DB, stackID string) error {
	var keepIDs []uint
	if err := db.Model(&model.StackVersion{}).
		Where("stack_id = ?", stackID).
		Order("version DESC, id DESC").
		Limit(stackVersionKeepN).
		Pluck("id", &keepIDs).Error; err != nil {
		return fmt.Errorf("查询保留版本失败: %w", err)
	}
	if len(keepIDs) == 0 {
		// 防御：正常流程刚插入至少一行，绝不在空集时误删整表。
		return nil
	}
	if err := db.Where("stack_id = ? AND id NOT IN ?", stackID, keepIDs).
		Delete(&model.StackVersion{}).Error; err != nil {
		return fmt.Errorf("删除旧版本失败: %w", err)
	}
	return nil
}

// ListStackVersions 列出某栈的历史版本（版本号降序，含内容，供前端「查看内容」直显）。
func ListStackVersions(db *gorm.DB, stackID string) ([]model.StackVersion, error) {
	if db == nil {
		return nil, errors.New("数据库未就绪")
	}
	items := []model.StackVersion{}
	if err := db.Where("stack_id = ?", stackID).
		Order("version DESC, id DESC").
		Limit(stackVersionListLimit).
		Find(&items).Error; err != nil {
		return nil, fmt.Errorf("查询版本历史失败: %w", err)
	}
	return items, nil
}

// GetStackVersion 按行 id 读取单个版本（回滚目标定位）。id 为 uint，走参数化查询（防注入）。
func GetStackVersion(db *gorm.DB, id uint) (*model.StackVersion, error) {
	if db == nil {
		return nil, errors.New("数据库未就绪")
	}
	var v model.StackVersion
	if err := db.First(&v, id).Error; err != nil {
		return nil, fmt.Errorf("读取版本失败: %w", err)
	}
	return &v, nil
}
