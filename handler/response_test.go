package handler

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"testing"
)

// TestFriendlyMessage 覆盖「错误脱敏」的核心提取逻辑。
//
// 风险点：handler 是内外边界，libvirt 原始报错里带宿主机路径、socket 位置、XML 片段、
// SQL 语句等信息，直接回给前端等于把内部拓扑送出去。约定 virt 层错误写成
// `中文描述: 内部细节`，本函数只取第一个冒号前的中文段，取不到中文就统一降级「操作失败」。
// 因此这里既要验证「该保留的中文文案被保留」，也要验证「英文/系统层报错被彻底吞掉」。
func TestFriendlyMessage(t *testing.T) {
	// 600 个汉字：验证 handler 版**不做长度截断**（与 tasks.friendlyError 的 500 rune 截断不同）
	longCN := strings.Repeat("失", 600)

	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil 错误降级为通用文案", nil, "操作失败"},
		{"纯中文无冒号：原样返回", errors.New("创建虚拟机失败"), "创建虚拟机失败"},
		{"半角冒号：只取中文描述段", errors.New("创建卷失败: libvirt internal error"), "创建卷失败"},
		{"全角冒号：同样能切分", errors.New("创建卷失败：libvirt 内部错误"), "创建卷失败"},
		{"多重冒号：取第一段", errors.New("创建卷失败: dial unix: connection refused"), "创建卷失败"},
		{"中文描述里嵌对象名", errors.New("创建卷 vol1 失败: no such pool"), "创建卷 vol1 失败"},
		{"纯英文无冒号：整段吞掉", errors.New("connection refused"), "操作失败"},
		{"纯英文带冒号：整段吞掉", errors.New("dial tcp 127.0.0.1:16509: i/o timeout"), "操作失败"},
		{"冒号前无中文：整段吞掉", errors.New("libvirt error: 卷不存在"), "操作失败"},
		{"空错误消息：降级", errors.New(""), "操作失败"},
		{"仅一个冒号：降级", errors.New(":"), "操作失败"},
		{"%w 包裹的多层错误链：只露最外层中文", fmt.Errorf("创建存储池失败: %w",
			fmt.Errorf("libvirt XML 解析失败: %w", errors.New("unexpected element"))), "创建存储池失败"},

		// —— 以下为现状断言（实现的已知边界，不是期望行为，见函数尾部 TODO 说明）——
		{"冒号位于首位（半角）：现状原样返回，含内部细节", errors.New(": internal 细节"), ": internal 细节"},
		{"冒号位于首位（全角）：现状原样返回", errors.New("：中文描述"), "：中文描述"},
		{"含换行的多行错误：现状把首个冒号前的整段返回", errors.New("任务执行失败\ngoroutine 1 [running]:"),
			"任务执行失败\ngoroutine 1 [running]"},
		{"600 汉字：现状不截断，原样返回", errors.New(longCN), longCN},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := friendlyMessage(tc.err)
			if got != tc.want {
				t.Errorf("friendlyMessage(%v) 结果错误：\n期望=%q\n实际=%q", tc.err, tc.want, got)
			}
		})
	}

	// TODO(已在回报中列出，待产品决定):
	//  1. 冒号位于首位时（IndexAny 返回 0，条件 i>0 不成立）整条消息原样外泄，
	//     只要串里任意位置含一个汉字就会绕过降级；
	//  2. 不截断长度，600 汉字会原样进响应体（tasks.friendlyError 截断到 500 rune）；
	//  3. 脱敏效果依赖「中文描述在冒号前」的书写约定，描述段自身含内部路径时仍会外泄。
	// 三者均为现状，测试断言的是现状而非期望值。
}

// TestContainsCJK 覆盖「是否为中文友好文案」的判定。
//
// 风险点：这是 friendlyMessage 决定「露出还是降级」的唯一开关。判定过宽会放行英文系统
// 报错（内部细节外泄），判定过窄会把正常中文文案降级成无信息量的「操作失败」（可用性下降）。
// 实现只匹配 unicode.Han，因此日文假名、韩文谚文、中文标点都判定为 false —— 本项目所有
// 文案都是简体中文，该行为正确，但函数名 containsCJK 与实际（仅 C）并不严格对应。
func TestContainsCJK(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"简体中文", "创建失败", true},
		{"单个汉字", "错", true},
		{"中英混排", "创建 vol1 失败 code=42", true},
		{"日文汉字（属 Han）", "日本語", true},
		{"日文平假名（非 Han，判定 false）", "ひらがな", false},
		{"日文片假名（非 Han，判定 false）", "カタカナ", false},
		{"韩文谚文（非 Han，判定 false）", "한글", false},
		{"中文标点（非 Han，判定 false）", "，。！：", false},
		{"纯 ASCII 英文", "connection refused", false},
		{"空串", "", false},
		{"纯数字", "1234567890", false},
		{"emoji", "🚀🔥", false},
		{"全角拉丁字母", "ＡＢＣ", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := containsCJK(tc.in); got != tc.want {
				t.Errorf("containsCJK(%q) 判定错误：期望 %v，实际 %v", tc.in, tc.want, got)
			}
		})
	}
}

// TestSuccessAndCreatedResponseShape 校验成功响应的统一结构。
//
// 风险点：前端 api/index.js 与所有页面都按 {code, message, data} 解包，
// 少一个字段或 code 不是 200 会导致页面静默空白。此处把契约钉死。
func TestSuccessAndCreatedResponseShape(t *testing.T) {
	t.Run("Success 返回 code=200、message=success、data 原样透出", func(t *testing.T) {
		c, rec := newTestContext("/api/vms", nil)
		Success(c, map[string]interface{}{"total": 2})

		if rec.Code != http.StatusOK {
			t.Errorf("HTTP 状态码错误：期望 %d，实际 %d", http.StatusOK, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Errorf("Content-Type 错误：期望含 application/json，实际 %q", ct)
		}
		body := decodeBody(t, rec)
		if got, want := body["code"], float64(200); got != want {
			t.Errorf("code 错误：期望 %v，实际 %v", want, got)
		}
		if got, want := body["message"], "success"; got != want {
			t.Errorf("message 错误：期望 %q，实际 %v", want, got)
		}
		data, ok := body["data"].(map[string]interface{})
		if !ok {
			t.Fatalf("data 字段缺失或类型不符：实际 %#v", body["data"])
		}
		if got, want := data["total"], float64(2); got != want {
			t.Errorf("data.total 错误：期望 %v，实际 %v", want, got)
		}
	})

	t.Run("Created 使用自定义 message 但仍是 HTTP 200", func(t *testing.T) {
		c, rec := newTestContext("/api/hosts", nil)
		Created(c, "添加成功", nil)

		if rec.Code != http.StatusOK {
			t.Errorf("HTTP 状态码错误：期望 %d（Created 刻意不用 201），实际 %d", http.StatusOK, rec.Code)
		}
		body := decodeBody(t, rec)
		if got, want := body["message"], "添加成功"; got != want {
			t.Errorf("message 错误：期望 %q，实际 %v", want, got)
		}
		if _, exists := body["data"]; !exists {
			t.Error("data 键缺失：Created 即使 data 为 nil 也必须带该键")
		}
	})
}

// TestFailResponseShape 校验失败响应结构。
//
// 风险点：Fail 用 HTTP 状态码同时充当业务 code，二者必须一致，否则前端按 code 分支判断会错。
// 现状：Fail **不写 data 键**（Success/Created 与 middleware.abortJSON 都写），
// 这是与统一契约 {code, message, data} 的偏差，本用例把现状钉住并在回报中列为疑似问题。
func TestFailResponseShape(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		message string
	}{
		{"400 参数错误", http.StatusBadRequest, "ID 参数非法"},
		{"403 权限不足", http.StatusForbidden, "需要管理员权限"},
		{"404 资源不存在", http.StatusNotFound, "虚拟机不存在"},
		{"500 内部错误", http.StatusInternalServerError, "操作失败"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := newTestContext("/api/vms", nil)
			Fail(c, tc.status, tc.message)

			if rec.Code != tc.status {
				t.Errorf("HTTP 状态码错误：期望 %d，实际 %d", tc.status, rec.Code)
			}
			body := decodeBody(t, rec)
			if got, want := body["code"], float64(tc.status); got != want {
				t.Errorf("响应 code 与 HTTP 状态码不一致：期望 %v，实际 %v", want, got)
			}
			if got := body["message"]; got != tc.message {
				t.Errorf("message 错误：期望 %q，实际 %v", tc.message, got)
			}
			if _, exists := body["data"]; exists {
				t.Errorf("现状变更：Fail 原本不写 data 键，实际写了 %#v（若为刻意补齐请同步更新本用例）",
					body["data"])
			}
		})
	}
}

// TestErrorResponseHidesInternalDetail 校验「完整错误进日志、前端只收中文」这条边界纪律。
//
// 风险点：这是 P0 批次修掉 36 处 err.Error() 泄漏后的回归锚点。一旦有人改回
// gin.H{"error": err.Error()}，响应体里会出现 libvirt socket 路径、SQL、XML 片段，
// 攻击者据此可判断宿主机部署形态。两个断言方向缺一不可：响应里没有、日志里有。
func TestErrorResponseHidesInternalDetail(t *testing.T) {
	const internal = "libvirt: internal error: /var/run/libvirt/libvirt-sock permission denied"
	err := fmt.Errorf("创建存储池失败: %w", errors.New(internal))

	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	defer log.SetOutput(io.Discard)

	c, rec := newTestContext("/api/storage/pools", nil)
	ErrorResponse(c, http.StatusInternalServerError, err)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("HTTP 状态码错误：期望 %d，实际 %d", http.StatusInternalServerError, rec.Code)
	}
	body := decodeBody(t, rec)
	if got, want := body["message"], "创建存储池失败"; got != want {
		t.Errorf("响应 message 错误：期望 %q，实际 %v", want, got)
	}
	for _, secret := range []string{"libvirt", "libvirt-sock", "/var/run", "permission denied"} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Errorf("响应体泄漏内部细节 %q：%s", secret, rec.Body.String())
		}
	}
	if !strings.Contains(logBuf.String(), internal) {
		t.Errorf("服务端日志缺少完整错误，故障将无从排查：\n日志=%q\n期望包含=%q", logBuf.String(), internal)
	}
	if !strings.Contains(logBuf.String(), "/api/storage/pools") {
		t.Errorf("服务端日志缺少请求路径，无法定位出错接口：日志=%q", logBuf.String())
	}
}

// TestErrorWithMessageUsesCallerText 校验调用方指定文案时，原始错误只进日志。
// 风险点：ErrorWithMessage 是「调用方已知友好文案」的通道，不能因为传了 message 就漏掉日志。
func TestErrorWithMessageUsesCallerText(t *testing.T) {
	const internal = "Error 1062: Duplicate entry 'web-01' for key 'vms.uuid'"
	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	defer log.SetOutput(io.Discard)

	c, rec := newTestContext("/api/vms", nil)
	ErrorWithMessage(c, http.StatusBadRequest, "虚拟机名称已存在", errors.New(internal))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("HTTP 状态码错误：期望 %d，实际 %d", http.StatusBadRequest, rec.Code)
	}
	body := decodeBody(t, rec)
	if got, want := body["message"], "虚拟机名称已存在"; got != want {
		t.Errorf("响应 message 错误：期望 %q，实际 %v", want, got)
	}
	if strings.Contains(rec.Body.String(), "Duplicate entry") || strings.Contains(rec.Body.String(), "1062") {
		t.Errorf("响应体泄漏了数据库原始错误：%s", rec.Body.String())
	}
	if !strings.Contains(logBuf.String(), internal) {
		t.Errorf("服务端日志缺少数据库原始错误：日志=%q", logBuf.String())
	}
}

// TestLogErrorNilSafe LogError(nil) 必须静默返回。
// 风险点：调用方常写 LogError(c, err) 而不判空，若不防御会打出无意义的 err=<nil> 噪音行。
func TestLogErrorNilSafe(t *testing.T) {
	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	defer log.SetOutput(io.Discard)

	c, _ := newTestContext("/api/vms", nil)
	LogError(c, nil)

	if logBuf.Len() != 0 {
		t.Errorf("LogError(nil) 不应写日志，实际写入：%q", logBuf.String())
	}
}
