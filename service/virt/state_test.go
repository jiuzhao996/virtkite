package virt

import (
	"testing"

	"github.com/digitalocean/go-libvirt"
	"github.com/jiuzhao/vmops/model"
)

// platformStatuses 平台允许写入 vms.status 列的全部取值。
//
// 这个集合就是 StateToPlatform 的值域契约：DB 列宽只有 20、前端状态色与文案映射
// （VmList.vue / VmDetail.vue / Dashboard.vue）以及 service/tasks 的状态回写全部按
// 这四个字面量硬匹配。一旦 StateToPlatform 漏出第五个取值，前端会渲染成空白且
// 任何状态筛选都命中不到，属于静默故障，因此值域必须由测试锁死。
var platformStatuses = map[string]bool{
	StatusRunning: true,
	StatusShutOff: true,
	StatusPaused:  true,
	StatusError:   true,
}

// TestStateToPlatform 覆盖 libvirt DomainState 全部 8 个枚举值以及越界输入。
//
// 风险点：libvirt 的 8 个域状态要被压缩进平台的 4 个状态，这个「多对一」映射
// 是手写 switch，任何一条 case 写错都会让虚拟机在列表页显示成错误的状态
// （例如把 shutdown「正在关机」错判成 error，用户会以为虚拟机坏了）。
// 越界值来自两个真实场景：libvirt 将来新增枚举值，以及 RPC 解码异常给出负数。
func TestStateToPlatform(t *testing.T) {
	tests := []struct {
		name  string
		state int32
		want  string
	}{
		{"nostate（0，libvirt 说不出状态）", int32(libvirt.DomainNostate), StatusError},
		{"running（1，运行中）", int32(libvirt.DomainRunning), StatusRunning},
		{"blocked（2，运行但阻塞在资源上）", int32(libvirt.DomainBlocked), StatusError},
		{"paused（3，已暂停）", int32(libvirt.DomainPaused), StatusPaused},
		{"shutdown（4，正在关机的过渡态）", int32(libvirt.DomainShutdown), StatusShutOff},
		{"shutoff（5，已关机）", int32(libvirt.DomainShutoff), StatusShutOff},
		{"crashed（6，崩溃）", int32(libvirt.DomainCrashed), StatusError},
		{"pmsuspended（7，节能挂起）", int32(libvirt.DomainPmsuspended), StatusError},
		{"越界值 8（libvirt 将来新增枚举）", 8, StatusError},
		{"越界值 99（明显非法）", 99, StatusError},
		{"负数 -1（RPC 解码异常）", -1, StatusError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StateToPlatform(tt.state)
			if got != tt.want {
				t.Errorf("StateToPlatform(%d) 期望 %q，实际 %q", tt.state, tt.want, got)
			}
			if !platformStatuses[got] {
				t.Errorf("StateToPlatform(%d) 返回了平台不认识的状态 %q，允许值只有 running/shut off/paused/error",
					tt.state, got)
			}
		})
	}
}

// TestStateToPlatformValueDomainClosed 扫描一段远超 libvirt 枚举范围的输入，
// 断言返回值永远落在四值集合内。
//
// 风险点：StateToPlatform 末尾的 default 分支是整条链路的兜底。将来有人为了
// 「支持更多状态」把 default 删掉或改成 return 原始字符串，编译不会报错，
// 单点用例也可能全过，但脏值会一路写进 DB。这条用例专门守住 default 分支。
func TestStateToPlatformValueDomainClosed(t *testing.T) {
	for state := int32(-16); state <= 128; state++ {
		got := StateToPlatform(state)
		if !platformStatuses[got] {
			t.Fatalf("StateToPlatform(%d) 返回越界状态 %q，值域必须限定在 running/shut off/paused/error 四值内",
				state, got)
		}
	}
}

// TestStatusConstantsMatchModel 交叉校验 virt.Status* 与 model.VMStatus* 逐字一致。
//
// 风险点：virt 是最底层的 libvirt 封装层，为了不把 GORM 反向拖进来，它刻意
// 不 import model，于是同一批状态字面量在 model/vm.go 与 service/virt/state.go
// 各定义了一份（两处注释互相引用，约定「改一处必须同步另一处」）。
// 人工同步一定会漏，这条用例就是那份约定的机器执行版本：
// 生产代码不能 import model，但测试代码可以，正好用来兜住这个刻意的重复。
func TestStatusConstantsMatchModel(t *testing.T) {
	tests := []struct {
		name       string
		virtConst  string
		modelConst string
	}{
		{"running", StatusRunning, model.VMStatusRunning},
		{"shut off（注意中间是空格）", StatusShutOff, model.VMStatusShutOff},
		{"paused", StatusPaused, model.VMStatusPaused},
		{"error", StatusError, model.VMStatusError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.virtConst != tt.modelConst {
				t.Errorf("virt 与 model 的状态常量不一致：virt=%q，model=%q（两处必须逐字相同）",
					tt.virtConst, tt.modelConst)
			}
		})
	}
}

// TestStatusShutOffLiteral 单独锁死 "shut off" 的字面写法。
//
// 风险点：这个值历史上被写成过 shut_off（下划线），model/vm.go 的 gorm
// default tag 里也曾是下划线版本。下划线与空格两种写法混用时，
// 状态筛选和前端映射都会静默失效，且 DB 里会同时存在两种关机态。
// 用例直接断言字面量，任何一方改写都会立刻失败。
func TestStatusShutOffLiteral(t *testing.T) {
	if StatusShutOff != "shut off" {
		t.Errorf("StatusShutOff 期望 %q，实际 %q", "shut off", StatusShutOff)
	}
	if StatusShutOff == "shut_off" {
		t.Error("StatusShutOff 退回了下划线写法 shut_off，会与前端状态映射和 DB 存量数据冲突")
	}
}
