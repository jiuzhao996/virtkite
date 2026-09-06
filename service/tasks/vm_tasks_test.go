package tasks

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/virt"
	"gorm.io/gorm"
)

// realCreateVMPayload 一份贴近真实请求的 create_vm 任务参数。
//
// handler/vm.go 的 CreateVM 是把**原始请求体**直接 json.Unmarshal 成
// map[string]interface{} 后交给 Submit 的，Submit 再 json.Marshal 存进 tasks.payload，
// worker 取出时又 json.Unmarshal 回 map —— 全链路都经过 JSON，
// 所以 executor 里看到的数字**一律是 float64**，不会是 int。
// 这是 strParam/intParam 必须存在的根本原因，也是本文件最关键的一组用例。
const realCreateVMPayload = `{
  "name": "web-01",
  "host_id": 1,
  "storage_pool": "vmops",
  "vcpu": 2,
  "memory_mb": 2048,
  "disk_gb": 40,
  "network": "default",
  "disks": [
    {"create_gb": 40, "cloud_init": {"hostname": "web-01", "user": "ubuntu", "password": "p@ss",
      "net_mode": "static", "ip": "192.168.122.50", "gateway": "192.168.122.1",
      "dns": ["223.5.5.5", "8.8.8.8"]}},
    {"source_image_id": 3}
  ],
  "interfaces": [
    {"type": "network", "source": "default", "mac": "52:54:00:aa:bb:cc", "model": "virtio"},
    {"type": "bridge", "source": "br0"}
  ]
}`

// decodePayload 把 JSON 文本解析成 worker 实际拿到的 payload 形态。
func decodePayload(t *testing.T, raw string) map[string]interface{} {
	t.Helper()
	payload := map[string]interface{}{}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("测试用 payload 不是合法 JSON: %v", err)
	}
	return payload
}

// TestStrParam 覆盖 payload 取字符串。
//
// 风险点：payload 的键值完全来自 HTTP 请求体，类型不受控。裸断言 v.(string) 遇到
// 数字/布尔/对象会直接 panic，而 executor 的 panic 只能靠 worker 的 recover 兜住
// （任务失败但进程不死）—— 与其依赖兜底，不如在取值处就带 ok 降级。
func TestStrParam(t *testing.T) {
	payload := decodePayload(t, `{
		"name": "web-01",
		"empty": "",
		"vcpu": 2,
		"enabled": true,
		"nested": {"a": 1},
		"list": ["a"],
		"nullval": null,
		"numeric_string": "42",
		"chinese": "中文名称"
	}`)

	cases := []struct {
		name   string
		key    string
		want   string
		wantOK bool
	}{
		{"正常字符串", "name", "web-01", true},
		{"空字符串（存在但为空，ok 仍为 true）", "empty", "", true},
		{"数字字符串按字符串取出", "numeric_string", "42", true},
		{"中文值原样取出", "chinese", "中文名称", true},
		{"键不存在", "missing", "", false},
		{"值是数字（JSON 里是 float64）", "vcpu", "", false},
		{"值是布尔", "enabled", "", false},
		{"值是对象", "nested", "", false},
		{"值是数组", "list", "", false},
		{"值是 null", "nullval", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := strParam(payload, tc.key)
			if ok != tc.wantOK {
				t.Fatalf("strParam(%q) 判定错误：期望 ok=%v，实际 ok=%v（值=%q）", tc.key, tc.wantOK, ok, got)
			}
			if got != tc.want {
				t.Errorf("strParam(%q) 值错误：期望 %q，实际 %q", tc.key, tc.want, got)
			}
		})
	}

	t.Run("payload 为 nil 时不 panic", func(t *testing.T) {
		got, ok := strParam(nil, "name")
		if ok || got != "" {
			t.Errorf("期望 (\"\", false)，实际 (%q, %v)", got, ok)
		}
	})
}

// TestFloatParam 覆盖 payload 取数值时的类型兼容。
//
// 风险点：同一个 payload 有两种来路 ——
//   - 经 DB 往返（json.Unmarshal）时数字是 float64；
//   - 由 handler 直接构造 map 传给 Submit 时可能是 int/uint 字面量。
//
// 两种都必须支持，否则「同一段代码在不同调用路径下取不到值」，会退化成静默用默认值。
func TestFloatParam(t *testing.T) {
	cases := []struct {
		name   string
		value  interface{}
		want   float64
		wantOK bool
	}{
		{"float64（JSON 反序列化的常态）", float64(2), 2, true},
		{"float64 小数", float64(2.5), 2.5, true},
		{"float32", float32(1.5), 1.5, true},
		{"int（handler 直传）", 4, 4, true},
		{"int32", int32(8), 8, true},
		{"int64", int64(16), 16, true},
		{"uint", uint(32), 32, true},
		{"uint64", uint64(64), 64, true},
		{"json.Number 整数", json.Number("128"), 128, true},
		{"json.Number 小数", json.Number("1.5"), 1.5, true},
		{"json.Number 非法内容", json.Number("abc"), 0, false},
		{"零值", float64(0), 0, true},
		{"负数", float64(-1), -1, true},
		{"字符串数字（不做隐式转换）", "42", 0, false},
		{"布尔", true, 0, false},
		{"nil 值", nil, 0, false},
		{"对象", map[string]interface{}{"a": 1}, 0, false},
		{"数组", []interface{}{1}, 0, false},
		// uint32/int8 等未在 switch 里列举的整型走 default 分支，取不到值
		{"uint32（实现未覆盖的整型，返回 false）", uint32(7), 0, false},
		{"int8（实现未覆盖的整型，返回 false）", int8(7), 0, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := map[string]interface{}{"k": tc.value}
			got, ok := floatParam(payload, "k")
			if ok != tc.wantOK {
				t.Fatalf("floatParam 判定错误：值 %#v 期望 ok=%v，实际 ok=%v（返回 %v）",
					tc.value, tc.wantOK, ok, got)
			}
			if got != tc.want {
				t.Errorf("floatParam 值错误：值 %#v 期望 %v，实际 %v", tc.value, tc.want, got)
			}
		})
	}

	t.Run("键不存在", func(t *testing.T) {
		if got, ok := floatParam(map[string]interface{}{}, "k"); ok || got != 0 {
			t.Errorf("期望 (0, false)，实际 (%v, %v)", got, ok)
		}
	})

	t.Run("payload 为 nil 时不 panic", func(t *testing.T) {
		if got, ok := floatParam(nil, "k"); ok || got != 0 {
			t.Errorf("期望 (0, false)，实际 (%v, %v)", got, ok)
		}
	})
}

// TestIntParam 覆盖整数取值（float64 → int 的转换语义）。
//
// 风险点：intParam 只是 floatParam 的 int 转换封装，**不做范围校验**。
// vcpu/memory_mb/disk_gb 都走它，而 handler 侧也没有范围校验（payload 是原始请求体直传），
// 因此负数、小数、超大值都能落到 virt.DomainSpec 上（详见回报「疑似问题」）。
// 这里断言的是「当前真实行为」，不是「应该的行为」。
func TestIntParam(t *testing.T) {
	cases := []struct {
		name   string
		value  interface{}
		want   int
		wantOK bool
	}{
		{"整数 float64", float64(2), 2, true},
		{"小数向零截断（2.9 → 2）", float64(2.9), 2, true},
		{"负小数向零截断（-2.9 → -2）", float64(-2.9), -2, true},
		{"零", float64(0), 0, true},
		{"负数（实现不拒绝，直接透传）", float64(-4), -4, true},
		{"int 直传", 8, 8, true},
		{"字符串数字：取不到", "8", 0, false},
		{"nil：取不到", nil, 0, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := intParam(map[string]interface{}{"k": tc.value}, "k")
			if ok != tc.wantOK {
				t.Fatalf("intParam 判定错误：值 %#v 期望 ok=%v，实际 ok=%v", tc.value, tc.wantOK, ok)
			}
			if got != tc.want {
				t.Errorf("intParam 值错误：值 %#v 期望 %d，实际 %d", tc.value, tc.want, got)
			}
		})
	}

	// float64 只有 53 位有效位：超过 2^53 的整数在 JSON 往返后就已经不精确了，
	// 这不是 intParam 的缺陷而是 JSON 数字的固有限制（前端传 id 用字符串才安全）。
	t.Run("超过 2^53 的整数在 JSON 往返后丢精度", func(t *testing.T) {
		payload := decodePayload(t, `{"big": 9007199254740993}`) // 2^53+1
		got, ok := intParam(payload, "big")
		if !ok {
			t.Fatal("期望能取到值")
		}
		if got != 9007199254740992 { // 2^53，末位 1 已丢失
			t.Errorf("精度损失行为变化：期望 %d，实际 %d", 9007199254740992, got)
		}
	})

	// float→int 溢出时的结果按 Go 规范是「实现相关」，因此只断言「能取到值、不 panic」，
	// 不断言具体数值（在 amd64 上是 int64 最小值，换平台可能不同）。
	t.Run("1e19 溢出 int64：仍返回 ok=true（无范围校验）", func(t *testing.T) {
		payload := decodePayload(t, `{"vcpu": 1e19}`)
		got, ok := intParam(payload, "vcpu")
		if !ok {
			t.Fatal("现状是照常返回 ok=true（未做范围校验），若已加校验请更新本用例")
		}
		t.Logf("溢出结果为实现相关值：%d（Go 规范未定义，故不做数值断言）", got)
	})
}

// TestIntParamFromRealPayload 用真实 create_vm 参数验证「JSON 数字都是 float64」这条前提。
// 风险点：如果哪天有人把 intParam 改成裸 v.(int) 断言，本用例会立刻失败 ——
// 而线上表现只是「所有虚拟机都用默认 1 核 1024MB 创建」，非常难察觉。
func TestIntParamFromRealPayload(t *testing.T) {
	payload := decodePayload(t, realCreateVMPayload)

	// 先证明前提：payload 里的数字确实是 float64 而不是 int
	if _, isFloat := payload["vcpu"].(float64); !isFloat {
		t.Fatalf("前提不成立：vcpu 的实际类型是 %T，本套取值函数的设计依据需要重新评估", payload["vcpu"])
	}
	if _, isInt := payload["vcpu"].(int); isInt {
		t.Fatal("前提不成立：vcpu 竟然是 int，说明 payload 未经 JSON 往返")
	}

	intCases := []struct {
		key  string
		want int
	}{
		{"vcpu", 2},
		{"memory_mb", 2048},
		{"disk_gb", 40},
		{"host_id", 1},
	}
	for _, tc := range intCases {
		t.Run("整数字段 "+tc.key, func(t *testing.T) {
			got, ok := intParam(payload, tc.key)
			if !ok {
				t.Fatalf("未取到 %q，executor 会静默使用默认值", tc.key)
			}
			if got != tc.want {
				t.Errorf("%q 值错误：期望 %d，实际 %d", tc.key, tc.want, got)
			}
		})
	}

	strCases := []struct {
		key  string
		want string
	}{
		{"name", "web-01"},
		{"storage_pool", "vmops"},
		{"network", "default"},
	}
	for _, tc := range strCases {
		t.Run("字符串字段 "+tc.key, func(t *testing.T) {
			got, ok := strParam(payload, tc.key)
			if !ok {
				t.Fatalf("未取到 %q", tc.key)
			}
			if got != tc.want {
				t.Errorf("%q 值错误：期望 %q，实际 %q", tc.key, tc.want, got)
			}
		})
	}
}

// TestParseTaskInterfaces 覆盖网卡列表解析。
//
// 风险点：网卡数组来自请求体，元素可能不是对象、字段可能缺失或类型不符。
// 解析器必须逐层带 ok 跳过脏数据，而不是 panic 掉 worker；
// 同时不能把脏元素解析成「空网卡」塞进 DomainSpec（会生成非法域 XML）。
func TestParseTaskInterfaces(t *testing.T) {
	t.Run("正常两块网卡（含字段缺失的第二块）", func(t *testing.T) {
		payload := decodePayload(t, realCreateVMPayload)
		got := parseTaskInterfaces(payload["interfaces"])

		if len(got) != 2 {
			t.Fatalf("网卡数量错误：期望 2，实际 %d（%#v）", len(got), got)
		}
		want0 := virt.InterfaceSpec{Type: "network", Source: "default", MAC: "52:54:00:aa:bb:cc", Model: "virtio"}
		if got[0] != want0 {
			t.Errorf("第一块网卡解析错误：\n期望 %+v\n实际 %+v", want0, got[0])
		}
		// 第二块只给了 type/source，mac 与 model 应为空（由 virt 层兜默认值）
		want1 := virt.InterfaceSpec{Type: "bridge", Source: "br0"}
		if got[1] != want1 {
			t.Errorf("第二块网卡解析错误：\n期望 %+v\n实际 %+v", want1, got[1])
		}
	})

	cases := []struct {
		name    string
		raw     string // interfaces 字段的 JSON
		wantLen int
	}{
		{"空数组", `[]`, 0},
		{"null", `null`, 0},
		{"不是数组而是对象", `{"type":"network"}`, 0},
		{"不是数组而是字符串", `"default"`, 0},
		{"数组元素是字符串（跳过）", `["default","br0"]`, 0},
		{"数组元素是数字（跳过）", `[1,2]`, 0},
		{"数组元素是 null（跳过）", `[null]`, 0},
		{"混合：一个合法 + 两个脏元素", `[{"type":"network"},"x",null]`, 1},
		{"字段类型全错（元素保留但字段为空）", `[{"type":1,"source":true,"mac":[],"model":{}}]`, 1},
		{"空对象（元素保留，字段为空）", `[{}]`, 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var raw interface{}
			if err := json.Unmarshal([]byte(tc.raw), &raw); err != nil {
				t.Fatalf("测试数据非法 JSON: %v", err)
			}
			got := parseTaskInterfaces(raw)
			if len(got) != tc.wantLen {
				t.Fatalf("网卡数量错误：期望 %d，实际 %d（%#v）", tc.wantLen, len(got), got)
			}
		})
	}

	t.Run("字段类型错误时留空值而不是塞入脏数据", func(t *testing.T) {
		var raw interface{}
		_ = json.Unmarshal([]byte(`[{"type":1,"source":true,"mac":[],"model":{}}]`), &raw)
		got := parseTaskInterfaces(raw)
		if len(got) != 1 {
			t.Fatalf("期望保留 1 个元素，实际 %d", len(got))
		}
		if got[0] != (virt.InterfaceSpec{}) {
			t.Errorf("类型错误的字段应留空，实际 %+v", got[0])
		}
	})

	t.Run("非数组输入返回 nil 而不是空切片", func(t *testing.T) {
		if got := parseTaskInterfaces("not-an-array"); got != nil {
			t.Errorf("期望 nil（调用方据此判断「未提供网卡」），实际 %#v", got)
		}
	})
}

// TestParseTaskDisks 覆盖磁盘列表解析（三种互斥形态：新建卷/引用现有卷/引用云镜像）。
//
// 风险点：source_image_id 走 intParam，JSON 里是 float64；若解析失败会退化成 0，
// 「基于云镜像创建」就会静默变成「建一块空盘」—— 用户拿到一台开不了机的虚拟机。
func TestParseTaskDisks(t *testing.T) {
	t.Run("真实参数：新建卷 + 引用云镜像", func(t *testing.T) {
		payload := decodePayload(t, realCreateVMPayload)
		got := parseTaskDisks(payload["disks"])

		if len(got) != 2 {
			t.Fatalf("磁盘数量错误：期望 2，实际 %d", len(got))
		}
		if got[0].CreateGB != 40 {
			t.Errorf("第一块盘容量错误：期望 40，实际 %d", got[0].CreateGB)
		}
		if got[0].CloudInit == nil {
			t.Fatal("第一块盘的 cloud_init 未解析")
		}
		if got[0].CloudInit.Hostname != "web-01" {
			t.Errorf("cloud-init hostname 错误：期望 web-01，实际 %q", got[0].CloudInit.Hostname)
		}
		if got[1].SourceImageID != 3 {
			t.Errorf("第二块盘的 source_image_id 错误：期望 3，实际 %d（会退化成建空盘）", got[1].SourceImageID)
		}
	})

	cases := []struct {
		name    string
		raw     string
		wantLen int
		check   func(t *testing.T, disks []createDiskReq)
	}{
		{"空数组", `[]`, 0, nil},
		{"null", `null`, 0, nil},
		{"不是数组", `{"create_gb":10}`, 0, nil},
		{"元素是字符串（跳过）", `["10G"]`, 0, nil},
		{"source_image_id 为 0（视为未提供）", `[{"source_image_id":0}]`, 1,
			func(t *testing.T, d []createDiskReq) {
				if d[0].SourceImageID != 0 {
					t.Errorf("期望 0，实际 %d", d[0].SourceImageID)
				}
			}},
		{"source_image_id 为负（实现只接受 >0，故留 0）", `[{"source_image_id":-3}]`, 1,
			func(t *testing.T, d []createDiskReq) {
				if d[0].SourceImageID != 0 {
					t.Errorf("负数应被忽略，实际 %d", d[0].SourceImageID)
				}
			}},
		{"source 为路径字符串", `[{"source":"/var/lib/libvirt/images/base.qcow2"}]`, 1,
			func(t *testing.T, d []createDiskReq) {
				if d[0].Source != "/var/lib/libvirt/images/base.qcow2" {
					t.Errorf("source 解析错误：实际 %q", d[0].Source)
				}
			}},
		{"cloud_init 不是对象（留 nil）", `[{"create_gb":10,"cloud_init":"yes"}]`, 1,
			func(t *testing.T, d []createDiskReq) {
				if d[0].CloudInit != nil {
					t.Errorf("非对象的 cloud_init 应留 nil，实际 %+v", d[0].CloudInit)
				}
			}},
		{"create_gb 是字符串（留 0）", `[{"create_gb":"40"}]`, 1,
			func(t *testing.T, d []createDiskReq) {
				if d[0].CreateGB != 0 {
					t.Errorf("字符串容量不应被隐式转换，实际 %d", d[0].CreateGB)
				}
			}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var raw interface{}
			if err := json.Unmarshal([]byte(tc.raw), &raw); err != nil {
				t.Fatalf("测试数据非法 JSON: %v", err)
			}
			got := parseTaskDisks(raw)
			if len(got) != tc.wantLen {
				t.Fatalf("磁盘数量错误：期望 %d，实际 %d（%#v）", tc.wantLen, len(got), got)
			}
			if tc.check != nil {
				tc.check(t, got)
			}
		})
	}
}

// TestParseTaskCloudInit 覆盖 cloud-init 配置解析（写进 seed ISO 的 user-data/network-config）。
//
// 风险点：dns 是数组，元素类型不受控；实现要求逐元素断言并过滤空串，
// 否则会把空条目写进 network-config，cloud-init 解析失败 → 虚拟机拿不到网络配置。
func TestParseTaskCloudInit(t *testing.T) {
	t.Run("完整配置", func(t *testing.T) {
		payload := decodePayload(t, realCreateVMPayload)
		disks := parseTaskDisks(payload["disks"])
		got := disks[0].CloudInit
		if got == nil {
			t.Fatal("cloud_init 未解析")
		}
		want := virt.CloudInitSpec{
			Hostname: "web-01", User: "ubuntu", Password: "p@ss",
			NetMode: "static", IP: "192.168.122.50", Gateway: "192.168.122.1",
			DNS: []string{"223.5.5.5", "8.8.8.8"},
		}
		if got.Hostname != want.Hostname || got.User != want.User || got.Password != want.Password ||
			got.NetMode != want.NetMode || got.IP != want.IP || got.Gateway != want.Gateway {
			t.Errorf("标量字段解析错误：\n期望 %+v\n实际 %+v", want, *got)
		}
		if len(got.DNS) != 2 || got.DNS[0] != "223.5.5.5" || got.DNS[1] != "8.8.8.8" {
			t.Errorf("DNS 解析错误：期望 %v，实际 %v", want.DNS, got.DNS)
		}
	})

	cases := []struct {
		name  string
		raw   string
		check func(t *testing.T, spec *virt.CloudInitSpec)
	}{
		{"不是对象：返回 nil", `"hostname=web"`, func(t *testing.T, s *virt.CloudInitSpec) {
			if s != nil {
				t.Errorf("期望 nil，实际 %+v", s)
			}
		}},
		{"null：返回 nil", `null`, func(t *testing.T, s *virt.CloudInitSpec) {
			if s != nil {
				t.Errorf("期望 nil，实际 %+v", s)
			}
		}},
		{"空对象：返回零值 spec（非 nil）", `{}`, func(t *testing.T, s *virt.CloudInitSpec) {
			if s == nil {
				t.Fatal("期望非 nil 的零值 spec")
			}
			// CloudInitSpec 含切片字段不可直接比较，逐字段判空
			if s.Hostname != "" || s.User != "" || s.Password != "" || s.SSHKey != "" ||
				s.NetMode != "" || s.IP != "" || s.Gateway != "" || s.DNS != nil {
				t.Errorf("期望全部字段为零值，实际 %+v", s)
			}
		}},
		{"字段类型全错：全部留空", `{"hostname":1,"user":true,"password":[],"ip":{}}`,
			func(t *testing.T, s *virt.CloudInitSpec) {
				if s == nil {
					t.Fatal("期望非 nil")
				}
				if s.Hostname != "" || s.User != "" || s.Password != "" || s.IP != "" {
					t.Errorf("类型错误的字段应留空，实际 %+v", s)
				}
			}},
		{"dns 不是数组：留 nil", `{"dns":"223.5.5.5"}`, func(t *testing.T, s *virt.CloudInitSpec) {
			if s.DNS != nil {
				t.Errorf("非数组的 dns 应留 nil，实际 %v", s.DNS)
			}
		}},
		{"dns 含非字符串与空串：只保留合法项", `{"dns":["1.1.1.1",2,null,"","8.8.8.8"]}`,
			func(t *testing.T, s *virt.CloudInitSpec) {
				want := []string{"1.1.1.1", "8.8.8.8"}
				if len(s.DNS) != len(want) || s.DNS[0] != want[0] || s.DNS[1] != want[1] {
					t.Errorf("dns 过滤错误：期望 %v，实际 %v", want, s.DNS)
				}
			}},
		{"dns 为空数组：得到空切片", `{"dns":[]}`, func(t *testing.T, s *virt.CloudInitSpec) {
			if s.DNS == nil || len(s.DNS) != 0 {
				t.Errorf("期望长度 0 的切片，实际 %#v", s.DNS)
			}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var raw interface{}
			if err := json.Unmarshal([]byte(tc.raw), &raw); err != nil {
				t.Fatalf("测试数据非法 JSON: %v", err)
			}
			tc.check(t, parseTaskCloudInit(raw))
		})
	}
}

// TestTasksValidateVMName 覆盖 tasks 包内的虚拟机名校验。
//
// 风险点：名称会用于生成卷名（<name>.qcow2）、seed ISO 名与 libvirt 域名。
// 这份实现是 handler/vm.go 的**独立副本**（taskVMNameRegex），两处必须同步 ——
// executor 侧的校验是最后一道闸门：handler 提交任务时校验过，但 payload 存进 DB 后
// 若被直接改库或经由其他入口提交，executor 仍要自己拦一次。
func TestTasksValidateVMName(t *testing.T) {
	cases := []struct {
		name   string
		vmName string
		want   bool
	}{
		{"常规名称", "web-01", true},
		{"下划线", "web_01", true},
		{"纯数字", "202509", true},
		{"大小写混合", "WebSrv", true},
		{"空串", "", false},
		{"含点号", "web.01", false},
		{"含空格", "web 01", false},
		{"含斜杠（路径穿越形态）", "../web", false},
		{"含中文", "网站", false},
		{"含分号", "web;id", false},
		{"含尖括号（XML 闭合）", "web</name>", false},
		{"含引号", `web"01`, false},
		{"含换行", "web\n01", false},
		{"含 NUL", "web\x00", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := validateVMName(tc.vmName); got != tc.want {
				t.Errorf("validateVMName(%q) 判定错误：期望 %v，实际 %v", tc.vmName, tc.want, got)
			}
		})
	}

	t.Run("与 handler 侧规则保持一致的关键差异：不允许点号", func(t *testing.T) {
		// 卷名允许点（img.qcow2），VM 名不允许 —— 混淆这两套规则会让 XML 生成出意外结果
		if validateVMName("web.qcow2") {
			t.Error("VM 名不应允许点号（卷名才允许）")
		}
	})
}

// macFormatTasks / uuidV4FormatTasks 与 service/virt、handler 包内的同名校验一致。
var (
	macFormatTasks    = regexp.MustCompile(`^52:54:00:[0-9a-f]{2}:[0-9a-f]{2}:[0-9a-f]{2}$`)
	uuidV4FormatTasks = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

// TestRandomMAC 覆盖 MAC 生成。
//
// 风险点：52:54:00 是 KVM/libvirt 的保留前缀（第二个字节的 bit1 为 1 表示本地管理地址，
// bit0 为 0 表示单播），前缀写错可能与真实厂商 MAC 冲突。
// 重复的 MAC 会让同网段两台机 ARP 冲突、网络双双不可用（P2 批次修过一次同类缺陷）。
func TestRandomMAC(t *testing.T) {
	const n = 500
	seen := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		mac, err := randomMAC()
		if err != nil {
			t.Fatalf("第 %d 次生成失败: %v", i, err)
		}
		if !macFormatTasks.MatchString(mac) {
			t.Fatalf("格式不符（期望 52:54:00:xx:xx:xx 全小写十六进制）：实际 %q", mac)
		}
		if seen[mac] {
			t.Fatalf("%d 次生成出现重复 MAC：%s（同网段会 ARP 冲突）", n, mac)
		}
		seen[mac] = true
	}
	if len(seen) != n {
		t.Errorf("去重后数量错误：期望 %d，实际 %d", n, len(seen))
	}
}

// TestRandomUUID 覆盖 UUID 生成。
// 风险点：UUID 会写进 vms.uuid（唯一索引）与 libvirt 域定义，重复即创建失败；
// 版本位/变体位写错会让部分工具（virt-manager 等）判定 XML 非法。
func TestRandomUUID(t *testing.T) {
	const n = 500
	seen := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		uuid, err := randomUUID()
		if err != nil {
			t.Fatalf("第 %d 次生成失败: %v", i, err)
		}
		if !uuidV4FormatTasks.MatchString(uuid) {
			t.Fatalf("不符合 RFC 4122 v4（第三段须以 4 开头，第四段首字符须为 8/9/a/b）：实际 %q", uuid)
		}
		if len(uuid) != 36 {
			t.Fatalf("长度错误：期望 36，实际 %d（%q）", len(uuid), uuid)
		}
		if seen[uuid] {
			t.Fatalf("%d 次生成出现重复 UUID：%s（会撞 vms.uuid 唯一索引）", n, uuid)
		}
		seen[uuid] = true
	}
}

// TestCheckExecContext 覆盖 executor 上下文的前置校验。
//
// 风险点：executor 拿到的 ctx 由 manager 组装，任一字段为 nil 后续都是裸解引用 panic。
// 这个函数把「panic 崩 worker」转成「任务失败 + 中文原因」，是所有 executor 的第一行代码。
func TestCheckExecContext(t *testing.T) {
	fullCtx := func() *ExecContext {
		return &ExecContext{
			DB:      &gorm.DB{}, // 不建连，只需非 nil
			Virt:    virt.New(), // 惰性连接，构造时不碰 libvirt
			Task:    &model.Task{ID: 1},
			Payload: map[string]interface{}{"a": 1},
		}
	}

	cases := []struct {
		name    string
		build   func() *ExecContext
		wantErr string // 期望错误文案（空串表示应通过）
	}{
		{"字段齐全：通过", fullCtx, ""},
		{"ctx 为 nil", func() *ExecContext { return nil }, "任务上下文为空"},
		{"DB 为 nil", func() *ExecContext { c := fullCtx(); c.DB = nil; return c }, "任务数据库连接为空"},
		{"Virt 为 nil", func() *ExecContext { c := fullCtx(); c.Virt = nil; return c }, "任务虚拟化服务为空"},
		{"Task 为 nil", func() *ExecContext { c := fullCtx(); c.Task = nil; return c }, "任务记录为空"},
		{"Payload 为 nil", func() *ExecContext { c := fullCtx(); c.Payload = nil; return c }, "缺少任务参数"},
		{"Payload 为空 map：允许（合法的无参任务）",
			func() *ExecContext { c := fullCtx(); c.Payload = map[string]interface{}{}; return c }, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkExecContext(tc.build())
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("期望通过，实际报错: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("期望报错 %q，实际通过", tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Errorf("错误文案错误：期望 %q，实际 %q", tc.wantErr, err.Error())
			}
			if !containsCJK(err.Error()) {
				t.Errorf("错误文案必须是中文（会写进 tasks.error 回显前端）：%q", err.Error())
			}
		})
	}
}

// TestTaskSeedDir 覆盖 cloud-init seed 目录取值。
//
// 风险点：worker 在后台线程跑，不能假设 config.GlobalConfig 已初始化
// （单测、脚本化调用都可能没走 config.Init），否则空指针 panic 崩 worker。
// 用完立即恢复全局配置，避免污染同包其他用例。
func TestTaskSeedDir(t *testing.T) {
	original := config.GlobalConfig
	defer func() { config.GlobalConfig = original }()

	// 该默认值是写死在代码里的绝对路径（含用户名），换机器部署时需要靠环境变量覆盖，
	// 已在回报中列为待改进项。这里断言现状，改动时会失败提醒同步。
	const hardcodedDefault = "/home/jiuzhao/vmops/data/seed"

	cases := []struct {
		name string
		cfg  *config.Config
		want string
	}{
		{"配置未初始化：回落到内置默认值", nil, hardcodedDefault},
		{"配置里 SeedDir 为空：回落到内置默认值", &config.Config{}, hardcodedDefault},
		{"配置里有值：使用配置值", &config.Config{SeedDir: "/data/seed"}, "/data/seed"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config.GlobalConfig = tc.cfg
			if got := taskSeedDir(); got != tc.want {
				t.Errorf("taskSeedDir() 错误：期望 %q，实际 %q", tc.want, got)
			}
		})
	}
}

// TestSetTaskResultVM 覆盖任务结果回填。
//
// 风险点：Result 是 JSON 字符串，前端任务详情按键读取（含 P2 新增的 kept_volumes 数组）。
// VMID 必须存**值的副本**指针：直接取参数地址在循环里复用会让所有任务指向同一个变量。
func TestSetTaskResultVM(t *testing.T) {
	t.Run("正常回填结果与 VM 关联", func(t *testing.T) {
		ctx := &ExecContext{Task: &model.Task{ID: 1}}
		setTaskResultVM(ctx, map[string]interface{}{
			"vm_name":      "web-01",
			"kept_volumes": []string{"base.qcow2（是镜像库登记的共享基镜像）"},
		}, 7, "web-01")

		if ctx.Task.VMID == nil {
			t.Fatal("VMID 未回填")
		}
		if *ctx.Task.VMID != 7 {
			t.Errorf("VMID 错误：期望 7，实际 %d", *ctx.Task.VMID)
		}
		if ctx.Task.VMName != "web-01" {
			t.Errorf("VMName 错误：期望 web-01，实际 %q", ctx.Task.VMName)
		}

		var result map[string]interface{}
		if err := json.Unmarshal([]byte(ctx.Task.Result), &result); err != nil {
			t.Fatalf("Result 不是合法 JSON：err=%v 内容=%q", err, ctx.Task.Result)
		}
		if result["vm_name"] != "web-01" {
			t.Errorf("Result.vm_name 错误：实际 %v", result["vm_name"])
		}
		kept, ok := result["kept_volumes"].([]interface{})
		if !ok || len(kept) != 1 {
			t.Fatalf("Result.kept_volumes 错误：实际 %#v", result["kept_volumes"])
		}
		if !strings.Contains(kept[0].(string), "共享基镜像") {
			t.Errorf("保留原因未透出：实际 %v", kept[0])
		}
	})

	t.Run("两次调用的 VMID 指针互相独立", func(t *testing.T) {
		a := &ExecContext{Task: &model.Task{ID: 1}}
		b := &ExecContext{Task: &model.Task{ID: 2}}
		setTaskResultVM(a, map[string]interface{}{}, 10, "vm-a")
		setTaskResultVM(b, map[string]interface{}{}, 20, "vm-b")

		if *a.Task.VMID != 10 || *b.Task.VMID != 20 {
			t.Errorf("VMID 互相污染：a=%d b=%d（期望 10 与 20）", *a.Task.VMID, *b.Task.VMID)
		}
		if a.Task.VMID == b.Task.VMID {
			t.Error("两个任务共用了同一个 VMID 指针")
		}
	})

	t.Run("结果无法序列化时保留原值且不 panic", func(t *testing.T) {
		ctx := &ExecContext{Task: &model.Task{ID: 1, Result: `{"old":true}`}}
		// chan 不可 JSON 序列化，json.Marshal 会报错
		setTaskResultVM(ctx, map[string]interface{}{"bad": make(chan int)}, 5, "vm-x")

		if ctx.Task.Result != `{"old":true}` {
			t.Errorf("序列化失败时不应改写 Result：实际 %q", ctx.Task.Result)
		}
		// VM 关联仍应写入（与 Result 无关）
		if ctx.Task.VMID == nil || *ctx.Task.VMID != 5 || ctx.Task.VMName != "vm-x" {
			t.Error("序列化失败不应影响 VM 关联字段的回填")
		}
	})
}

// TestReportProgress 覆盖进度上报的空值防御。
// 风险点：Report 由 manager 注入，单测或未来的直接调用路径可能没有它；
// 不判空就是 nil 函数调用 panic，而进度上报只是辅助信息，绝不该拖垮任务。
func TestReportProgress(t *testing.T) {
	t.Run("Report 为 nil 时静默忽略", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Report 为 nil 时不应 panic：%v", r)
			}
		}()
		reportProgress(&ExecContext{Task: &model.Task{}}, 50, "进行中")
	})

	t.Run("ctx 为 nil 时静默忽略", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("ctx 为 nil 时不应 panic：%v", r)
			}
		}()
		reportProgress(nil, 50, "进行中")
	})

	t.Run("Report 存在时按原参数调用", func(t *testing.T) {
		var gotPct int
		var gotMsg string
		ctx := &ExecContext{
			Task:   &model.Task{},
			Report: func(pct int, msg string) { gotPct, gotMsg = pct, msg },
		}
		reportProgress(ctx, 70, "磁盘清理完成")

		if gotPct != 70 || gotMsg != "磁盘清理完成" {
			t.Errorf("参数传递错误：期望 (70, %q)，实际 (%d, %q)", "磁盘清理完成", gotPct, gotMsg)
		}
	})
}
