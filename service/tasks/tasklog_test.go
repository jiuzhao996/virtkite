package tasks

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/jiuzhao/vmops/model"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// openTaskTestDB 打开内存库并迁移 tasks 表（本文件各用例共用）。
func openTaskTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	if err := db.AutoMigrate(&model.Task{}); err != nil {
		t.Fatalf("AutoMigrate 失败: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, cErr := db.DB(); cErr == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

// TestParseLogEntries 结构化 vs 纯文本的判定依据：只有 JSON 数组才算结构化。
func TestParseLogEntries(t *testing.T) {
	t.Run("JSON 数组解析成功且保留字段", func(t *testing.T) {
		raw := `[{"seq":1,"ts":123,"kind":"stdout","text":"hello"},{"seq":2,"ts":124,"kind":"info","text":"done","hidden":true}]`
		entries, ok := ParseLogEntries(raw)
		if !ok || len(entries) != 2 {
			t.Fatalf("期望解析出 2 条，实际 ok=%v len=%d", ok, len(entries))
		}
		if entries[0].Seq != 1 || entries[0].Kind != "stdout" || entries[0].Text != "hello" {
			t.Errorf("第 1 条字段错误: %+v", entries[0])
		}
		if !entries[1].Hidden {
			t.Errorf("第 2 条 hidden 应为 true: %+v", entries[1])
		}
	})

	t.Run("空串判定为非结构化", func(t *testing.T) {
		if _, ok := ParseLogEntries(""); ok {
			t.Error("空串不应判定为结构化")
		}
	})

	t.Run("纯文本判定为非结构化（老任务向后兼容）", func(t *testing.T) {
		if _, ok := ParseLogEntries("PLAY [all] ****\nok: [web-01]"); ok {
			t.Error("纯文本不应判定为结构化")
		}
	})

	t.Run("业务 JSON 对象判定为非结构化", func(t *testing.T) {
		if _, ok := ParseLogEntries(`{"vm_id":3,"vm":"web-01"}`); ok {
			t.Error("JSON 对象（业务结果）不应判定为结构化日志数组")
		}
	})
}

// TestLogRedactorRedactsSecrets 落库前脱敏：cloud-init 密码等已知明文替换为 REDACTED。
func TestLogRedactorRedactsSecrets(t *testing.T) {
	db := openTaskTestDB(t)
	payload, _ := json.Marshal(map[string]interface{}{
		"cloud_init": map[string]interface{}{"password": "S3cr3tPassw0rd"},
		"disks": []interface{}{
			map[string]interface{}{"cloud_init": map[string]interface{}{"password": "DiskPassw0rd"}},
		},
	})
	task := &model.Task{Type: "create_vm", Status: statusRunning, Payload: string(payload)}
	if err := db.Create(task).Error; err != nil {
		t.Fatalf("建任务失败: %v", err)
	}

	r := newLogRedactor(db, task.ID, "")
	got := r.redact("初始化用户，口令=S3cr3tPassw0rd 请勿外泄")
	if strings.Contains(got, "S3cr3tPassw0rd") {
		t.Errorf("cloud-init 口令未被脱敏: %q", got)
	}
	if !strings.Contains(got, taskLogRedacted) {
		t.Errorf("脱敏结果应含 %q: %q", taskLogRedacted, got)
	}
	// 多盘密码同样收集
	if got2 := r.redact("disk pw DiskPassw0rd"); strings.Contains(got2, "DiskPassw0rd") {
		t.Errorf("多盘 cloud-init 口令未被脱敏: %q", got2)
	}
	// 无关文本原样返回
	if got3 := r.redact("这是一条普通日志"); got3 != "这是一条普通日志" {
		t.Errorf("无关文本不应被改动: %q", got3)
	}
}

// TestLogRedactorSkipsShortSecrets 过短的「口令」不参与替换，避免把正常日志打成 REDACTED。
func TestLogRedactorSkipsShortSecrets(t *testing.T) {
	r := &logRedactor{}
	r.add("ab") // 短于 taskLogMinSecretLen
	if got := r.redact("about abcdef"); got != "about abcdef" {
		t.Errorf("短口令不应触发替换: %q", got)
	}
}

// TestTaskLoggerSeqAndSubscribe 日志器：seq 单调递增、订阅者收到实时增量与 backlog。
func TestTaskLoggerSeqAndSubscribe(t *testing.T) {
	db := openTaskTestDB(t)
	task := &model.Task{Type: "ansible_run", Status: statusRunning}
	if err := db.Create(task).Error; err != nil {
		t.Fatalf("建任务失败: %v", err)
	}

	lg := newTaskLogger(db, task.ID, "", nil)
	defer lg.Close()

	lg.AppendLine("line-1")
	lg.Info("info-1")
	lg.Debug("debug-1")

	// 订阅者应拿到 seq>0 的三条 backlog
	backlog, ch, unsub := lg.SubscribeSince(0)
	defer unsub()
	if len(backlog) != 3 {
		t.Fatalf("期望 backlog 3 条，实际 %d", len(backlog))
	}
	if backlog[0].Seq != 1 || backlog[1].Seq != 2 || backlog[2].Seq != 3 {
		t.Errorf("seq 不单调: %+v", backlog)
	}
	if !backlog[2].Hidden {
		t.Errorf("Debug 条目 hidden 应为 true: %+v", backlog[2])
	}

	// 订阅后新增的条目应实时推送
	lg.AppendLine("line-4")
	select {
	case e := <-ch:
		if e.Seq != 4 || e.Text != "line-4" {
			t.Errorf("实时增量错误: %+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("订阅者未收到实时增量")
	}

	// SubscribeSince(2) 只回 seq>2 的条目
	backlog2, _, unsub2 := lg.SubscribeSince(2)
	defer unsub2()
	if len(backlog2) != 2 || backlog2[0].Seq != 3 {
		t.Errorf("since 过滤错误: %+v", backlog2)
	}
}

// TestTaskLoggerCloseFlushesAndClosesSubscribers Close 必须落库并关闭订阅通道（唤醒 WS 转发循环）。
func TestTaskLoggerCloseFlushesAndClosesSubscribers(t *testing.T) {
	db := openTaskTestDB(t)
	task := &model.Task{Type: "image_build", Status: statusRunning}
	if err := db.Create(task).Error; err != nil {
		t.Fatalf("建任务失败: %v", err)
	}

	lg := newTaskLogger(db, task.ID, "", nil)
	lg.Info("构建开始")
	lg.AppendLine("step 1")

	_, ch, unsub := lg.SubscribeSince(0)
	defer unsub()

	lg.Close()

	// 订阅通道应被关闭
	select {
	case _, ok := <-ch:
		if ok {
			// 通道里可能还有缓冲的历史帧，继续读到关闭
			for range ch {
			}
		}
	case <-time.After(time.Second):
		t.Fatal("Close 未关闭订阅通道，WS 转发循环会永久阻塞")
	}

	// 关闭后落库应包含两条
	var got model.Task
	if err := db.First(&got, task.ID).Error; err != nil {
		t.Fatalf("回读任务失败: %v", err)
	}
	entries, ok := ParseLogEntries(got.LogEntries)
	if !ok || len(entries) != 2 {
		t.Fatalf("Close 未落库日志: ok=%v entries=%+v", ok, entries)
	}
	if entries[0].Text != "构建开始" {
		t.Errorf("落库内容错误: %+v", entries[0])
	}

	// Close 幂等
	lg.Close()
}

// TestTaskLoggerCloseDropsLateAppends Close 之后的 Append 不应 panic，也不应写库。
func TestTaskLoggerCloseDropsLateAppends(t *testing.T) {
	db := openTaskTestDB(t)
	task := &model.Task{Type: "image_build", Status: statusRunning}
	if err := db.Create(task).Error; err != nil {
		t.Fatalf("建任务失败: %v", err)
	}
	lg := newTaskLogger(db, task.ID, "", nil)
	lg.Close()
	lg.Info("这条应被丢弃") // 不应 panic

	var got model.Task
	_ = db.First(&got, task.ID).Error
	if entries, ok := ParseLogEntries(got.LogEntries); ok && len(entries) > 0 {
		t.Errorf("Close 后的追加不应落库: %+v", entries)
	}
}

// TestIsNoiseLogLine 进度噪音行折叠启发式。
func TestIsNoiseLogLine(t *testing.T) {
	if !isNoiseLogLine("#3 extracting sha256:abc") {
		t.Error("extracting 行应被判定为噪音")
	}
	if isNoiseLogLine("error: build failed") {
		t.Error("错误行不应被判定为噪音")
	}
}
