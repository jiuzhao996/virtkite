package monitor

import (
	"encoding/json"
	"testing"

	"github.com/jiuzhao/vmops/model"
)

// TestGenerateFileSD 过滤规则：只保留 running 且 IP 非空的 VM。
func TestGenerateFileSD(t *testing.T) {
	vms := []model.VM{
		{ID: 1, Name: "web-1", IP: "192.168.1.10", Status: model.VMStatusRunning},
		{ID: 2, Name: "web-2", IP: "192.168.1.11", Status: model.VMStatusShutOff}, // 关机：过滤
		{ID: 3, Name: "web-3", IP: "", Status: model.VMStatusRunning},             // 无 IP：过滤
		{ID: 4, Name: "web-4", IP: "192.168.1.12", Status: model.VMStatusPaused},  // 暂停：过滤
		{ID: 5, Name: "db-1", IP: "192.168.1.10", Status: model.VMStatusRunning},  // 与 web-1 同 IP：去重
		{ID: 6, Name: "app-1", IP: "192.168.1.11", Status: model.VMStatusRunning}, // 独立 IP：第二个目标
	}

	entries := GenerateFileSD(vms)
	if len(entries) != 2 {
		t.Fatalf("期望 2 个去重后目标，实际 %d: %v", len(entries), entries)
	}

	// 首个目标 = 192.168.1.10（web-1 与 db-1 合并）
	first := entries[0]
	targets, ok := first["targets"].([]string)
	if !ok || len(targets) != 1 || targets[0] != "192.168.1.10:9100" {
		t.Fatalf("首个目标 targets 错误: %v", first["targets"])
	}
	labels := first["labels"].(map[string]string)
	if labels["job"] != "vm-node" {
		t.Errorf("labels.job = %q, want vm-node", labels["job"])
	}
	if labels["vm_name"] != "web-1,db-1" {
		t.Errorf("同 IP 去重后 vm_name = %q, want web-1,db-1", labels["vm_name"])
	}
	if labels["vm_id"] != "1,5" {
		t.Errorf("同 IP 去重后 vm_id = %q, want 1,5", labels["vm_id"])
	}

	// 次个目标 = 192.168.1.11（单台，字段为标量）
	second := entries[1]
	targets2 := second["targets"].([]string)
	if targets2[0] != "192.168.1.11:9100" {
		t.Fatalf("次个目标 targets 错误: %v", targets2)
	}
	labels2 := second["labels"].(map[string]string)
	if labels2["vm_name"] != "app-1" || labels2["vm_id"] != "6" {
		t.Errorf("labels2 = %v, want vm_name=app-1 vm_id=6", labels2)
	}
}

// TestGenerateFileSDEmpty 空输入/全被过滤时输出空数组（JSON 序列化为 [] 而非 null，
// Prometheus file_sd 对 null 文件会报解析错误）。
func TestGenerateFileSDEmpty(t *testing.T) {
	for name, vms := range map[string][]model.VM{
		"空列表":     {},
		"全为关机无IP": {{ID: 1, Name: "a", IP: "", Status: model.VMStatusShutOff}},
	} {
		entries := GenerateFileSD(vms)
		if entries == nil {
			t.Errorf("%s: 返回 nil 切片（JSON 会序列化为 null）", name)
			continue
		}
		if len(entries) != 0 {
			t.Errorf("%s: 期望 0 条目，实际 %d", name, len(entries))
		}
		data, err := json.Marshal(entries)
		if err != nil || string(data) != "[]" {
			t.Errorf("%s: JSON 序列化 = %q err=%v, want []", name, data, err)
		}
	}
}

// TestGenerateFileSDOutputShape 输出结构符合 Prometheus file_sd 约定（可被 json.Unmarshal 进通用结构）。
func TestGenerateFileSDOutputShape(t *testing.T) {
	vms := []model.VM{
		{ID: 7, Name: "app-1", IP: "10.0.0.5", Status: model.VMStatusRunning},
	}
	data, err := json.Marshal(GenerateFileSD(vms))
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	var parsed []struct {
		Targets []string          `json:"targets"`
		Labels  map[string]string `json:"labels"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("file_sd 结构反序列化失败: %v body=%s", err, data)
	}
	if len(parsed) != 1 || len(parsed[0].Targets) != 1 || parsed[0].Targets[0] != "10.0.0.5:9100" {
		t.Fatalf("targets 不符: %+v", parsed)
	}
	for _, key := range []string{"job", "vm_name", "vm_id"} {
		if _, ok := parsed[0].Labels[key]; !ok {
			t.Errorf("labels 缺少 %q: %v", key, parsed[0].Labels)
		}
	}
}

// TestGenerateFileSDDoesNotIncludePlatformExporter 平台自身（DB 里无对应行）天然不会进 file_sd：
// 约定由 prometheus.yml 静态抓取，这里守护 GenerateFileSD 只吃传入的 VM 列表、不偷加平台目标。
func TestGenerateFileSDDoesNotIncludePlatformExporter(t *testing.T) {
	entries := GenerateFileSD(nil)
	if len(entries) != 0 {
		t.Fatalf("空输入不应产生目标: %v", entries)
	}
	// 即使 VM 列表全量传入，:8080 平台端口也不应出现在任何 target 里
	vms := []model.VM{{ID: 1, Name: "x", IP: "127.0.0.1", Status: model.VMStatusRunning}}
	for _, e := range GenerateFileSD(vms) {
		for _, tgt := range e["targets"].([]string) {
			if tgt == "127.0.0.1:8080" {
				t.Errorf("平台自身 exporter 不应进 file_sd: %v", tgt)
			}
		}
	}
}
