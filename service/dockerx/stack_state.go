// compose 栈状态机（借 Dockge 的 5 态模型）。
//
// Dockge 的取值：UNKNOWN / CREATED_FILE（草稿：文件在但未部署）/ CREATED_STACK / RUNNING / EXITED。
// 关键约定：**任一 service exited 即整栈 EXITED**——不存在「部分运行」态，因为栈是一个整体，
// 有一个服务挂了就说明这个栈没在正常服务，展示成「部分运行」反而误导用户。
//
// 注意：本状态机只描述 compose 栈自身（部署与否 + 容器运行态），**不做漂移检测**——
// 「配置改了但没重新部署」的漂移由设计器模块（handler/designer.go）负责，两者不要混。
// 这里的 CREATED_FILE 仅表示「部署副本文件在磁盘上，但 docker compose ls 里没有该项目」。
package dockerx

import "strings"

// 栈状态常量（与 Dockge 5 态对齐，前端据此着色）。
const (
	StackStateUnknown      = "unknown"       // 既无文件也未部署
	StackStateCreatedFile  = "created_file"  // 草稿：文件在但未部署
	StackStateCreatedStack = "created_stack" // 已部署但无运行中的服务容器
	StackStateRunning      = "running"       // 全部服务在运行
	StackStateExited       = "exited"        // 至少一个服务已退出（整栈判定为 EXITED）
)

// ComposeStackState 计算栈状态（纯函数，可单测）。
//   - hasFile：栈的 compose 文件是否存在于磁盘；
//   - deployed：项目是否出现在 docker compose ls（即已 up 过）；
//   - services：项目内的服务容器（compose ps -a）。
func ComposeStackState(hasFile, deployed bool, services []ComposeService) string {
	if !deployed {
		if hasFile {
			return StackStateCreatedFile
		}
		return StackStateUnknown
	}
	if len(services) == 0 {
		return StackStateCreatedStack
	}
	anyRunning := false
	for _, s := range services {
		switch strings.ToLower(strings.TrimSpace(s.State)) {
		case "exited", "dead":
			return StackStateExited // 任一 exited/dead 即整栈 EXITED
		case "running":
			anyRunning = true
		}
	}
	if anyRunning {
		return StackStateRunning
	}
	return StackStateCreatedStack // 有容器但既非 running 也非 exited（如 created/paused）
}
