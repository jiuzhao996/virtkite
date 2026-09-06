package handler

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestMain 统一初始化 handler 包的测试环境。
//
//  1. gin 切 TestMode：去掉 debug 模式的路由/警告噪音，行为与 release 一致；
//  2. 标准库 log 输出丢弃：ErrorResponse/LogError 会把**完整错误**写进日志（这是刻意设计，
//     响应里只回中文友好文案），测试默认不需要这些行混进 go test -v 输出。
//     需要验证「日志确实写了」的用例自行用 log.SetOutput 临时接管（见 response_test.go）。
//
// 说明：本包所有测试都不调用 t.Parallel()。多个用例会临时接管全局 log 输出，
// 并行执行会互相污染，且这些用例本身都是微秒级，并行没有收益。
func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	log.SetOutput(io.Discard)
	code := m.Run()
	log.SetOutput(os.Stderr)
	os.Exit(code)
}

// newTestContext 构造不依赖真实 HTTP 服务的 gin 上下文与响应记录器。
//
// c.Request 必须非 nil：LogError 会读 c.Request.URL.Path，裸上下文会直接 panic。
// target 固定传合法路径：httptest.NewRequest 对含空格/引号的 URL 会 panic，
// 而被测的注入载荷是通过 c.Params 注入的（等价于 gin 路由解析后的结果），与 URL 无关。
func newTestContext(target string, params gin.Params) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	c.Params = params
	return c, rec
}

// decodeBody 把响应体解析成 map，便于断言统一响应格式 {code, message, data}。
// JSON 数字统一是 float64，断言 code 时要与 float64 比。
func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应体不是合法 JSON：err=%v，原始内容=%q", err, rec.Body.String())
	}
	return body
}

// TestParseID 覆盖数值主键解析器。
//
// 风险点：GORM 的 BuildCondition 对「非纯数字字符串且未带占位参数」的内联条件，
// 会把该字符串当作**原始 SQL 片段**直接拼进 WHERE。也就是说
//
//	db.First(&vm, c.Param("id"))   // 注入点：字符串原样进 SQL
//	db.First(&vm, id)              // 安全：uint 走参数化
//
// 而 RBAC 对 GET 是放行的（viewer 也能到达这些查询接口），注入面不是理论风险。
// 因此本函数是「参数进 DB 前的唯一闸门」，任何一条注入载荷被判定为合法都是高危缺陷。
// 实现基于 strconv.ParseUint(raw, 10, 32)：只接受纯十进制数字，且额外拒绝 0。
func TestParseID(t *testing.T) {
	cases := []struct {
		name   string // 中文场景名
		raw    string // 路径参数原值
		wantID uint
		wantOK bool
	}{
		// —— 合法输入 ——
		{"常规主键", "1", 1, true},
		{"多位数主键", "20250906", 20250906, true},
		{"uint32 上界（4294967295）", "4294967295", 4294967295, true},
		{"前导零：ParseUint 按十进制照收，等价于 7", "007", 7, true},

		// —— 边界与非法数值 ——
		{"0 号主键：自增主键从 1 起，0 一律视为非法", "0", 0, false},
		{"多个零", "000", 0, false},
		{"负数：ParseUint 不接受符号", "-1", 0, false},
		{"超出 uint32：4294967296 溢出被拒", "4294967296", 0, false},
		{"超长数字串（40 个 9）溢出被拒", strings.Repeat("9", 40), 0, false},
		{"空串（路径参数缺失时的实际取值）", "", 0, false},

		// —— 空白字符：ParseUint 不做 TrimSpace ——
		{"前导空格", " 1", 0, false},
		{"尾随空格", "1 ", 0, false},
		{"仅空格", "   ", 0, false},

		// —— 混入非数字字符 ——
		{"数字后缀字母", "1abc", 0, false},
		{"纯字母", "abc", 0, false},
		{"小数", "1.0", 0, false},
		{"科学计数法", "1e3", 0, false},
		{"显式正号", "+1", 0, false},
		{"十六进制字面量（base 固定 10，不识别 0x）", "0x10", 0, false},
		{"Go 风格数字分隔符", "1_0", 0, false},
		{"阿拉伯-印度数字（Unicode 数字但非 ASCII）", "١٢٣", 0, false},
		{"中文数字", "一", 0, false},

		// —— SQL 注入载荷：这些是本函数存在的直接理由 ——
		{"注入：布尔恒真", "1 OR 1=1", 0, false},
		{"注入：单引号闭合", "1' OR '1'='1", 0, false},
		{"注入：堆叠查询 + 延时探测", "1;SELECT SLEEP(3)", 0, false},
		{"注入：子查询盲注", "1 AND (SELECT COUNT(*) FROM users)>0", 0, false},
		{"注入：UNION 读表", "1 UNION SELECT password_hash FROM users", 0, false},
		{"注入：注释截断", "1--", 0, false},
		{"注入：换行 + 注释", "1\n-- ", 0, false},
		{"注入：超长载荷（4096 字符）", strings.Repeat("A", 4096), 0, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotID, gotOK := parseID(tc.raw)
			if gotOK != tc.wantOK {
				t.Fatalf("parseID(%q) 合法性判定错误：期望 ok=%v，实际 ok=%v（返回 id=%d）",
					tc.raw, tc.wantOK, gotOK, gotID)
			}
			if gotID != tc.wantID {
				t.Errorf("parseID(%q) 解析值错误：期望 id=%d，实际 id=%d", tc.raw, tc.wantID, gotID)
			}
			// 不变式：判定非法时必须返回零值，调用方即使忽略 ok 也拿不到可用主键
			if !gotOK && gotID != 0 {
				t.Errorf("parseID(%q) 判定非法却返回了非零 id=%d，调用方漏判 ok 时会把脏值带进 SQL",
					tc.raw, gotID)
			}
		})
	}
}

// TestParamID 覆盖 HTTP 处理器专用的主键解析入口。
//
// 风险点：paramID 失败时必须**自己写完 400 响应并 Abort**，调用方只写 `return`。
// 若忘记写响应，客户端会拿到 200 空体；若忘记 Abort，后续中间件与处理器会继续跑，
// 等于校验形同虚设。另外错误文案不能回显用户传入的原值（避免把注入载荷反射回响应体）。
func TestParamID(t *testing.T) {
	cases := []struct {
		name   string
		raw    string
		wantID uint
		wantOK bool
	}{
		{"合法主键放行", "42", 42, true},
		{"uint32 上界放行", "4294967295", 4294967295, true},
		{"0 号主键被拒", "0", 0, false},
		{"负数被拒", "-7", 0, false},
		{"空串被拒", "", 0, false},
		{"注入载荷被拒（单引号闭合）", "1' OR '1'='1", 0, false},
		{"注入载荷被拒（堆叠查询）", "1;SELECT SLEEP(3)", 0, false},
		{"溢出被拒", "99999999999", 0, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := newTestContext("/api/vms/id", gin.Params{{Key: "id", Value: tc.raw}})

			gotID, gotOK := paramID(c, "id")
			if gotOK != tc.wantOK {
				t.Fatalf("paramID(id=%q) 合法性判定错误：期望 ok=%v，实际 ok=%v", tc.raw, tc.wantOK, gotOK)
			}

			if tc.wantOK {
				if gotID != tc.wantID {
					t.Errorf("paramID(id=%q) 解析值错误：期望 %d，实际 %d", tc.raw, tc.wantID, gotID)
				}
				if c.IsAborted() {
					t.Errorf("paramID(id=%q) 合法却中断了请求链，后续处理器不会执行", tc.raw)
				}
				if rec.Body.Len() != 0 {
					t.Errorf("paramID(id=%q) 合法时不应写响应体，实际写入 %q", tc.raw, rec.Body.String())
				}
				return
			}

			// —— 失败路径的三项硬要求 ——
			if got := rec.Code; got != http.StatusBadRequest {
				t.Errorf("paramID(id=%q) 状态码错误：期望 %d，实际 %d", tc.raw, http.StatusBadRequest, got)
			}
			if !c.IsAborted() {
				t.Errorf("paramID(id=%q) 未调用 Abort：非法 ID 仍会流入后续中间件与处理器", tc.raw)
			}
			body := decodeBody(t, rec)
			if got, want := body["code"], float64(http.StatusBadRequest); got != want {
				t.Errorf("paramID(id=%q) 响应 code 错误：期望 %v，实际 %v", tc.raw, want, got)
			}
			if got, want := body["message"], "ID 参数非法"; got != want {
				t.Errorf("paramID(id=%q) 响应 message 错误：期望 %q，实际 %v", tc.raw, want, got)
			}
			// 不把用户输入回显到响应体（原值可能是注入载荷或 XSS 片段）。
			// 只对含非数字字符的原值做此断言：纯数字原值（如 "0"）会与状态码 400 的字面量误撞。
			if containsNonDigit(tc.raw) && strings.Contains(rec.Body.String(), tc.raw) {
				t.Errorf("paramID(id=%q) 把用户原始输入回显进了响应体：%s", tc.raw, rec.Body.String())
			}
		})
	}
}

// containsNonDigit 判断字符串是否含非 ASCII 数字字符（用于筛出注入类原值）。
func containsNonDigit(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return true
		}
	}
	return false
}

// TestParamIDMissingParam 路径参数名写错/未注册时，c.Param 返回空串，必须走失败路径。
// 风险点：若此时误判为合法，GORM 会收到 0 值主键做无意义查询。
func TestParamIDMissingParam(t *testing.T) {
	c, rec := newTestContext("/api/vms/1", gin.Params{{Key: "id", Value: "1"}})

	// 故意取一个不存在的参数名
	id, ok := paramID(c, "vm_id")
	if ok {
		t.Fatalf("参数名不存在时期望判定失败，实际返回 ok=true id=%d", id)
	}
	if rec.Code != http.StatusBadRequest {
		t.Errorf("状态码错误：期望 %d，实际 %d", http.StatusBadRequest, rec.Code)
	}
	if !c.IsAborted() {
		t.Error("参数缺失时未 Abort，请求链会继续执行")
	}
}
