package monitor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/jiuzhao/vmops/model"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// openMonitorTestDB 打开内存库并迁移 vms 表（不碰真实 data/）。
func openMonitorTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	if err := db.AutoMigrate(&model.VM{}); err != nil {
		t.Fatalf("AutoMigrate 失败: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, cErr := db.DB(); cErr == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

// fileSDEntry 反序列化 file_sd 文件用的通用结构。
type fileSDEntry struct {
	Targets []string          `json:"targets"`
	Labels  map[string]string `json:"labels"`
}

// readFileSD 读取并解析落盘的 file_sd 文件。
func readFileSD(t *testing.T, path string) []fileSDEntry {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 file_sd 文件失败: %v", err)
	}
	var entries []fileSDEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatalf("file_sd 不是合法 JSON: %v body=%s", err, data)
	}
	return entries
}

// targetIPs 抽出文件里全部 targets（形如 `<ip>:9100`）。
func targetIPs(entries []fileSDEntry) []string {
	var out []string
	for _, e := range entries {
		out = append(out, e.Targets...)
	}
	return out
}

// TestWriteFileSDOnce 覆盖「查库 → 生成 → 原子落盘」主路径。
//
// 风险点：
//   - 落盘必须走「写 .tmp → rename」原子替换，成功后不得残留 `.tmp`（残留说明只写了一半）；
//   - 只挑 running 且 IP 非空的 VM，关机/暂停/无 IP 都不能进目标文件（否则 Prometheus
//     会去抓死端点并持续报 down）；
//   - 文件末尾补换行，且内容可被标准 json 反序列化。
func TestWriteFileSDOnce(t *testing.T) {
	// 注意：model.VM.UUID 带 uniqueIndex，测试里每台 VM 必须给唯一 UUID——
	// 否则空 UUID 会撞唯一约束，批量插入整条语句失败（静默 0 行）。
	db := openMonitorTestDB(t)
	db.Create(&[]model.VM{
		{Name: "run-ok", UUID: "u-run-ok", IP: "10.0.0.5", Status: model.VMStatusRunning},
		{Name: "run-noip", UUID: "u-run-noip", IP: "", Status: model.VMStatusRunning},
		{Name: "off", UUID: "u-off", IP: "10.0.0.6", Status: model.VMStatusShutOff},
		{Name: "paused", UUID: "u-paused", IP: "10.0.0.7", Status: model.VMStatusPaused},
	})

	dir := t.TempDir()
	path := filepath.Join(dir, "vm_targets.json")
	writeFileSDOnce(db, path)

	// 无残留临时文件
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("原子写后不应残留 .tmp 文件, stat err=%v", err)
	}
	// 末尾换行
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("文件未生成: %v", err)
	}
	if !strings.HasSuffix(string(raw), "\n") {
		t.Errorf("文件应以换行结尾, got %q", string(raw))
	}

	entries := readFileSD(t, path)
	ips := targetIPs(entries)
	if len(ips) != 1 || ips[0] != "10.0.0.5:9100" {
		t.Fatalf("目标文件内容不符: %v", ips)
	}
	if entries[0].Labels["vm_name"] != "run-ok" {
		t.Errorf("labels.vm_name=%q want run-ok", entries[0].Labels["vm_name"])
	}
}

// TestWriteFileSDOnceCreatesNestedDir 覆盖首次启用时目标目录尚不存在的场景。
// 风险点：deploy/file_sd 可能还没挂载出来，写入前必须先 MkdirAll，否则首轮就写失败。
func TestWriteFileSDOnceCreatesNestedDir(t *testing.T) {
	db := openMonitorTestDB(t)
	db.Create(&model.VM{Name: "a", UUID: "u-nested", IP: "10.0.0.9", Status: model.VMStatusRunning})

	// 多层不存在的子目录
	path := filepath.Join(t.TempDir(), "sub", "deep", "vm_targets.json")
	writeFileSDOnce(db, path)

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("目录未自动创建或文件未写出: %v", err)
	}
	if ips := targetIPs(readFileSD(t, path)); len(ips) != 1 || ips[0] != "10.0.0.9:9100" {
		t.Errorf("目标不符: %v", ips)
	}
}

// TestWriteFileSDOnceOverwrite 二次写入应完全覆盖旧内容（VM 状态变化后即时反映）。
// 风险点：rename 是替换而非追加，旧目标不能残留在文件里（残留会让已关机 VM 继续被抓）。
func TestWriteFileSDOnceOverwrite(t *testing.T) {
	db := openMonitorTestDB(t)
	vm1 := model.VM{Name: "old", UUID: "u-old", IP: "10.0.0.10", Status: model.VMStatusRunning}
	db.Create(&vm1)

	path := filepath.Join(t.TempDir(), "vm_targets.json")
	writeFileSDOnce(db, path)
	if ips := targetIPs(readFileSD(t, path)); len(ips) != 1 || ips[0] != "10.0.0.10:9100" {
		t.Fatalf("首轮目标不符: %v", ips)
	}

	// old 关机，新增 new 运行
	db.Model(&vm1).Update("status", model.VMStatusShutOff)
	db.Create(&model.VM{Name: "new", UUID: "u-new", IP: "10.0.0.11", Status: model.VMStatusRunning})
	writeFileSDOnce(db, path)

	ips := targetIPs(readFileSD(t, path))
	if len(ips) != 1 || ips[0] != "10.0.0.11:9100" {
		t.Errorf("覆盖后应为新目标: %v", ips)
	}
}

// TestWriteFileSDOnceEmptyWritesArray 无有效目标时写出的必须是 `[]` 而非 `null`。
// 风险点：file_sd 文件若为 `null`/空，Prometheus 的 watcher 会报解析错误。
func TestWriteFileSDOnceEmptyWritesArray(t *testing.T) {
	db := openMonitorTestDB(t)
	db.Create(&model.VM{Name: "off", UUID: "u-empty-off", IP: "10.0.0.1", Status: model.VMStatusShutOff})

	path := filepath.Join(t.TempDir(), "vm_targets.json")
	writeFileSDOnce(db, path)

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("文件未生成: %v", err)
	}
	if strings.TrimSpace(string(raw)) != "[]" {
		t.Errorf("无目标时应写出 []，实际 %q", string(raw))
	}
}

// TestWriteFileSDOnceExcludesSoftDeleted 软删除的 VM 不应再进抓取目标。
// 风险点：GORM 默认作用域会排除 gorm.DeletedAt 非空行，此处守护查询条件别把它绕过。
func TestWriteFileSDOnceExcludesSoftDeleted(t *testing.T) {
	db := openMonitorTestDB(t)
	vm := model.VM{Name: "gone", UUID: "u-gone", IP: "10.0.0.20", Status: model.VMStatusRunning}
	db.Create(&vm)

	path := filepath.Join(t.TempDir(), "vm_targets.json")
	writeFileSDOnce(db, path)
	if ips := targetIPs(readFileSD(t, path)); len(ips) != 1 {
		t.Fatalf("删除前应有 1 个目标: %v", ips)
	}

	if err := db.Delete(&vm).Error; err != nil {
		t.Fatalf("软删除失败: %v", err)
	}
	writeFileSDOnce(db, path)
	if ips := targetIPs(readFileSD(t, path)); len(ips) != 0 {
		t.Errorf("软删除后不应有目标: %v", ips)
	}
}

// TestStartFileSDWriterNoopWhenDisabled 未配置路径或 db 为 nil 时必须直接返回、不起协程、不落盘。
// 风险点：FILE_SD_PATH 未配置时功能关闭；nil db 是防御分支。二者都不得写文件。
func TestStartFileSDWriterNoopWhenDisabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vm_targets.json")

	// db 为 nil：即使 path 合法也不启动
	StartFileSDWriter(nil, path, time.Hour)
	// path 为空：功能未启用
	StartFileSDWriter(openMonitorTestDB(t), "", time.Hour)

	// 给潜在协程一点时间（若误启动会很快写盘），再断言未生成文件
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("禁用状态下不应写出文件, stat err=%v", err)
	}
}

// TestStartFileSDWriterWritesOnStart 覆盖启动即先写一轮（无需等第一个 tick）。
//
// 说明：StartFileSDWriter 无停止接口，本用例以 1h 的 interval 启动，使 ticker 在
// 测试期间不会触发第二次；遗留的阻塞协程随测试进程退出，不影响断言。
func TestStartFileSDWriterWritesOnStart(t *testing.T) {
	db := openMonitorTestDB(t)
	db.Create(&model.VM{Name: "boot", UUID: "u-boot", IP: "10.0.0.30", Status: model.VMStatusRunning})

	path := filepath.Join(t.TempDir(), "vm_targets.json")
	StartFileSDWriter(db, path, time.Hour)

	// 轮询等待启动轮落盘（最多 2s）
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("启动轮未写出目标文件: %v", err)
	}
	if ips := targetIPs(readFileSD(t, path)); len(ips) != 1 || ips[0] != "10.0.0.30:9100" {
		t.Errorf("启动轮目标不符: %v", ips)
	}
}
