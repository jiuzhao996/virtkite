package monitor

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"

	"github.com/jiuzhao/vmops/model"
	"gorm.io/gorm"
)

// StartFileSDWriter 启动 file_sd 目标文件后台写入协程。
//
// 每个周期查询 vms 表（running 且 IP 非空）→ GenerateFileSD → 序列化 → 原子写入 path
// （先写 <path>.tmp 再 os.Rename，Prometheus 的 file_sd watcher 读到半截 JSON 会报解析错误）。
// path 为空表示功能未启用（FILE_SD_PATH 未配置），直接不启动。
// db 为 nil 同样不启动（防御：单测/特殊装配场景）。
//
// 协程自带 defer recover()：gin Recovery 只覆盖 HTTP 请求链，不覆盖自起协程；
// panic 值与堆栈只进日志。周期内的单次写入也各自兜底 recover，单次失败不终止循环。
func StartFileSDWriter(db *gorm.DB, path string, interval time.Duration) {
	if path == "" || db == nil {
		return
	}
	if interval <= 0 {
		interval = time.Minute
	}
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("[monitor] file_sd 写入协程 panic=%v\n%s", rec, debug.Stack())
			}
		}()

		writeFileSDOnce(db, path) // 启动即先写一轮，不必等第一个周期
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			writeFileSDOnce(db, path)
		}
	}()
	log.Printf("[monitor] file_sd 目标文件写入已启动 path=%s interval=%s", path, interval)
}

// writeFileSDOnce 单轮「查库 → 生成 → 原子落盘」。任何失败只记日志，绝不上抛：
// 监控目标文件写不出去属于可自愈的暂态问题（如目录权限），不该拖垮整个进程。
func writeFileSDOnce(db *gorm.DB, path string) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("[monitor] file_sd 单轮写入 panic=%v\n%s", rec, debug.Stack())
		}
	}()

	var vms []model.VM
	// 软删除的 VM 由 GORM 自动排除；与 handler 预览接口保持同一查询条件
	if err := db.Where("status = ? AND ip <> ?", model.VMStatusRunning, "").Find(&vms).Error; err != nil {
		log.Printf("[monitor] 查询运行中虚拟机失败: %v", err)
		return
	}

	entries := GenerateFileSD(vms)
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		// map[string]interface{} 序列化理论上不会失败，兜底防御
		log.Printf("[monitor] file_sd JSON 序列化失败: %v", err)
		return
	}
	data = append(data, '\n')

	// 目录不存在则创建（首次启用时 deploy/file_sd 可能还没挂载出来）
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Printf("[monitor] 创建 file_sd 目录失败: %v", err)
		return
	}

	// 原子写：先写同目录临时文件再 rename，watcher 不会读到半截文件
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		log.Printf("[monitor] 写入 file_sd 临时文件失败: %v", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		log.Printf("[monitor] file_sd 原子替换失败: %v", err)
		return
	}
	log.Printf("[monitor] file_sd 目标文件已更新 path=%s targets=%d", path, len(entries))
}
