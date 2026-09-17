// Package cron 提供计划任务能力：5 字段 cron 表达式解析/匹配，以及整点 tick 的调度器。
//
// 与 robfig/cron 等三方库的取舍：本项目只需要「分 时 日 月 周」五字段的最小子集
// （无秒/年/时区/@daily 别名），纯函数约百行即可覆盖且可单测，不引第三方依赖。
// 匹配语义为五字段全 AND（与 robfig 标准版在 dom/dow 同时受限时的 OR 行为不同，
// 本项目场景不存在该用法），详见 Match 注释。
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
	"runtime/debug"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/virt"
	"gorm.io/gorm"
)

// 动作类型白名单（model.ScheduledTask.Action 的合法取值）。
const (
	ActionVMSnapshot = "vm_snapshot" // 定时对指定虚拟机打快照
	ActionDBBackup   = "db_backup"   // 定时备份数据库（mysqldump）
)

const (
	// defaultBackupDir 数据库备份默认输出目录（Scheduler.BackupDir 为空时使用）。
	defaultBackupDir = "/home/jiuzhao/vmops/data/backup"
	// backupKeep 保留最近 N 份备份，更老的按文件名（含时间戳，字典序即时间序）删除。
	backupKeep = 7
	// dbBackupTimeout mysqldump 最长执行时间，防容器假死把调度协程吊死。
	dbBackupTimeout = 10 * time.Minute
	// maxLookaheadDays Next 逐分钟探测的最大天数，防「永不匹配的表达式」（如 2 月 31 日）死循环。
	maxLookaheadDays = 366
)

// Spec 解析后的 cron 表达式，五个字段各自是「允许值」的升序去重集合。
type Spec struct {
	Min  []int // 分钟 0-59
	Hour []int // 小时 0-23
	Dom  []int // 日 1-31（不感知月份天数，2 月 30/31 日这类永不成立，Next 会返回零值）
	Mon  []int // 月 1-12
	Dow  []int // 周 0-6（0=周日，与 crontab 一致）
}

// ParseCron 解析 5 字段 cron 表达式（分 时 日 月 周，空格分隔）。
// 每个字段支持：* 、*/n 步进、固定值、逗号列表（1,15）、闭区间范围（a-b，可再带 /n 步进）。
// 解析失败返回中文错误（固定文案，可直接回显给前端），错误消息不含冒号，
// 以免被 handler.friendlyMessage 截断。
func ParseCron(expr string) (*Spec, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron 表达式必须为 5 个空格分隔的字段（分 时 日 月 周），当前 %d 个", len(fields))
	}
	// 字段顺序与取值域固定，逐个解析
	domains := []struct {
		raw  string
		lo   int
		hi   int
		name string
	}{
		{fields[0], 0, 59, "分钟"},
		{fields[1], 0, 23, "小时"},
		{fields[2], 1, 31, "日"},
		{fields[3], 1, 12, "月"},
		{fields[4], 0, 6, "周"},
	}
	vals := make([][]int, 0, 5)
	for _, d := range domains {
		v, err := parseField(d.raw, d.lo, d.hi, d.name)
		if err != nil {
			return nil, err
		}
		vals = append(vals, v)
	}
	return &Spec{Min: vals[0], Hour: vals[1], Dom: vals[2], Mon: vals[3], Dow: vals[4]}, nil
}

// parseField 解析单个字段（逗号列表，每段交给 parsePart），结果升序去重。
func parseField(field string, lo, hi int, name string) ([]int, error) {
	seen := make(map[int]bool)
	for _, part := range strings.Split(field, ",") {
		if part == "" {
			return nil, fmt.Errorf("%s字段 %q 存在空片段", name, field)
		}
		vals, err := parsePart(part, lo, hi, name)
		if err != nil {
			return nil, err
		}
		for _, v := range vals {
			seen[v] = true
		}
	}
	out := make([]int, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}
	sort.Ints(out)
	return out, nil
}

// parsePart 解析字段中的一个片段，返回展开后的取值集合（升序）。
// 支持：* 、*/n 、固定值 、固定值/n（从该值步进到上限）、a-b 、a-b/n。
// 取值域校验同时挡掉负数（lo ≥ 0），故 strconv.Atoi 结果直接参与比较即可。
func parsePart(part string, lo, hi int, name string) ([]int, error) {
	base, step := part, 1
	if i := strings.Index(part, "/"); i >= 0 {
		base = part[:i]
		stepStr := part[i+1:]
		if stepStr == "" {
			return nil, fmt.Errorf("%s字段 %q 的步长缺失", name, part)
		}
		n, err := strconv.Atoi(stepStr)
		if err != nil {
			return nil, fmt.Errorf("%s字段 %q 的步长 %q 不是正整数", name, part, stepStr)
		}
		if n <= 0 {
			return nil, fmt.Errorf("%s字段 %q 的步长必须为正整数", name, part)
		}
		step = n
	}

	var start, end int
	switch {
	case base == "*":
		start, end = lo, hi
	case strings.Contains(base, "-"):
		bounds := strings.SplitN(base, "-", 2)
		a, err1 := strconv.Atoi(bounds[0])
		b, err2 := strconv.Atoi(bounds[1])
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("%s字段 %q 的范围不是合法数字", name, part)
		}
		if a < lo || a > hi {
			return nil, fmt.Errorf("%s字段范围起始 %d 超出取值域 %d-%d", name, a, lo, hi)
		}
		if b < lo || b > hi {
			return nil, fmt.Errorf("%s字段范围结束 %d 超出取值域 %d-%d", name, b, lo, hi)
		}
		if a > b {
			return nil, fmt.Errorf("%s字段范围 %q 起始不能大于结束", name, part)
		}
		start, end = a, b
	default:
		v, err := strconv.Atoi(base)
		if err != nil {
			return nil, fmt.Errorf("%s字段 %q 不是合法取值（支持 * 、*/n 、固定值 、a-b 范围与逗号列表）", name, part)
		}
		if v < lo || v > hi {
			return nil, fmt.Errorf("%s字段取值 %d 超出取值域 %d-%d", name, v, lo, hi)
		}
		// 固定值带步进（v/n）：从 v 起步进到上限，与 Vixie cron 行为一致；无步进则单值
		start, end = v, hi
		if step == 1 && !strings.Contains(part, "/") {
			end = v
		}
	}

	out := make([]int, 0, (end-start)/step+1)
	for v := start; v <= end; v += step {
		out = append(out, v)
	}
	return out, nil
}

// contains 判断 v 是否在升序集合中（集合最大 60 个元素，线性扫描足够）。
func contains(set []int, v int) bool {
	return slices.Contains(set, v)
}

// Match 判断时刻 t 是否命中表达式：五字段全 AND（分/时/日/月/周全部命中才算）。
// 注意与标准 crontab 的差异：日、周同时受限时标准 cron 取 OR，这里始终 AND——
// 本项目下拉/模板生成的表达式不会同时受限两者，语义等价且实现更直白。
// spec 为 nil 时恒不命中（防御，正常路径 spec 均来自 ParseCron）。
func Match(spec *Spec, t time.Time) bool {
	if spec == nil {
		return false
	}
	return contains(spec.Min, t.Minute()) &&
		contains(spec.Hour, t.Hour()) &&
		contains(spec.Dom, t.Day()) &&
		contains(spec.Mon, int(t.Month())) &&
		contains(spec.Dow, int(t.Weekday()))
}

// minuteFloor 把时刻截断到整分（保留原时区；不用 time.Truncate——那是按 UTC 绝对时间截断）。
func minuteFloor(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, t.Location())
}

// Next 返回严格晚于 from 的下一个匹配时刻（逐分钟探测，最多向前找 366 天）。
// 找不到（如 "0 0 31 2 *"——2 月 31 日不存在）返回零值 time.Time{}，调用方须判 IsZero。
func Next(spec *Spec, from time.Time) time.Time {
	if spec == nil {
		return time.Time{}
	}
	start := minuteFloor(from).Add(time.Minute) // 严格晚于 from
	limit := minuteFloor(from).AddDate(0, 0, maxLookaheadDays)
	for t := start; t.Before(limit); t = t.Add(time.Minute) {
		if Match(spec, t) {
			return t
		}
	}
	return time.Time{}
}

// Scheduler 计划任务调度器：整点 tick → 查 enabled 任务 → 表达式匹配 → 执行。
// 由 main 构造并调用 Start（内部自起 goroutine，进程生命周期即调度器生命周期）。
type Scheduler struct {
	DB        *gorm.DB
	Virt      *virt.Virt
	BackupDir string // 数据库备份输出目录，空串用 defaultBackupDir

	// execMu 串行化执行：定时 tick 与 handler 的手动触发（ExecuteNow）可能并发
	// 命中同一任务，mysqldump / 打快照不重入。
	execMu sync.Mutex
}

// Start 启动调度循环（内部自起 goroutine，无需再 go 一层）。
func (s *Scheduler) Start() {
	go s.loop()
}

// loop 调度主循环：sleep 到下一个整分（秒==0）执行一次 tick。
// 顶部 recover 兜底（项目规范：自起协程必须兜底）；单次 tick 的 panic 在 safeTick
// 内已被捕获并继续循环，本层 recover 只作最后防线，正常不可达。
func (s *Scheduler) loop() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[cron] 调度循环异常退出 panic=%v\n%s", r, debug.Stack())
		}
	}()
	for {
		next := minuteFloor(time.Now()).Add(time.Minute)
		time.Sleep(time.Until(next))
		s.safeTick(next)
	}
}

// safeTick 单次扫描，自带 recover：一个任务的 panic 不允许终结调度循环。
func (s *Scheduler) safeTick(now time.Time) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[cron] tick panic 已兜底 now=%s panic=%v\n%s",
				now.Format("2006-01-02 15:04:05"), r, debug.Stack())
		}
	}()
	s.tick(now)
}

// tick 执行一轮调度扫描：取所有启用任务，表达式命中当前整分即执行。
func (s *Scheduler) tick(now time.Time) {
	var tasks []model.ScheduledTask
	if err := s.DB.Where("enabled = ?", true).Find(&tasks).Error; err != nil {
		log.Printf("[cron] 查询计划任务失败: %v", err)
		return
	}
	for _, st := range tasks {
		spec, err := ParseCron(st.CronExpr)
		if err != nil {
			// 创建/更新时已校验，这里只在脏数据（手改库）时发生，记日志跳过
			log.Printf("[cron] 任务 %d(%s) 的 cron 表达式 %q 非法，已跳过: %v", st.ID, st.Name, st.CronExpr, err)
			continue
		}
		if !Match(spec, now) {
			continue
		}
		if err := s.execute(st); err != nil {
			log.Printf("[cron] 计划任务执行失败 id=%d(%s) action=%s: %v", st.ID, st.Name, st.Action, err)
		}
	}
}

// ExecuteNow 立即执行一次计划任务（handler 的手动触发入口），与定时执行共用 execute。
func (s *Scheduler) ExecuteNow(st model.ScheduledTask) error {
	return s.execute(st)
}

// execute 执行单个任务并更新执行统计（LastRun/RunCount），返回业务错误供调用方记日志/回显。
// 执行全程持锁（见 execMu），统计更新失败只记日志不阻断——下次执行仍会正常累计。
func (s *Scheduler) execute(st model.ScheduledTask) error {
	s.execMu.Lock()
	defer s.execMu.Unlock()

	log.Printf("[cron] 执行计划任务 id=%d name=%s action=%s", st.ID, st.Name, st.Action)
	var err error
	switch st.Action {
	case ActionVMSnapshot:
		err = s.runVMSnapshot(st)
	case ActionDBBackup:
		err = s.runDBBackup()
	default:
		err = fmt.Errorf("未知动作类型 %q", st.Action)
	}

	updates := map[string]interface{}{
		"last_run":  time.Now(),
		"run_count": gorm.Expr("run_count + 1"),
	}
	if uerr := s.DB.Model(&model.ScheduledTask{}).Where("id = ?", st.ID).Updates(updates).Error; uerr != nil {
		log.Printf("[cron] 更新执行统计失败 id=%d: %v", st.ID, uerr)
	}
	return err
}

// runVMSnapshot 执行定时快照：params JSON 形如 {"vm_id":14}，快照名 cron-<时间戳>。
func (s *Scheduler) runVMSnapshot(st model.ScheduledTask) error {
	var params struct {
		VMID uint `json:"vm_id"`
	}
	if strings.TrimSpace(st.Params) == "" {
		return fmt.Errorf("参数为空，vm_snapshot 需要 {\"vm_id\":N}")
	}
	if err := json.Unmarshal([]byte(st.Params), &params); err != nil {
		return fmt.Errorf("解析参数失败: %w", err)
	}
	if params.VMID == 0 {
		return fmt.Errorf("参数缺少有效的 vm_id")
	}
	var vm model.VM
	if err := s.DB.First(&vm, params.VMID).Error; err != nil {
		return fmt.Errorf("虚拟机 %d 不存在: %w", params.VMID, err)
	}
	snapName := "cron-" + time.Now().Format("20060102-150405")
	if err := s.Virt.CreateSnapshot(vm.Name, snapName, "计划任务自动快照"); err != nil {
		return fmt.Errorf("创建快照失败: %w", err)
	}
	log.Printf("[cron] 快照创建成功 vm=%s(%d) snapshot=%s", vm.Name, vm.ID, snapName)
	return nil
}

// runDBBackup 执行数据库备份：docker exec 进 MySQL 容器跑 mysqldump，
// 输出落 BackupDir/vmops-YYYYMMDD-HHMM.sql，并只保留最近 backupKeep 份。
func (s *Scheduler) runDBBackup() error {
	dir := s.BackupDir
	if dir == "" {
		dir = defaultBackupDir
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建备份目录失败: %w", err)
	}
	outPath := filepath.Join(dir, "vmops-"+time.Now().Format("20060102-1504")+".sql")
	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("创建备份文件失败: %w", err)
	}
	defer f.Close()

	ctx, cancel := context.WithTimeout(context.Background(), dbBackupTimeout)
	defer cancel()
	// 参数必须走数组形式（不经过 shell 拼接）；等价命令：
	//   docker exec vmops-mysql mysqldump -uvmops -pvmops123 vmops
	cmd := exec.CommandContext(ctx, "docker", "exec", "vmops-mysql",
		"mysqldump", "-uvmops", "-pvmops123", "vmops")
	cmd.Stdout = f
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("mysqldump 执行失败: %w（stderr: %s）", err, strings.TrimSpace(errBuf.String()))
	}
	log.Printf("[cron] 数据库备份完成 file=%s", outPath)

	s.pruneBackups(dir)
	return nil
}

// pruneBackups 只保留最近 backupKeep 份备份（文件名内嵌时间戳，字典序即时间序）。
// 清理属 best-effort，失败只记日志——删不掉旧文件不影响本次备份的有效性。
func (s *Scheduler) pruneBackups(dir string) {
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
	if len(backups) <= backupKeep {
		return
	}
	sort.Strings(backups) // vmops-YYYYMMDD-HHMM.sql 字典序 == 时间序
	for _, name := range backups[:len(backups)-backupKeep] {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			log.Printf("[cron] 清理过期备份失败 file=%s: %v", name, err)
			continue
		}
		log.Printf("[cron] 已清理过期备份 file=%s", name)
	}
}
