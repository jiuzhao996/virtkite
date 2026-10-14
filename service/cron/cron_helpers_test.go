package cron

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/model"
)

// TestKeepOrDefault 保留份数归一化：<=0（含 gorm default 未生效的历史行零值）一律回退 DefaultKeep。
//
// 风险点：Keep 是定时快照/备份的保留份数。历史行可能是 Enable 前的零值，若直接拿 0 去
// 清理，会把所有旧快照删光（0 保留）；这条用例钉死「0 与负数都当默认 7」。
func TestKeepOrDefault(t *testing.T) {
	cases := []struct{ in, want int }{
		{-5, DefaultKeep},
		{0, DefaultKeep},
		{1, 1},
		{7, 7},
		{20, 20},
	}
	for _, c := range cases {
		if got := keepOrDefault(c.in); got != c.want {
			t.Errorf("keepOrDefault(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestTruncateRunes 按字符数截断（错误消息常含中文）。
//
// 风险点：若误按字节截断，中文会切成半个字符的乱码写进 cron_runs.output 与通知文案。
// 三条边界最值钱：limit<=0 原样返回（防御）、恰好在阈值上界不截、超限后总字符数恰好等于 limit。
func TestTruncateRunes(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		limit int
		want  string
	}{
		{"长度以内原样", "abc", 10, "abc"},
		{"恰在上界原样", "abcd", 4, "abcd"},
		{"limit<=0 原样返回", "abcdef", 0, "abcdef"},
		{"limit 为负原样返回", "abcdef", -1, "abcdef"},
		{"ASCII 超限截断", "abcdef", 4, "abc…"},
		{"截到 1 只剩省略号", "abc", 1, "…"},
		// 中文按字符计：4 个中文字（12 字节）在 limit=4 下不应被截
		{"中文按字符不误截", "中文公告", 4, "中文公告"},
		{"中文超限按字符截", "中文字符串", 3, "中文…"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateRunes(tc.in, tc.limit)
			if got != tc.want {
				t.Errorf("truncateRunes(%q, %d) = %q, want %q", tc.in, tc.limit, got, tc.want)
			}
			// 截断后字符数必须恰好等于 limit（保证「截到限制」而非「截多一点」）
			if tc.limit > 0 && len([]rune(got)) != tc.limit && len([]rune(tc.in)) > tc.limit {
				t.Errorf("截断结果字符数 = %d, want %d", len([]rune(got)), tc.limit)
			}
		})
	}
}

// TestMinuteFloor 整分截断必须保留原时区并按本地墙钟去秒，而非按 UTC 绝对时间截断。
//
// 风险点：time.Truncate 是按 UTC 绝对时间截断，非整小时偏移时区（如 UTC+8）会得到
// 错误的分钟；调度器用 minuteFloor 决定「本轮整分」，写错会让任务整体偏移或漏跑。
func TestMinuteFloor(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	t0 := time.Date(2024, 1, 8, 10, 3, 45, 123456789, loc)
	got := minuteFloor(t0)
	want := time.Date(2024, 1, 8, 10, 3, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("minuteFloor(%s) = %s, want %s", t0, got, want)
	}
	if got.Location() != loc {
		t.Errorf("minuteFloor 丢失了原时区：got %v, want %v", got.Location(), loc)
	}
	if got.Second() != 0 || got.Nanosecond() != 0 {
		t.Errorf("分钟截断后秒/纳秒应为 0，实际 %d.%d", got.Second(), got.Nanosecond())
	}
}

// TestNextPreservesLocation Next 的返回值必须与原时区一致（不得悄悄换成 UTC）。
//
// 风险点：Next 内部走 minuteFloor（本地墙钟）。若返回值落入 UTC，跨时区部署下
// 预览端点展示的执行时刻会整体偏移若干小时，用户按北京时间配的任务看着像错的。
func TestNextPreservesLocation(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	spec, err := ParseCron("*/5 * * * *")
	if err != nil {
		t.Fatalf("ParseCron 意外报错: %v", err)
	}
	from := time.Date(2024, 1, 8, 10, 3, 0, 0, loc)
	got := Next(spec, from)
	want := time.Date(2024, 1, 8, 10, 5, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("Next = %s, want %s", got, want)
	}
	if got.Location() != loc {
		t.Errorf("Next 丢失时区：got %v, want %v", got.Location(), loc)
	}
	if got.Hour() != 10 || got.Minute() != 5 {
		t.Errorf("本地墙钟应为 10:05，实际 %s", got.Format("15:04"))
	}
}

// TestParseFieldDomains 逐字段钉死取值域边界（分0-59/时0-23/日1-31/月1-12/周0-6）。
//
// 风险点：parseField/parsePart 是「百行纯函数」的核心，五个字段的 lo/hi 一旦写错
// （如日写成 0-31 或周写成 1-7），非法表达式会被接受并在 Next 里得到错误时刻；
// 且函数只对越界输入报错，边界值本身必须能正常展开。既有 ParseCron 用例只覆盖了
// 部分越界点，这里把每个域的上下界成对钉死。
func TestParseFieldDomains(t *testing.T) {
	cases := []struct {
		name    string
		field   string
		lo, hi  int
		want    []int
		wantErr bool
	}{
		{"分钟下界", "0", 0, 59, []int{0}, false},
		{"分钟上界", "59", 0, 59, []int{59}, false},
		{"分钟越上界", "60", 0, 59, nil, true},
		{"分钟全范围", "0-59", 0, 59, rangeInts(0, 59, 1), false},
		{"分钟步进", "*/15", 0, 59, []int{0, 15, 30, 45}, false},

		{"小时上界", "23", 0, 23, []int{23}, false},
		{"小时越上界", "24", 0, 23, nil, true},

		{"日下界", "1", 1, 31, []int{1}, false},
		{"日上界", "31", 1, 31, []int{31}, false},
		{"日越下界", "0", 1, 31, nil, true},
		{"日越上界", "32", 1, 31, nil, true},

		{"月下界", "1", 1, 12, []int{1}, false},
		{"月上界", "12", 1, 12, []int{12}, false},
		{"月越下界", "0", 1, 12, nil, true},
		{"月越上界", "13", 1, 12, nil, true},

		{"周下界周日", "0", 0, 6, []int{0}, false},
		{"周上界周六", "6", 0, 6, []int{6}, false},
		{"周越上界", "7", 0, 6, nil, true},

		{"列表混合区间去重升序", "1,3-5,9", 0, 59, []int{1, 3, 4, 5, 9}, false},
		{"列表重复值去重", "5,5,3", 0, 59, []int{3, 5}, false},
		{"步长超区间只落起点", "*/70", 0, 59, []int{0}, false},
		{"范围同值单点", "4-4", 0, 59, []int{4}, false},

		{"范围起始越界", "60-59", 0, 59, nil, true},
		{"范围结束越界", "0-60", 0, 59, nil, true},
		{"范围数字非法", "a-b", 0, 59, nil, true},
		{"步长非数字", "*/x", 0, 59, nil, true},
		{"步长缺失", "*/", 0, 59, nil, true},
		{"空片段", "1,,2", 0, 59, nil, true},
		{"纯垃圾", "abc", 0, 59, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseField(tc.field, tc.lo, tc.hi, "测试")
			if (err != nil) != tc.wantErr {
				t.Fatalf("parseField(%q,%d,%d) err=%v, wantErr=%v", tc.field, tc.lo, tc.hi, err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if len(got) != len(tc.want) {
				t.Fatalf("parseField(%q) = %v, want %v", tc.field, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("parseField(%q) = %v, want %v", tc.field, got, tc.want)
				}
			}
			// 展开结果必须严格升序（去重后靠 sort.Ints 保证；乱序会让匹配/预览错乱）
			for i := 1; i < len(got); i++ {
				if got[i] <= got[i-1] {
					t.Errorf("parseField(%q) 结果非严格升序: %v", tc.field, got)
				}
			}
		})
	}
}

// TestParsePartFixedStep 固定值带步进（v/n）语义：从 v 起步进到域上限（对齐 Vixie cron）。
//
// 风险点：v/n 与 a-b/n 两种步进容易混淆——把 v/n 误实现为「单值」会让
// "10/20" 只命中第 10 分而漏掉 30/50，任务静默少跑。
func TestParsePartFixedStep(t *testing.T) {
	cases := []struct {
		name string
		part string
		lo   int
		hi   int
		want []int
	}{
		{"固定值带步进", "10/20", 0, 59, []int{10, 30, 50}},
		{"固定值无步进为单值", "10", 0, 59, []int{10}},
		{"从上界起步步进", "59/1", 0, 59, []int{59}},
		{"范围带步进", "1-10/5", 0, 59, []int{1, 6}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parsePart(tc.part, tc.lo, tc.hi, "测试")
			if err != nil {
				t.Fatalf("parsePart(%q) 意外报错: %v", tc.part, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("parsePart(%q) = %v, want %v", tc.part, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("parsePart(%q) = %v, want %v", tc.part, got, tc.want)
				}
			}
		})
	}
}

// TestDbBackupCreds Nil 安全与逐字段回落。
//
// 风险点：runDBBackup 用这几个值拼 mysqldump 参数。config.GlobalConfig 在单测/独立
// 调用时可能为 nil——原函数特意做了 nil 兜底以免解引用 panic，这条用例守住该兜底；
// 同时校验 DBUser/DBName 为空时逐个回落到默认，避免拼出空账号的备份命令。
func TestDbBackupCreds(t *testing.T) {
	old := config.GlobalConfig
	t.Cleanup(func() { config.GlobalConfig = old })

	// config 未初始化：全部回落默认、口令为空（不 panic）
	config.GlobalConfig = nil
	u, n, p := dbBackupCreds()
	if u != defaultDBUser || n != defaultDBName || p != "" {
		t.Errorf("nil config 应回落默认: user=%q db=%q pwd=%q", u, n, p)
	}

	// 三项均配置：原样返回
	config.GlobalConfig = &config.Config{DBUser: "u1", DBName: "d1", DBPassword: "secret"}
	u, n, p = dbBackupCreds()
	if u != "u1" || n != "d1" || p != "secret" {
		t.Errorf("配置齐全应按配置返回: user=%q db=%q pwd=%q", u, n, p)
	}

	// 部分为空：空字段逐个回落默认，非空字段保留
	config.GlobalConfig = &config.Config{DBName: "d2"}
	u, n, _ = dbBackupCreds()
	if u != defaultDBUser {
		t.Errorf("DBUser 为空应回落默认，得到 %q", u)
	}
	if n != "d2" {
		t.Errorf("DBName 非空应保留，得到 %q", n)
	}
}

// TestPruneBackups 备份保留策略：仅处理 vmops-*.sql，按文件名（内嵌时间戳）字典序保留最新 keep 份。
//
// 风险点：清理逻辑若匹配过宽会误删非备份文件；若排序方向写反会把最新备份删掉、只留旧的，
// 真出事时恢复的是过期数据。用例在 t.TempDir() 内构造文件，不触碰真实备份目录。
func TestPruneBackups(t *testing.T) {
	s := &Scheduler{}

	t.Run("保留最新N份并忽略非备份文件", func(t *testing.T) {
		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, "vmops-20240101-0000.sql"))
		mustWrite(t, filepath.Join(dir, "vmops-20240102-0000.sql"))
		mustWrite(t, filepath.Join(dir, "vmops-20240103-0000.sql"))
		mustWrite(t, filepath.Join(dir, "notes.txt"))
		mustWrite(t, filepath.Join(dir, "vmops-not-backup.md"))

		s.pruneBackups(dir, 2)

		assertExists(t, filepath.Join(dir, "vmops-20240103-0000.sql"), true)
		assertExists(t, filepath.Join(dir, "vmops-20240102-0000.sql"), true)
		assertExists(t, filepath.Join(dir, "vmops-20240101-0000.sql"), false) // 最旧的被清理
		assertExists(t, filepath.Join(dir, "notes.txt"), true)                // 非备份文件不受影响
		assertExists(t, filepath.Join(dir, "vmops-not-backup.md"), true)      // 非 .sql 后缀不受影响
	})

	t.Run("份数未超保留数不删", func(t *testing.T) {
		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, "vmops-20240101-0000.sql"))
		mustWrite(t, filepath.Join(dir, "vmops-20240102-0000.sql"))
		s.pruneBackups(dir, 5)
		assertExists(t, filepath.Join(dir, "vmops-20240101-0000.sql"), true)
		assertExists(t, filepath.Join(dir, "vmops-20240102-0000.sql"), true)
	})

	t.Run("目录不存在不 panic", func(t *testing.T) {
		s.pruneBackups(filepath.Join(t.TempDir(), "missing"), 2) // 只应记日志，不 panic
	})
}

// TestRunActionGuards 动作分发在触碰外部系统前的防御分支。
//
// 风险点：runAction 的 default 分支是「未知动作」的兜底——被删掉后未知动作会静默
// 成功。其余几条是「参数/依赖缺失」路径，均发生在访问 DB/执行命令之前，可在无副作用
// 前提下验证（不涉及 docker/mysqldump/libvirt）。
func TestRunActionGuards(t *testing.T) {
	s := &Scheduler{}

	t.Run("未知动作报错", func(t *testing.T) {
		if _, err := s.runAction(model.ScheduledTask{Action: "bogus_action"}); err == nil {
			t.Error("未知动作应报错")
		}
	})

	t.Run("vm_snapshot 参数缺失/非法在访问DB前报错", func(t *testing.T) {
		bad := []string{"", "   ", "not-json", `{"vm_id":0}`}
		for _, params := range bad {
			if _, err := s.runAction(model.ScheduledTask{Action: ActionVMSnapshot, Params: params}); err == nil {
				t.Errorf("参数 %q 应在访问 DB 前报错", params)
			}
		}
	})

	t.Run("ansible_playbook 未接入任务系统报错", func(t *testing.T) {
		if _, err := s.runAction(model.ScheduledTask{Action: ActionAnsiblePlaybook}); err == nil {
			t.Error("TaskMgr 为空时应报「任务系统未接入」")
		}
	})

	t.Run("designer_drift 未注入回调报错", func(t *testing.T) {
		if _, err := s.runAction(model.ScheduledTask{Action: ActionDesignerDrift}); err == nil {
			t.Error("DriftCheck 未注入时应如实报错而非静默成功")
		}
	})
}

// mustWrite 在测试临时目录写一个占位文件。
func mustWrite(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("写测试文件 %s 失败: %v", path, err)
	}
}

// assertExists 断言路径是否存在。
func assertExists(t *testing.T, path string, want bool) {
	t.Helper()
	_, err := os.Stat(path)
	if got := err == nil; got != want {
		t.Errorf("路径 %s 存在=%v, want %v", path, got, want)
	}
}
