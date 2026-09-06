package tasks

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jiuzhao/vmops/model"
)

// TestMain 初始化 tasks 包测试环境：丢弃日志输出。
//
// tasks 包大量依赖日志（executor panic 的堆栈、入队超时、保留卷原因等「只进日志不入库」
// 的信息都在这里），测试默认不需要它们混进 go test -v 输出。
// 需要验证日志内容的用例自行用 log.SetOutput 临时接管。
//
// 说明：本包测试不用 t.Parallel()。部分用例会临时改写 config.GlobalConfig，
// 且 enqueue 的超时用例依赖真实时钟，并行会让耗时断言变得不可靠。
func TestMain(m *testing.M) {
	log.SetOutput(io.Discard)
	code := m.Run()
	log.SetOutput(os.Stderr)
	os.Exit(code)
}

// TestFriendlyError 覆盖任务失败文案的脱敏与截断。
//
// 风险点：返回值直接写进 tasks.error 列，而该列会被前端任务详情原样展示。
// libvirt 的原始报错里带宿主机路径、XML 片段、SQL 语句，绝不能进这个字段；
// 同时该列是 size:500，超长文案在 MySQL 严格模式下会写入失败（任务失败原因也就丢了），
// 因此必须在应用层按 rune 截断。约定与 handler.friendlyMessage 一致：取首个冒号前的中文段。
func TestFriendlyError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil 错误降级为通用文案", nil, "操作失败"},
		{"纯中文无冒号：原样返回", errors.New("虚拟机名称只允许字母、数字、下划线和连字符"),
			"虚拟机名称只允许字母、数字、下划线和连字符"},
		{"半角冒号：只取中文段", errors.New("创建虚拟机记录失败: Error 1062 Duplicate entry"), "创建虚拟机记录失败"},
		{"全角冒号：同样能切分", errors.New("定义域失败：libvirt XML 报错"), "定义域失败"},
		{"多层 %w 错误链：只露最外层中文", fmt.Errorf("创建磁盘卷失败: %w",
			fmt.Errorf("存储池不存在: %w", errors.New("Storage pool not found"))), "创建磁盘卷失败"},
		{"纯英文：整段吞掉", errors.New("connection reset by peer"), "操作失败"},
		{"纯英文带冒号：整段吞掉", errors.New("libvirt: internal error: cannot open /dev/kvm"), "操作失败"},
		{"冒号前无中文：整段吞掉", errors.New("virError: 磁盘不存在"), "操作失败"},
		{"空错误消息：降级", errors.New(""), "操作失败"},
		// executor panic 走统一失败路径，文案必须原样透出（不含冒号、含中文，正好不被改写）
		{"executor panic 的哨兵错误原样透出", errExecutorPanic, msgTaskPanic},
		{"包装过的 panic 哨兵：仍取中文首段", fmt.Errorf("%w: 附加信息", errExecutorPanic), msgTaskPanic},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := friendlyError(tc.err); got != tc.want {
				t.Errorf("friendlyError(%v) 错误：\n期望=%q\n实际=%q", tc.err, tc.want, got)
			}
		})
	}

	t.Run("超长中文按 rune 截断到 maxErrorLen", func(t *testing.T) {
		long := strings.Repeat("失", 600)
		got := friendlyError(errors.New(long))
		if n := len([]rune(got)); n != maxErrorLen {
			t.Errorf("截断长度错误：期望 %d 个 rune，实际 %d 个", maxErrorLen, n)
		}
		// 截断必须按 rune 而不是 byte，否则会切出半个汉字（乱码写进 DB）
		if strings.ContainsRune(got, '\uFFFD') {
			t.Error("截断结果含替换字符 U+FFFD，说明按字节切断了多字节汉字")
		}
		if got != strings.Repeat("失", maxErrorLen) {
			t.Errorf("截断内容错误：期望前 %d 个「失」，实际 %.20q...", maxErrorLen, got)
		}
	})

	t.Run("恰好 maxErrorLen 长度不截断", func(t *testing.T) {
		exact := strings.Repeat("失", maxErrorLen)
		if got := friendlyError(errors.New(exact)); got != exact {
			t.Errorf("边界长度被误截断：期望 %d 个 rune，实际 %d 个", maxErrorLen, len([]rune(got)))
		}
	})
}

// TestTruncate 覆盖按 rune 截断的工具函数。
// 风险点：按 byte 截断会把汉字切成半个字符（写库即乱码），必须按 rune 处理。
func TestTruncate(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"短于上限：原样返回", "创建失败", 10, "创建失败"},
		{"等于上限：原样返回", "创建失败", 4, "创建失败"},
		{"超出上限：按 rune 截断", "创建虚拟机失败", 3, "创建虚"},
		{"英文按 rune 与 byte 一致", "abcdef", 3, "abc"},
		{"中英混排", "创建 vm 失败", 4, "创建 v"},
		{"空串", "", 5, ""},
		{"上限为 0", "创建失败", 0, ""},
		{"emoji 按 rune 不被切碎", "任务🚀完成", 3, "任务🚀"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := truncate(tc.in, tc.n)
			if got != tc.want {
				t.Errorf("truncate(%q, %d) 错误：期望 %q，实际 %q", tc.in, tc.n, tc.want, got)
			}
			if strings.ContainsRune(got, '\uFFFD') && !strings.ContainsRune(tc.in, '\uFFFD') {
				t.Errorf("truncate(%q, %d) 切出了半个字符：%q", tc.in, tc.n, got)
			}
		})
	}
}

// TestTasksContainsCJK 覆盖 tasks 包内的中文判定（与 handler 包同名函数各有一份实现）。
// 风险点：它决定任务错误文案是「露出」还是降级成「操作失败」，判定过宽会放行英文原始报错。
func TestTasksContainsCJK(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"简体中文", "创建失败", true},
		{"单个汉字", "错", true},
		{"中英混排", "创建 vol1 失败", true},
		{"日文汉字（属 Han）", "日本語", true},
		{"日文假名（非 Han）", "ひらがな", false},
		{"韩文谚文（非 Han）", "한글", false},
		{"中文标点（非 Han）", "，。：", false},
		{"纯英文", "connection refused", false},
		{"空串", "", false},
		{"纯数字", "12345", false},
		{"emoji", "🚀", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := containsCJK(tc.in); got != tc.want {
				t.Errorf("containsCJK(%q) 判定错误：期望 %v，实际 %v", tc.in, tc.want, got)
			}
		})
	}
}

// TestTaskContractConstants 把对外契约里的字面量钉死。
//
// 风险点：这些常量不是内部实现细节 —— tasks.status 的四个取值同时被前端任务列表的
// 状态色映射、docs/task-contract.md 与 main.go 的启动收敛逻辑（把残留 pending/running
// 置 failed）依赖；错误列长度 500 与 DB 列定义 size:500 必须对齐。
// 手滑改一个字母不会有编译错误，只会让前端静默显示未知状态，所以用测试兜住。
func TestTaskContractConstants(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"pending 状态字面量", statusPending, "pending"},
		{"running 状态字面量", statusRunning, "running"},
		{"success 状态字面量", statusSuccess, "success"},
		{"failed 状态字面量", statusFailed, "failed"},
		{"默认存储池名（与 model.VM.StoragePool 的 gorm 默认值一致）", DefaultStoragePoolResolver(), "vmops"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("常量值被改动：期望 %q，实际 %q", tc.want, tc.got)
			}
		})
	}

	t.Run("用户可见文案为中文且不含内部细节", func(t *testing.T) {
		for _, msg := range []string{msgTaskPanic, msgQueueBusy} {
			if !containsCJK(msg) {
				t.Errorf("文案不是中文：%q", msg)
			}
			if strings.Contains(msg, ":") || strings.Contains(msg, "：") {
				t.Errorf("文案含冒号，会被 friendlyError 截断成半句：%q", msg)
			}
		}
	})

	t.Run("数值常量与 DB/队列容量约定一致", func(t *testing.T) {
		if maxErrorLen != 500 {
			t.Errorf("maxErrorLen 应与 tasks.error 列的 size:500 对齐：实际 %d", maxErrorLen)
		}
		if workerCount <= 0 {
			t.Errorf("workerCount 必须为正，否则没有 worker 取任务：实际 %d", workerCount)
		}
		if queueBufferSize <= 0 {
			t.Errorf("queueBufferSize 必须为正：实际 %d", queueBufferSize)
		}
		if enqueueTimeout <= 0 {
			t.Errorf("enqueueTimeout 必须为正，否则队列满时会直接判失败：实际 %v", enqueueTimeout)
		}
		if defaultListLimit <= 0 || defaultListLimit > maxListLimit {
			t.Errorf("List 默认条数必须在 (0, %d] 内：实际 %d", maxListLimit, defaultListLimit)
		}
	})
}

// TestRunExecutorRecoversPanic 覆盖 executor panic 的兜底（P0 稳定性批次的核心成果）。
//
// 风险点：executor 跑在 worker goroutine 里，**gin 的 Recovery 中间件完全覆盖不到**。
// 修复前任何一个 executor 里的 nil 解引用（比如 payload 里少个字段）都会带走整个进程，
// 所有虚拟机的管理面同时中断。这里验证三件事：不再 panic 出来、返回统一哨兵错误、
// panic 值与堆栈只进日志不进返回值（Task.Error 会回显前端）。
func TestRunExecutorRecoversPanic(t *testing.T) {
	panics := []struct {
		name string
		fn   Executor
	}{
		{"显式 panic 字符串", func(*ExecContext) error { panic("boom") }},
		{"nil map 写入", func(*ExecContext) error {
			var m map[string]string
			m["k"] = "v"
			return nil
		}},
		{"nil 指针解引用", func(*ExecContext) error {
			var vm *model.VM
			_ = vm.Name
			return nil
		}},
		{"切片越界", func(*ExecContext) error {
			s := []int{1}
			_ = s[5]
			return nil
		}},
		{"整数除零", func(*ExecContext) error {
			zero := 0
			_ = 1 / zero
			return nil
		}},
		{"panic 值携带敏感内容", func(*ExecContext) error {
			panic("password=admin123 /var/lib/libvirt/images/web.qcow2")
		}},
	}

	for _, tc := range panics {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &ExecContext{Task: &model.Task{ID: 42, Type: "create_vm"}}

			err := runExecutor(tc.fn, ctx)
			if err == nil {
				t.Fatal("executor panic 后应返回错误，实际返回 nil（任务会被误判为成功）")
			}
			if !errors.Is(err, errExecutorPanic) {
				t.Errorf("错误类型不符：期望 %v，实际 %v", errExecutorPanic, err)
			}
			// 回显给用户的文案里不能出现 panic 原文
			if got := friendlyError(err); got != msgTaskPanic {
				t.Errorf("用户可见文案错误：期望 %q，实际 %q", msgTaskPanic, got)
			}
			if strings.Contains(err.Error(), "password") || strings.Contains(err.Error(), "/var/lib") {
				t.Errorf("panic 内容泄漏进返回错误：%v", err)
			}
		})
	}
}

// TestRunExecutorPassesThroughResult executor 正常返回时 runExecutor 不得改写结果。
// 风险点：兜底逻辑写错会把成功当失败（或反之），任务状态与实际操作背离。
func TestRunExecutorPassesThroughResult(t *testing.T) {
	t.Run("返回 nil 表示成功", func(t *testing.T) {
		ctx := &ExecContext{Task: &model.Task{ID: 1, Type: "stop_vm"}}
		if err := runExecutor(func(*ExecContext) error { return nil }, ctx); err != nil {
			t.Errorf("期望 nil，实际 %v", err)
		}
	})

	t.Run("原始错误原样透出（保留 %w 链供 errors.Is 判断）", func(t *testing.T) {
		inner := errors.New("Storage pool not found")
		want := fmt.Errorf("存储池不存在: %w", inner)
		ctx := &ExecContext{Task: &model.Task{ID: 2, Type: "create_vm"}}

		err := runExecutor(func(*ExecContext) error { return want }, ctx)
		if err == nil {
			t.Fatal("期望返回原始错误，实际 nil")
		}
		if !errors.Is(err, inner) {
			t.Errorf("错误链被破坏：期望能 errors.Is 到 %v，实际 %v", inner, err)
		}
		if err.Error() != want.Error() {
			t.Errorf("错误文案被改写：期望 %q，实际 %q", want.Error(), err.Error())
		}
	})

	t.Run("executor 可通过 ctx 回写任务结果", func(t *testing.T) {
		ctx := &ExecContext{Task: &model.Task{ID: 3, Type: "clone_vm"}}
		err := runExecutor(func(c *ExecContext) error {
			c.Task.Result = `{"vm_id":7}`
			return nil
		}, ctx)
		if err != nil {
			t.Fatalf("期望成功，实际 %v", err)
		}
		if ctx.Task.Result != `{"vm_id":7}` {
			t.Errorf("Result 未回写：实际 %q", ctx.Task.Result)
		}
	})
}

// TestEnqueueFastPath 覆盖队列未满时的入队（非阻塞路径）。
// 风险点：入队失败又不改状态，前端会永远看到 pending 的僵尸任务。
// 这里手工构造 Manager（不走 NewManager），避免起 worker 与建 libvirt/DB 连接。
func TestEnqueueFastPath(t *testing.T) {
	m := &Manager{queue: make(chan uint, 4), executors: map[string]Executor{}}
	task := &model.Task{ID: 101, Type: "create_vm", Status: statusPending}

	done := make(chan struct{})
	go func() {
		m.enqueue(task)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("队列未满时 enqueue 应立即返回，实际阻塞超过 2 秒")
	}

	select {
	case got := <-m.queue:
		if got != task.ID {
			t.Errorf("入队的 taskID 错误：期望 %d，实际 %d", task.ID, got)
		}
	default:
		t.Fatal("任务未进入队列，worker 永远取不到它")
	}
	if task.Status != statusPending {
		t.Errorf("入队成功不应改状态：期望 %q，实际 %q", statusPending, task.Status)
	}
	if task.Error != "" {
		t.Errorf("入队成功不应写错误：实际 %q", task.Error)
	}
}

// TestEnqueueTimeoutMarksTaskFailed 覆盖队列满时的有界等待（P0 稳定性批次成果）。
//
// 风险点：旧实现是 `go func(){ m.queue <- id }()` —— 队列满时每次提交都留下一个
// 永不退出的阻塞 goroutine，提交越频繁泄漏越多，最终 OOM。现在改成同步等待
// enqueueTimeout，超时把任务置 failed 并回写返回给 handler 的 task 对象，
// 前端立刻能看到「任务队列繁忙」而不是永久 pending。
//
// 本用例刻意不接数据库：m.DB 为 nil，超时分支里的 markFailed 会在 gorm 上 panic，
// 而 markFailed 自带 recover —— 于是同时验证了「DB 层再出意外也不能把进程带走」。
// 代价是必须真等 enqueueTimeout（3 秒），这是本包唯一的慢用例。
func TestEnqueueTimeoutMarksTaskFailed(t *testing.T) {
	m := &Manager{queue: make(chan uint, 1), executors: map[string]Executor{}}
	m.queue <- 1 // 占满队列，且没有 worker 消费

	task := &model.Task{ID: 202, Type: "create_vm", Status: statusPending, VMName: "web-01"}

	// 接管日志以验证超时分支确实走到了（含 markFailed 的 recover 记录）。
	// 只在 <-done 之后读 buf：channel 关闭构成 happens-before，不存在并发读写。
	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	defer log.SetOutput(io.Discard)

	start := time.Now()
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.enqueue(task) // 内部 markFailed 会因 m.DB == nil 触发 panic 并自行 recover
	}()

	select {
	case <-done:
	case <-time.After(enqueueTimeout + 5*time.Second):
		t.Fatal("enqueue 未在超时后返回，说明有界等待失效（旧实现会永久阻塞）")
	}
	elapsed := time.Since(start)

	if elapsed < enqueueTimeout {
		t.Errorf("等待时间过短：期望至少 %v（应先尽力等待再判失败），实际 %v", enqueueTimeout, elapsed)
	}
	if elapsed > enqueueTimeout+3*time.Second {
		t.Errorf("等待时间过长：期望约 %v，实际 %v", enqueueTimeout, elapsed)
	}
	if task.Status != statusFailed {
		t.Errorf("超时后任务状态错误：期望 %q，实际 %q（前端会一直显示 pending）", statusFailed, task.Status)
	}
	if task.Error != msgQueueBusy {
		t.Errorf("超时后错误文案错误：期望 %q，实际 %q", msgQueueBusy, task.Error)
	}
	// 队列里原有的任务不应被顶掉
	if len(m.queue) != 1 {
		t.Errorf("队列内容被破坏：期望仍有 1 个待执行任务，实际 %d 个", len(m.queue))
	}
	// 运维可观测性：超时必须留日志，且带上 id/type/vm 便于定位积压来源
	logged := logBuf.String()
	if !strings.Contains(logged, "入队超时") {
		t.Errorf("超时未写日志，线上积压将无从发现：日志=%q", logged)
	}
	if !strings.Contains(logged, "id=202") || !strings.Contains(logged, "web-01") {
		t.Errorf("超时日志缺少任务标识：日志=%q", logged)
	}
	// markFailed 的 recover 记录，证明「DB 出意外也不会带走 worker」这条兜底真的被执行到
	if !strings.Contains(logged, "置 failed 时 panic") {
		t.Errorf("markFailed 的 recover 未被触达，本用例对 DB 兜底的验证失效：日志=%q", logged)
	}
}

// TestMarkFailedRecoversFromNilDB markFailed 的 recover 兜底。
// 风险点：markFailed 是 panic 兜底路径的终点（run 的 defer 里也调它）。
// 如果它自己 panic 了，worker goroutine 就直接把进程带走 —— 兜底逻辑必须自己也不出事。
func TestMarkFailedRecoversFromNilDB(t *testing.T) {
	m := &Manager{queue: make(chan uint, 1), executors: map[string]Executor{}} // DB 为 nil

	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	defer log.SetOutput(io.Discard)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("markFailed 未兜住内部 panic，worker goroutine 会带走进程：%v", r)
		}
	}()
	m.markFailed(7, msgTaskPanic)

	// 非空断言：确认内部确实发生了 panic 并被 recover（否则本用例是空跑）
	if got := logBuf.String(); !strings.Contains(got, "置 failed 时 panic id=7") {
		t.Errorf("未观察到 recover 记录，无法证明兜底生效：日志=%q", got)
	}
}

// TestRegisterVMTasks 覆盖 executor 注册表。
//
// 风险点：run() 里按 task.Type 查表，查不到就把任务置 failed（「未知任务类型」）。
// 少注册一个类型 = 对应功能整条链路静默失效（前端提交成功但任务永远失败），
// 因此把 docs/task-contract.md 约定的 5 个类型钉在测试里。
func TestRegisterVMTasks(t *testing.T) {
	m := &Manager{queue: make(chan uint, 1), executors: map[string]Executor{}}
	RegisterVMTasks(m)

	want := []string{"create_vm", "delete_vm", "clone_vm", "clone_image_vm", "stop_vm"}
	for _, taskType := range want {
		t.Run("已注册 "+taskType, func(t *testing.T) {
			m.mu.RLock()
			fn, ok := m.executors[taskType]
			m.mu.RUnlock()
			if !ok {
				t.Fatalf("任务类型 %q 未注册，提交后会被判「未知任务类型」", taskType)
			}
			if fn == nil {
				t.Fatalf("任务类型 %q 注册了 nil executor，调用即 panic", taskType)
			}
		})
	}

	m.mu.RLock()
	total := len(m.executors)
	m.mu.RUnlock()
	if total != len(want) {
		t.Errorf("注册数量与契约不符：期望 %d 个（%v），实际 %d 个（若新增了任务类型请同步本用例与 docs/task-contract.md）",
			len(want), want, total)
	}

	t.Run("Register 覆盖同名类型（后注册者生效）", func(t *testing.T) {
		called := false
		m.Register("create_vm", func(*ExecContext) error {
			called = true
			return nil
		})
		m.mu.RLock()
		fn := m.executors["create_vm"]
		m.mu.RUnlock()
		if err := fn(nil); err != nil {
			t.Fatalf("执行覆盖后的 executor 失败: %v", err)
		}
		if !called {
			t.Error("覆盖注册未生效")
		}
	})

	t.Run("RegisterVMTasks(nil) 不 panic", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("传 nil Manager 时应静默返回，实际 panic：%v", r)
			}
		}()
		RegisterVMTasks(nil)
	})
}
