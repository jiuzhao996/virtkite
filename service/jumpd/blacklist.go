package jumpd

import (
	"regexp"
	"strings"
)

// 命令黑名单：教学场景安全护栏——学生经跳板敲入高危命令（rm -rf / mkfs / dd 等）
// 时整行拦截、不送达目标机，并落审计留痕。
// 设计取舍（终审与实现讨论定稿）：默认逐字节透传（保护 vim/htop 等全屏程序交互），
// 仅在行结束（\r/\n）时对「该行输入」做黑名单判定；命中则向目标机发 Ctrl-U（0x15，
// 行清除）冲掉已回显的行，防止下次回车误执行残留命令。

// defaultCmdBlacklist 内置默认黑名单（子串匹配，小写归一后比较）
var defaultCmdBlacklist = []string{
	"rm -rf",
	"mkfs",
	"dd if=",
	"shred",
	":(){", // fork 炸弹
	"chmod -r 777 /",
	"wipefs",
}

// spacesRe 连续空白归一（rm  -rf 与 rm -rf 等价）
var spacesRe = regexp.MustCompile(`\s+`)

// ParseBlacklist 把设置值（逗号分隔）解析为黑名单条目；空值回退内置默认
func ParseBlacklist(raw string) []string {
	var out []string
	for _, item := range strings.Split(raw, ",") {
		item = strings.ToLower(strings.TrimSpace(item))
		if item != "" {
			out = append(out, item)
		}
	}
	if len(out) == 0 {
		return defaultCmdBlacklist
	}
	return out
}

// normalizeCmdLine 行归一：小写 + 连续空白折叠为单空格（抗 rm  -rf / RM -RF 类变体）
func normalizeCmdLine(line string) string {
	return strings.TrimSpace(spacesRe.ReplaceAllString(strings.ToLower(line), " "))
}

// matchBlacklist 行与黑名单匹配（归一化后子串包含）
func matchBlacklist(line string, entries []string) bool {
	if line == "" {
		return false
	}
	norm := normalizeCmdLine(line)
	for _, e := range entries {
		if strings.Contains(norm, e) {
			return true
		}
	}
	return false
}

// CmdBlacklistResolver 黑名单来源钩子：main 启动时接线到 settings（jumpd_cmd_blacklist 键）。
// 未接线或返回空时用内置默认——护栏不能因为没配置而消失（fail-safe）。
var CmdBlacklistResolver func() []string

// currentBlacklist 当前生效黑名单
func currentBlacklist() []string {
	if CmdBlacklistResolver != nil {
		if items := CmdBlacklistResolver(); len(items) > 0 {
			return items
		}
	}
	return defaultCmdBlacklist
}
