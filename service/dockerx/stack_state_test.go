package dockerx

import "testing"

// TestComposeStackState 覆盖 5 态状态机，重点是「任一 service exited 即整栈 EXITED」。
func TestComposeStackState(t *testing.T) {
	cases := []struct {
		name     string
		hasFile  bool
		deployed bool
		services []ComposeService
		want     string
	}{
		{"无文件未部署=未知", false, false, nil, StackStateUnknown},
		{"有文件未部署=草稿", true, false, nil, StackStateCreatedFile},
		{"已部署无服务容器=CREATED_STACK", true, true, nil, StackStateCreatedStack},
		{"全部运行=RUNNING", true, true, []ComposeService{{State: "running"}, {State: "running"}}, StackStateRunning},
		{"全部退出=EXITED", true, true, []ComposeService{{State: "exited"}, {State: "exited"}}, StackStateExited},
		{"部分运行部分退出=整栈 EXITED（无部分运行态）", true, true, []ComposeService{{State: "running"}, {State: "exited"}}, StackStateExited},
		{"dead 视为退出", true, true, []ComposeService{{State: "running"}, {State: "dead"}}, StackStateExited},
		{"created 非运行非退出=CREATED_STACK", true, true, []ComposeService{{State: "created"}}, StackStateCreatedStack},
		{"状态大小写与空白不敏感", true, true, []ComposeService{{State: " Running "}}, StackStateRunning},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ComposeStackState(tc.hasFile, tc.deployed, tc.services)
			if got != tc.want {
				t.Errorf("ComposeStackState(%v,%v,%v)=%q，期望 %q", tc.hasFile, tc.deployed, tc.services, got, tc.want)
			}
		})
	}
}
