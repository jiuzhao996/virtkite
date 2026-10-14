package handler

import (
	"encoding/json"
	"testing"
)

// TestJSONString JSON 字符串编码（含引号与转义）。
//
// 风险点：AI 请求 payload 是**手工字符串拼接**的 JSON——Chat() 里
// `{"role":%q,"content":%s}`，content 位置塞的正是本函数的产物。一旦本函数产出的
// 不是合法 JSON 字符串字面量（或转义不全），整段 payload 就会破坏，上游直接 400；
// 更糟的是用户输入里的引号/反斜杠/控制字符若不转义，等于把任意文本注入到请求体结构里
// （类 JSON 注入），可以构造出非预期的字段。所以这里必须逐类字符钉死：
// 合法 JSON（能被 json.Unmarshal 解码回原串）+ 特殊字符全部转义。
func TestJSONString(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"普通 ASCII", "hello world"},
		{"双引号必须转义", `她说"你好"`},
		{"反斜杠必须转义", `C:\path\to\file`},
		{"换行/制表/回车", "a\nb\tc\rd"},
		{"控制字符（0x00/0x01）", "x\x00\x01y"},
		{"中文与 emoji", "虚拟化 🚀 集群"},
		{"HTML 元字符", "<script>&\"'"},
		{"组合：引号+反斜杠+换行", "a\"b\\c\nd"},
		{"空串", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := jsonString(tc.in)
			// 1) 输出必须是合法 JSON 字符串字面量（带引号），可被解码
			var back string
			if err := json.Unmarshal([]byte(got), &back); err != nil {
				t.Fatalf("jsonString(%q) 产出非法 JSON %q：%v", tc.in, got, err)
			}
			// 2) 往返必须无损（转义正确性的根判据）
			if back != tc.in {
				t.Errorf("往返不一致：输入 %q，解码回 %q（编码 %q）", tc.in, back, got)
			}
			// 3) 必须以引号包裹（拼接进 `"content":%s` 需要它自带引号）
			if len(got) < 2 || got[0] != '"' || got[len(got)-1] != '"' {
				t.Errorf("jsonString(%q) 未用引号包裹：%q", tc.in, got)
			}
		})
	}

	// 关键安全性质：用户输入里的裸引号不得原样出现在产物中（否则破坏外层拼接结构）
	if got := jsonString(`a"b`); got == `"a"b"` {
		t.Errorf("双引号未转义，payload 结构会被破坏：%q", got)
	}
	// 控制字符必须被转义成 \uXXXX，不能留裸字节进请求体
	if got := jsonString("\x01"); got == "\"\x01\"" {
		t.Errorf("控制字符未转义：%q", got)
	}
}
