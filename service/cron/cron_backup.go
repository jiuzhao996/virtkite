// 本文件负责「备份类」定时动作的执行实现：虚拟机快照（vm_snapshot）与数据库备份
// （db_backup），含各自的保留份数清理与配置兜底。动作分发入口 runAction 在 cron_actions.go，
// 调度与落库在 cron.go。
package cron

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/virt"
)

const (
	// defaultBackupDir 数据库备份默认输出目录（Scheduler.BackupDir 为空时使用）。
	defaultBackupDir = "/home/jiuzhao/vmops/data/backup"
	// dbContainerName 备份目标容器：mysqldump 在 MySQL 容器内执行（宿主机不装 mysql 客户端）。
	dbContainerName = "vmops-mysql"
	// defaultDBUser / defaultDBName：配置缺失或 config 未初始化（如单测）时的兜底，与 config 默认值一致。
	defaultDBUser = "vmops"
	defaultDBName = "vmops"
	// mysqlPwdEnv 口令传给 mysqldump 的环境变量名：mysqldump 原生识别，避免口令出现在命令行
	// 参数里（容器内 ps / docker inspect 都能看到 -p<明文>）。
	mysqlPwdEnv = "MYSQL_PWD"
	// dbBackupTimeout mysqldump 最长执行时间，防容器假死把调度协程吊死。
	dbBackupTimeout = 10 * time.Minute
	// snapshotPrefix 定时快照的命名前缀；保留份数清理只针对该前缀，手工快照不受影响。
	snapshotPrefix = "cron-"
)

// runVMSnapshot 执行定时快照：params JSON 形如 {"vm_id":14}，快照名 cron-<时间戳>。
func (s *Scheduler) runVMSnapshot(st model.ScheduledTask) (string, error) {
	var params struct {
		VMID uint `json:"vm_id"`
	}
	if strings.TrimSpace(st.Params) == "" {
		return "", fmt.Errorf("参数为空，vm_snapshot 需要 {\"vm_id\":N}")
	}
	if err := json.Unmarshal([]byte(st.Params), &params); err != nil {
		return "", fmt.Errorf("解析参数失败: %w", err)
	}
	if params.VMID == 0 {
		return "", fmt.Errorf("参数缺少有效的 vm_id")
	}
	var vm model.VM
	if err := s.DB.First(&vm, params.VMID).Error; err != nil {
		return "", fmt.Errorf("虚拟机 %d 不存在: %w", params.VMID, err)
	}
	snapName := snapshotPrefix + time.Now().Format("20060102-150405")
	if err := s.Virt.CreateSnapshot(vm.Name, snapName, "计划任务自动快照"); err != nil {
		return "", fmt.Errorf("创建快照失败: %w", err)
	}
	log.Printf("[cron] 快照创建成功 vm=%s(%d) snapshot=%s", vm.Name, vm.ID, snapName)

	summary := fmt.Sprintf("快照 %s 创建成功", snapName)
	// 保留份数：超出部分的旧 cron- 快照按创建时间从老到新删除（best-effort，不阻断成功语义）
	if removed := s.pruneVMSnapshots(vm.Name, keepOrDefault(st.Keep)); removed > 0 {
		summary += fmt.Sprintf("，已清理过期快照 %d 份", removed)
	}
	return summary, nil
}

// pruneVMSnapshots 只保留最近 keep 份 cron- 前缀快照，更老的按创建时间从新到旧排序后
// 删除（即最老的先删）。只动 snapshotPrefix 前缀，手工创建的快照不受影响。
// 清理属 best-effort：列举失败或单个删除失败只记日志继续，不清理干净不影响本次
// 快照已成功的语义。返回实际删除份数（供摘要文案）。
func (s *Scheduler) pruneVMSnapshots(vmName string, keep int) int {
	snaps, err := s.Virt.ListSnapshots(vmName)
	if err != nil {
		log.Printf("[cron] 列举快照失败 vm=%s: %v", vmName, err)
		return 0
	}
	cronSnaps := make([]virt.SnapshotInfo, 0, len(snaps))
	for _, sn := range snaps {
		if strings.HasPrefix(sn.Name, snapshotPrefix) {
			cronSnaps = append(cronSnaps, sn)
		}
	}
	if len(cronSnaps) <= keep {
		return 0
	}
	// 创建时间倒序（新的在前）；同秒冲突按名字字典序兜底，保证删除顺序确定
	sort.Slice(cronSnaps, func(i, j int) bool {
		if cronSnaps[i].CreationTime != cronSnaps[j].CreationTime {
			return cronSnaps[i].CreationTime > cronSnaps[j].CreationTime
		}
		return cronSnaps[i].Name > cronSnaps[j].Name
	})
	removed := 0
	for _, sn := range cronSnaps[keep:] {
		if err := s.Virt.DeleteSnapshot(vmName, sn.Name); err != nil {
			log.Printf("[cron] 清理过期快照失败 vm=%s snapshot=%s: %v", vmName, sn.Name, err)
			continue
		}
		log.Printf("[cron] 已清理过期快照 vm=%s snapshot=%s", vmName, sn.Name)
		removed++
	}
	return removed
}

// runDBBackup 执行数据库备份：docker exec 进 MySQL 容器跑 mysqldump，
// 输出落 BackupDir/vmops-YYYYMMDD-HHMM.sql，并只保留最近 keep（st.Keep，<=0 视为 7）份。
// 返回成果摘要供执行历史 output 使用。
func (s *Scheduler) runDBBackup(st model.ScheduledTask) (string, error) {
	dir := s.BackupDir
	if dir == "" {
		dir = defaultBackupDir
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建备份目录失败: %w", err)
	}
	outPath := filepath.Join(dir, "vmops-"+time.Now().Format("20060102-1504")+".sql")
	f, err := os.Create(outPath)
	if err != nil {
		return "", fmt.Errorf("创建备份文件失败: %w", err)
	}
	defer f.Close()

	// 备份用的库账号与口令一律取自全局配置（DB_USER / DB_NAME / DB_PASSWORD），不再写死：
	// 硬编码口令会与配置形成隐式耦合——运维按配置改了库口令后，定时任务静默失败（错误只进
	// 任务日志），真要恢复时才发现没有一份可用备份。
	user, dbName, password := dbBackupCreds()

	ctx, cancel := context.WithTimeout(context.Background(), dbBackupTimeout)
	defer cancel()
	// 参数必须走数组形式（不经过 shell 拼接）；等价命令：
	//   docker exec -e MYSQL_PWD=*** vmops-mysql mysqldump -uvmops vmops
	args := []string{"exec"}
	if password != "" {
		args = append(args, "-e", mysqlPwdEnv+"="+password)
	} else {
		// 口令为空时不能拼出裸 "-p"：mysqldump 会把它当成「等待交互输入口令」而挂到超时，
		// 表现为备份任务假死且无输出，比直接报连接失败更难排查。
		log.Printf("[cron] 警告：DB_PASSWORD 为空，mysqldump 将以无口令方式连接；若库要求口令请配置 DB_PASSWORD")
	}
	args = append(args, dbContainerName, "mysqldump", "-u"+user, dbName)
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout = f
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		// errBuf 可能回显命令片段，故只截取有限长度；口令经 env 传递不会出现在命令行里
		return "", fmt.Errorf("mysqldump 执行失败: %w（stderr: %s）", err, truncateRunes(strings.TrimSpace(errBuf.String()), 500))
	}
	log.Printf("[cron] 数据库备份完成 file=%s", outPath)

	keep := keepOrDefault(st.Keep)
	s.pruneBackups(dir, keep)
	return fmt.Sprintf("备份完成 %s（已保留最近 %d 份）", outPath, keep), nil
}

// dbBackupCreds 取备份用的数据库账号、库名与口令（供 runDBBackup 拼 mysqldump）。
// config 未初始化（单测或独立调用）时回退到保守默认值，避免 nil 解引用 panic。
func dbBackupCreds() (user, dbName, password string) {
	user, dbName = defaultDBUser, defaultDBName
	if config.GlobalConfig == nil {
		return user, dbName, ""
	}
	if config.GlobalConfig.DBUser != "" {
		user = config.GlobalConfig.DBUser
	}
	if config.GlobalConfig.DBName != "" {
		dbName = config.GlobalConfig.DBName
	}
	return user, dbName, config.GlobalConfig.DBPassword
}

// pruneBackups 只保留最近 keep 份备份（文件名内嵌时间戳，字典序即时间序）。
// 清理属 best-effort，失败只记日志——删不掉旧文件不影响本次备份的有效性。
func (s *Scheduler) pruneBackups(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		log.Printf("[cron] 读取备份目录失败 dir=%s: %v", dir, err)
		return
	}
	backups := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() && strings.HasPrefix(name, "vmops-") && strings.HasSuffix(name, ".sql") {
			backups = append(backups, name)
		}
	}
	if len(backups) <= keep {
		return
	}
	sort.Strings(backups) // vmops-YYYYMMDD-HHMM.sql 字典序 == 时间序
	for _, name := range backups[:len(backups)-keep] {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			log.Printf("[cron] 清理过期备份失败 file=%s: %v", name, err)
			continue
		}
		log.Printf("[cron] 已清理过期备份 file=%s", name)
	}
}

// keepOrDefault 归一化保留份数：未配置（<=0，含 gorm default 未生效的历史行零值）回退 DefaultKeep。
func keepOrDefault(keep int) int {
	if keep <= 0 {
		return DefaultKeep
	}
	return keep
}
