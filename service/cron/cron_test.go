package cron

import (
	"reflect"
	"testing"
	"time"
)

// rangeInts 生成 [lo, hi] 区间内步进 step 的整数切片（测试辅助，step 必须 ≥1）。
func rangeInts(lo, hi, step int) []int {
	out := make([]int, 0, (hi-lo)/step+1)
	for v := lo; v <= hi; v += step {
		out = append(out, v)
	}
	return out
}

// TestParseCron 表驱动测试表达式解析：合法用例核对五字段展开集合，非法用例核对必须报错。
func TestParseCron(t *testing.T) {
	valid := []struct {
		name string
		expr string
		want Spec
	}{
		{
			name: "步进分钟_全通配",
			expr: "*/5 * * * *",
			want: Spec{
				Min:  rangeInts(0, 55, 5),
				Hour: rangeInts(0, 23, 1),
				Dom:  rangeInts(1, 31, 1),
				Mon:  rangeInts(1, 12, 1),
				Dow:  rangeInts(0, 6, 1),
			},
		},
		{
			name: "每天凌晨两点",
			expr: "0 2 * * *",
			want: Spec{
				Min:  []int{0},
				Hour: []int{2},
				Dom:  rangeInts(1, 31, 1),
				Mon:  rangeInts(1, 12, 1),
				Dow:  rangeInts(0, 6, 1),
			},
		},
		{
			name: "每月1号15号01点30分",
			expr: "30 1 1,15 * *",
			want: Spec{
				Min:  []int{30},
				Hour: []int{1},
				Dom:  []int{1, 15},
				Mon:  rangeInts(1, 12, 1),
				Dow:  rangeInts(0, 6, 1),
			},
		},
		{
			name: "固定值_每年1月1日零点",
			expr: "0 0 1 1 *",
			want: Spec{
				Min:  []int{0},
				Hour: []int{0},
				Dom:  []int{1},
				Mon:  []int{1},
				Dow:  rangeInts(0, 6, 1),
			},
		},
		{
			name: "闭区间范围",
			expr: "1-5 * * * *",
			want: Spec{
				Min:  []int{1, 2, 3, 4, 5},
				Hour: rangeInts(0, 23, 1),
				Dom:  rangeInts(1, 31, 1),
				Mon:  rangeInts(1, 12, 1),
				Dow:  rangeInts(0, 6, 1),
			},
		},
		{
			name: "工作日小时范围",
			expr: "0 9-17 * * 1-5",
			want: Spec{
				Min:  []int{0},
				Hour: rangeInts(9, 17, 1),
				Dom:  rangeInts(1, 31, 1),
				Mon:  rangeInts(1, 12, 1),
				Dow:  []int{1, 2, 3, 4, 5},
			},
		},
		{
			name: "混合步进与周固定",
			expr: "*/15 */6 * * 0",
			want: Spec{
				Min:  []int{0, 15, 30, 45},
				Hour: []int{0, 6, 12, 18},
				Dom:  rangeInts(1, 31, 1),
				Mon:  rangeInts(1, 12, 1),
				Dow:  []int{0},
			},
		},
		{
			name: "固定值带步进_从10每20分钟",
			expr: "10/20 * * * *",
			want: Spec{
				Min:  []int{10, 30, 50},
				Hour: rangeInts(0, 23, 1),
				Dom:  rangeInts(1, 31, 1),
				Mon:  rangeInts(1, 12, 1),
				Dow:  rangeInts(0, 6, 1),
			},
		},
		{
			name: "范围带步进",
			expr: "1-10/5 * * * *",
			want: Spec{
				Min:  []int{1, 6},
				Hour: rangeInts(0, 23, 1),
				Dom:  rangeInts(1, 31, 1),
				Mon:  rangeInts(1, 12, 1),
				Dow:  rangeInts(0, 6, 1),
			},
		},
		{
			name: "逗号列表重复值去重",
			expr: "5,5,5 * * * *",
			want: Spec{
				Min:  []int{5},
				Hour: rangeInts(0, 23, 1),
				Dom:  rangeInts(1, 31, 1),
				Mon:  rangeInts(1, 12, 1),
				Dow:  rangeInts(0, 6, 1),
			},
		},
		{
			name: "多空格分隔等价单空格",
			expr: "  0   2  *  *  *  ",
			want: Spec{
				Min:  []int{0},
				Hour: []int{2},
				Dom:  rangeInts(1, 31, 1),
				Mon:  rangeInts(1, 12, 1),
				Dow:  rangeInts(0, 6, 1),
			},
		},
	}

	for _, tt := range valid {
		t.Run("合法/"+tt.name, func(t *testing.T) {
			got, err := ParseCron(tt.expr)
			if err != nil {
				t.Fatalf("ParseCron(%q) 意外报错: %v", tt.expr, err)
			}
			if !reflect.DeepEqual(*got, tt.want) {
				t.Errorf("ParseCron(%q) = %+v, want %+v", tt.expr, *got, tt.want)
			}
		})
	}

	invalid := []struct {
		name string
		expr string
	}{
		{name: "字段数不足", expr: "* * * *"},
		{name: "字段过多", expr: "* * * * * *"},
		{name: "空表达式", expr: ""},
		{name: "纯空白", expr: "   "},
		{name: "分钟超上限", expr: "60 * * * *"},
		{name: "小时超上限", expr: "* 24 * * *"},
		{name: "日超下限", expr: "* * 0 * *"},
		{name: "月超上限", expr: "* * * 13 *"},
		{name: "周超上限", expr: "* * * * 7"},
		{name: "分钟负数", expr: "-1 * * * *"},
		{name: "垃圾字符", expr: "abc * * * *"},
		{name: "零步长", expr: "*/0 * * * *"},
		{name: "负步长", expr: "*/-5 * * * *"},
		{name: "步长缺失", expr: "*/ * * * *"},
		{name: "范围倒置", expr: "5-1 * * * *"},
		{name: "范围多段", expr: "1-2-3 * * * *"},
		{name: "空片段", expr: "1,,2 * * * *"},
	}

	for _, tt := range invalid {
		t.Run("非法/"+tt.name, func(t *testing.T) {
			got, err := ParseCron(tt.expr)
			if err == nil {
				t.Fatalf("ParseCron(%q) 应报错，实际得到 %+v", tt.expr, got)
			}
			if got != nil {
				t.Errorf("报错时 spec 应为 nil，实际 %+v", got)
			}
		})
	}
}

// TestParseCronErrorChinese 核对错误消息为中文固定文案（handler 会直接回显给前端）。
func TestParseCronErrorChinese(t *testing.T) {
	_, err := ParseCron("60 * * * *")
	if err == nil {
		t.Fatal("应报错")
	}
	if !containsHan(err.Error()) {
		t.Errorf("错误消息应包含中文，实际 %q", err.Error())
	}
}

// containsHan 判断字符串是否含汉字（测试辅助，与 handler.containsCJK 语义一致的最小子集）。
func containsHan(s string) bool {
	for _, r := range s {
		if r >= 0x4E00 && r <= 0x9FFF {
			return true
		}
	}
	return false
}

// TestMatch 核对五字段 AND 匹配语义。
func TestMatch(t *testing.T) {
	cases := []struct {
		name string
		expr string
		t    time.Time
		want bool
	}{
		{
			name: "每5分钟_命中",
			expr: "*/5 * * * *",
			t:    time.Date(2024, 1, 8, 12, 5, 0, 0, time.UTC),
			want: true,
		},
		{
			name: "每5分钟_未命中",
			expr: "*/5 * * * *",
			t:    time.Date(2024, 1, 8, 12, 3, 0, 0, time.UTC),
			want: false,
		},
		{
			name: "指定日_命中",
			expr: "30 1 1,15 * *",
			t:    time.Date(2024, 1, 15, 1, 30, 30, 0, time.UTC), // 秒不参与匹配
			want: true,
		},
		{
			name: "指定日_日期未命中",
			expr: "30 1 1,15 * *",
			t:    time.Date(2024, 1, 16, 1, 30, 0, 0, time.UTC),
			want: false,
		},
		{
			name: "指定日_分钟未命中",
			expr: "30 1 1,15 * *",
			t:    time.Date(2024, 1, 15, 1, 31, 0, 0, time.UTC),
			want: false,
		},
		{
			name: "周日零点_命中2024-01-07",
			expr: "0 0 * * 0",
			t:    time.Date(2024, 1, 7, 0, 0, 0, 0, time.UTC),
			want: true,
		},
		{
			name: "周日零点_周一未命中",
			expr: "0 0 * * 0",
			t:    time.Date(2024, 1, 8, 0, 0, 0, 0, time.UTC),
			want: false,
		},
		{
			name: "spec为nil_恒不命中",
			expr: "",
			t:    time.Date(2024, 1, 7, 0, 0, 0, 0, time.UTC),
			want: false,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var spec *Spec
			if tt.expr != "" {
				s, err := ParseCron(tt.expr)
				if err != nil {
					t.Fatalf("ParseCron(%q) 意外报错: %v", tt.expr, err)
				}
				spec = s
			}
			if got := Match(spec, tt.t); got != tt.want {
				t.Errorf("Match(%q, %s) = %v, want %v", tt.expr, tt.t, got, tt.want)
			}
		})
	}
}

// TestNext 核对「严格晚于 from 的下一个匹配时刻」语义与边界。
func TestNext(t *testing.T) {
	utc := time.UTC
	cases := []struct {
		name string
		expr string
		from time.Time
		want time.Time // IsZero 时期望返回零值（无匹配）
	}{
		{
			name: "每5分钟_从03分到05分",
			expr: "*/5 * * * *",
			from: time.Date(2024, 1, 8, 10, 3, 0, 0, utc),
			want: time.Date(2024, 1, 8, 10, 5, 0, 0, utc),
		},
		{
			name: "每5分钟_恰在匹配时刻时跳到下一档",
			expr: "*/5 * * * *",
			from: time.Date(2024, 1, 8, 10, 5, 0, 0, utc),
			want: time.Date(2024, 1, 8, 10, 10, 0, 0, utc),
		},
		{
			name: "每天凌晨两点_今天已过则明天",
			expr: "0 2 * * *",
			from: time.Date(2024, 1, 8, 3, 0, 0, 0, utc),
			want: time.Date(2024, 1, 9, 2, 0, 0, 0, utc),
		},
		{
			name: "每月1号15号_跨月推进",
			expr: "30 1 1,15 * *",
			from: time.Date(2024, 1, 16, 0, 0, 0, 0, utc),
			want: time.Date(2024, 2, 1, 1, 30, 0, 0, utc),
		},
		{
			name: "闰日_当年可匹配",
			expr: "0 0 29 2 *",
			from: time.Date(2023, 11, 1, 0, 0, 0, 0, utc),
			want: time.Date(2024, 2, 29, 0, 0, 0, 0, utc),
		},
		{
			name: "永不匹配_2月31日返回零值",
			expr: "0 0 31 2 *",
			from: time.Date(2024, 1, 1, 0, 0, 0, 0, utc),
			want: time.Time{},
		},
		{
			name: "from带秒与纳秒_对齐到整分之后",
			expr: "*/30 * * * *",
			from: time.Date(2024, 1, 8, 10, 0, 37, 123, utc),
			want: time.Date(2024, 1, 8, 10, 30, 0, 0, utc),
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			spec, err := ParseCron(tt.expr)
			if err != nil {
				t.Fatalf("ParseCron(%q) 意外报错: %v", tt.expr, err)
			}
			got := Next(spec, tt.from)
			if !got.Equal(tt.want) {
				t.Errorf("Next(%q, %s) = %s, want %s", tt.expr, tt.from, got, tt.want)
			}
		})
	}

	t.Run("nil spec 返回零值", func(t *testing.T) {
		if got := Next(nil, time.Now()); !got.IsZero() {
			t.Errorf("Next(nil, ...) = %s, want 零值", got)
		}
	})
}
