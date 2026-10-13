package metrics

import (
	"os"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/virt"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// openMetricsTestDB 打开内存库并迁移所需表。
func openMetricsTestDB(t *testing.T, models ...interface{}) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	if err := db.AutoMigrate(models...); err != nil {
		t.Fatalf("AutoMigrate 失败: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, cErr := db.DB(); cErr == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

// gatherFamilyNames 返回注册表当前导出（Gather）到的全部指标族名。
func gatherFamilyNames(t *testing.T) map[string]bool {
	t.Helper()
	mfs, err := Registry.Gather()
	if err != nil {
		t.Fatalf("Gather 失败: %v", err)
	}
	names := make(map[string]bool, len(mfs))
	for _, mf := range mfs {
		names[mf.GetName()] = true
	}
	return names
}

// gaugeValue 在注册表里按指标名 + 标签集查一条序列的值；找不到返回 ok=false。
func gaugeValue(t *testing.T, name string, match map[string]string) (float64, bool) {
	t.Helper()
	mfs, err := Registry.Gather()
	if err != nil {
		t.Fatalf("Gather 失败: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			labels := map[string]string{}
			for _, lp := range m.GetLabel() {
				labels[lp.GetName()] = lp.GetValue()
			}
			hit := true
			for k, v := range match {
				if labels[k] != v {
					hit = false
					break
				}
			}
			if hit {
				return m.GetGauge().GetValue(), true
			}
		}
	}
	return 0, false
}

// TestRegisteredMetricNames 锁死全部指标名与标签名。
//
// 风险点：这些名字是 Grafana 面板与 PromQL 查询（见源码各指标上方的注释）的硬依赖，
// 改名不会让编译或运行报错——只会让面板静默变成「No data」。带标签的 GaugeVec 在
// 没有任何序列时 Gather 不出现在输出里，因此先给每条 vec 塞一个探针序列再断言存在。
func TestRegisteredMetricNames(t *testing.T) {
	const probe = "nameguard-probe"
	// 给每条带标签的 vec 打一个探针序列，使其进入 Gather 输出。
	vmCPU.WithLabelValues(probe).Set(1)
	vmMemUsed.WithLabelValues(probe).Set(1)
	vmMemTotal.WithLabelValues(probe).Set(1)
	vmDiskRead.WithLabelValues(probe).Set(1)
	vmDiskWrite.WithLabelValues(probe).Set(1)
	vmNetRx.WithLabelValues(probe).Set(1)
	vmNetTx.WithLabelValues(probe).Set(1)
	vmRunning.WithLabelValues(probe).Set(1)
	poolCapacity.WithLabelValues(probe).Set(1)
	poolAvailable.WithLabelValues(probe).Set(1)
	t.Cleanup(func() {
		vmCPU.DeleteLabelValues(probe)
		vmMemUsed.DeleteLabelValues(probe)
		vmMemTotal.DeleteLabelValues(probe)
		vmDiskRead.DeleteLabelValues(probe)
		vmDiskWrite.DeleteLabelValues(probe)
		vmNetRx.DeleteLabelValues(probe)
		vmNetTx.DeleteLabelValues(probe)
		vmRunning.DeleteLabelValues(probe)
		poolCapacity.DeleteLabelValues(probe)
		poolAvailable.DeleteLabelValues(probe)
	})

	names := gatherFamilyNames(t)
	want := []string{
		"vmops_vm_cpu_percent",
		"vmops_vm_mem_used_kib",
		"vmops_vm_mem_total_kib",
		"vmops_vm_disk_read_bps",
		"vmops_vm_disk_write_bps",
		"vmops_vm_net_rx_bps",
		"vmops_vm_net_tx_bps",
		"vmops_vm_running",
		"vmops_host_cpu_percent",
		"vmops_host_mem_used_kib",
		"vmops_host_mem_total_kib",
		"vmops_pool_capacity_bytes",
		"vmops_pool_available_bytes",
		"vmops_tasks_pending",
		"vmops_tasks_running",
	}
	for _, n := range want {
		if !names[n] {
			t.Errorf("注册表缺少指标 %q（改名会让 Grafana 面板静默变 No data）", n)
		}
	}
	// 运行时指标也必须注册（Go/进程），否则进程级面板全空。
	for _, n := range []string{"go_goroutines", "process_cpu_seconds_total"} {
		if !names[n] {
			t.Errorf("注册表缺少运行时指标 %q", n)
		}
	}

	// 标签名断言：VM 用 vm、池用 pool，写错则所有带标签查询失配。
	if v, ok := gaugeValue(t, "vmops_vm_running", map[string]string{"vm": probe}); !ok {
		t.Error("vmops_vm_running 的标签名不是 vm")
	} else if v != 1 {
		t.Errorf("探针序列值期望 1，实际 %v", v)
	}
	if _, ok := gaugeValue(t, "vmops_pool_capacity_bytes", map[string]string{"pool": probe}); !ok {
		t.Error("vmops_pool_capacity_bytes 的标签名不是 pool")
	}
}

// TestUpdateVMsNonRunningAndStaleCleanup 覆盖：
//  1. 非运行态 VM 导出 running=0；
//  2. VM 从库中消失后，其 stale 序列被清理。
//
// 风险点：knownVMs 与 seen 的差集清理是「VM 被删后 /metrics 还挂着它的序列」的唯一
// 解药。若这段差集逻辑被删，被删 VM 的序列会永久驻留（label 基数只增不减），
// 最终把 Prometheus 系列数撑爆。这里用「先建后删再采集」精确复现该场景。
func TestUpdateVMsNonRunningAndStaleCleanup(t *testing.T) {
	db := openMetricsTestDB(t, &model.VM{})
	if err := db.Create(&model.VM{UUID: "uu-cleanup", HostID: 1, Name: "stale-vm", Status: model.VMStatusShutOff}).Error; err != nil {
		t.Fatalf("建 VM 失败: %v", err)
	}
	c := &Collector{DB: db} // Virt 留空：只要库里没有 running VM 就不会被调用

	c.updateVMs()
	if v, ok := gaugeValue(t, "vmops_vm_running", map[string]string{"vm": "stale-vm"}); !ok || v != 0 {
		t.Fatalf("关机 VM 应导出 running=0 序列，实际 ok=%v v=%v", ok, v)
	}

	// 软删除该 VM，再采集：序列应被清理。
	if err := db.Where("name = ?", "stale-vm").Delete(&model.VM{}).Error; err != nil {
		t.Fatalf("删 VM 失败: %v", err)
	}
	c.updateVMs()
	if _, ok := gaugeValue(t, "vmops_vm_running", map[string]string{"vm": "stale-vm"}); ok {
		t.Error("已删除 VM 的 stale 序列未被清理（标签基数只增不减）")
	}
}

// TestUpdateVMsTransientFailureKeepsSeries 锁死「运行中 VM 抓取瞬时失败要保留上次序列」。
//
// 风险点：库里标记 running、但这一拍 libvirt 抓取失败（瞬时抖动 / 域查找失败）时，若不把该 VM
// 记入 seen，差集清理会删掉它的整条 vmops_vm_running 序列——PromQL 上该 VM「消失」，甚至误触发
// VMRunningDrop 告警。契约（文件头 + Update 注释）是「失败项跳过保留旧值」。
func TestUpdateVMsTransientFailureKeepsSeries(t *testing.T) {
	db := openMetricsTestDB(t, &model.VM{})
	// 库里 running、但 libvirt 里并不存在的域 → GetDomainStats 必失败（本机 libvirt 可用与否都成立）
	if err := db.Create(&model.VM{UUID: "uu-ghost", HostID: 1, Name: "ghost-vm", Status: model.VMStatusRunning}).Error; err != nil {
		t.Fatalf("建 VM 失败: %v", err)
	}
	vmRunning.WithLabelValues("ghost-vm").Set(1) // 模拟上一拍成功采集过的序列
	t.Cleanup(func() { vmRunning.DeleteLabelValues("ghost-vm") })

	c := &Collector{DB: db, Virt: virt.New()}
	c.updateVMs()

	if v, ok := gaugeValue(t, "vmops_vm_running", map[string]string{"vm": "ghost-vm"}); !ok || v != 1 {
		t.Fatalf("运行中 VM 抓取失败后序列被误删（应保留上次值 1）：ok=%v v=%v", ok, v)
	}
}

// TestUpdateTasksCounts 覆盖任务队列深度两个仪表的计数口径。
//
// 风险点：pending/running 只统计对应状态的任务，success/failed 不能算进去。
// 口径写错（例如漏了 status 过滤）会让「队列积压」面板常年虚高，掩盖真实阻塞。
func TestUpdateTasksCounts(t *testing.T) {
	db := openMetricsTestDB(t, &model.Task{})
	for _, st := range []string{model.TaskStatusPending, model.TaskStatusPending, model.TaskStatusRunning, "success", "failed"} {
		if err := db.Create(&model.Task{Type: "t", Title: "t", Status: st}).Error; err != nil {
			t.Fatalf("建任务失败: %v", err)
		}
	}
	c := &Collector{DB: db}
	c.updateTasks()

	if v, ok := gaugeValue(t, "vmops_tasks_pending", nil); !ok || v != 2 {
		t.Errorf("vmops_tasks_pending 期望 2，实际 ok=%v v=%v", ok, v)
	}
	if v, ok := gaugeValue(t, "vmops_tasks_running", nil); !ok || v != 1 {
		t.Errorf("vmops_tasks_running 期望 1，实际 ok=%v v=%v", ok, v)
	}
}

// TestReadCPUStatSmoke 对 /proc/stat 聚合行解析做最小化烟雾校验。
//
// 风险点：readCPUStat 硬编码了字段下标（vals[3]=idle、vals[4]=iowait）与 "cpu " 前缀。
// 下标错位（比如把 user 当 idle）会让宿主机 CPU 使用率整体偏移而不报错。这里断言
// idle ≤ total（idle/iowait 本就是 total 的一部分）以及 total>0，能捕捉到下标/前缀
// 写错导致的解析失配。非 Linux 环境（无 /proc/stat）跳过。
func TestReadCPUStatSmoke(t *testing.T) {
	if _, err := os.Stat("/proc/stat"); err != nil {
		t.Skip("/proc/stat 不存在，跳过（非 Linux 环境）")
	}
	idle, total, err := readCPUStat()
	if err != nil {
		t.Fatalf("readCPUStat 失败: %v", err)
	}
	if total == 0 {
		t.Fatal("total 为 0：cpu 聚合行解析可能失配")
	}
	if idle > total {
		t.Fatalf("idle(%d) > total(%d)：字段下标可能错位", idle, total)
	}
}

// TestReadMeminfoSmoke 校验 /proc/meminfo 的 MemTotal/MemAvailable 解析。
//
// 风险点：宿主机内存面板分母（total）与分子（used=total-avail）都来自这里。
// 若字段名匹配写错（如漏掉冒号），total 会变成 0，面板显示 NaN 或恒定 0。
func TestReadMeminfoSmoke(t *testing.T) {
	if _, err := os.Stat("/proc/meminfo"); err != nil {
		t.Skip("/proc/meminfo 不存在，跳过（非 Linux 环境）")
	}
	total, avail := readMeminfo()
	if total == 0 {
		t.Fatal("MemTotal 解析为 0：字段名匹配可能失效")
	}
	if avail > total {
		t.Fatalf("MemAvailable(%d) > MemTotal(%d)，解析异常", avail, total)
	}
}
