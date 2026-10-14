// 本文件负责 cron 表达式的解析与匹配：把 5 字段（分 时 日 月 周）表达式解析成各字段的
// 允许取值集合，并提供给定时刻的命中判定与下一次命中推算。全部是纯函数、无副作用。
package cron

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// maxLookaheadDays Next 逐分钟探测的最大天数，防「永不匹配的表达式」（如 2 月 31 日）死循环。
const maxLookaheadDays = 366

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

// NextRuns 解析表达式并返回自 now 起接下来的 n 个执行时刻（升序，严格递增）。
// 供 handler 的表达式预览端点（GET /api/crons/preview）复用 Next 的逐分钟探测。
// 表达式非法返回 ParseCron 的中文错误；表达式合法但 366 天内无匹配时刻时返回
// 已找到的前缀（可能为空切片），由调用方决定如何呈现。
func NextRuns(expr string, n int) ([]time.Time, error) {
	spec, err := ParseCron(expr)
	if err != nil {
		return nil, err
	}
	out := make([]time.Time, 0, n)
	cur := time.Now()
	for i := 0; i < n; i++ {
		next := Next(spec, cur)
		if next.IsZero() {
			break
		}
		out = append(out, next)
		cur = next
	}
	return out, nil
}
