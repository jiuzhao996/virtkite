// Package tasks 内放后台任务执行器（executor）注册逻辑。
//
// VM 异步任务 executor 按职责拆分在 4 个文件（对应 virsh 耗时操作后台化）：
// vm_create.go（create_vm）/ vm_delete.go（delete_vm + cleanup_volumes）/
// vm_clone.go（clone_vm + clone_image_vm）/ 本文件（stop_vm + 注册表 + 共享辅助）。
// 业务逻辑分别从 handler/vm.go（CreateVM/DeleteVM/CloneVM/StopVM）与
// handler/image.go（CloneVM）搬运而来，h.DB/h.Virt 改为 ctx.DB/ctx.Virt。
//
// 约束：executor 运行在 worker goroutine，禁止引用 gin/handler；
// 所有错误用 fmt.Errorf("中文描述: %w", err) 保留错误链。
package tasks

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/virt"
)

// DefaultStoragePoolResolver 返回未指定存储池时使用的默认池名（与 model.VM.StoragePool 的 gorm 默认值一致）。
// main 启动时接到系统设置（service/setting），未接线时退回内置默认值 vmops。
var DefaultStoragePoolResolver = func() string { return "vmops" }

// taskVMNameRegex 虚拟机名称只允许字母、数字、下划线和连字符（copy 自 handler/vm.go）。
var taskVMNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// validateVMName 校验虚拟机名称合法性（copy 自 handler/vm.go，tasks 包内自实现）。
func validateVMName(name string) bool {
	return taskVMNameRegex.MatchString(name)
}

// strParam 从 payload 安全取字符串（类型断言带 ok，缺失/类型不符返回 false）。
func strParam(payload map[string]interface{}, key string) (string, bool) {
	if payload == nil {
		return "", false
	}
	v, ok := payload[key]
	if !ok || v == nil {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	return s, true
}

// floatParam 从 payload 安全取数值（兼容 JSON 反序列化的 float64 与 Submit 直传的整型）。
func floatParam(payload map[string]interface{}, key string) (float64, bool) {
	if payload == nil {
		return 0, false
	}
	v, ok := payload[key]
	if !ok || v == nil {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}

// intParam 从 payload 安全取整数。
func intParam(payload map[string]interface{}, key string) (int, bool) {
	f, ok := floatParam(payload, key)
	if !ok {
		return 0, false
	}
	return int(f), true
}

// firstTaskHost 返回平台登记的首台宿主机（默认纳管目标，copy 自 handler firstHost）。
func firstTaskHost(ctx *ExecContext) (*model.Host, error) {
	var host model.Host
	if err := ctx.DB.Order("id ASC").First(&host).Error; err != nil {
		return nil, err
	}
	return &host, nil
}

// taskSeedDir 返回 cloud-init seed 落盘目录（带默认值，防御 GlobalConfig 未初始化）。
func taskSeedDir() string {
	if config.GlobalConfig != nil && config.GlobalConfig.SeedDir != "" {
		return config.GlobalConfig.SeedDir
	}
	return "/home/jiuzhao/vmops/data/seed"
}

// checkExecContext 校验 executor 上下文（worker 内仅可访问 ctx.Task/DB/Virt）。
func checkExecContext(ctx *ExecContext) error {
	if ctx == nil {
		return errors.New("任务上下文为空")
	}
	if ctx.DB == nil {
		return errors.New("任务数据库连接为空")
	}
	if ctx.Virt == nil {
		return errors.New("任务虚拟化服务为空")
	}
	if ctx.Task == nil {
		return errors.New("任务记录为空")
	}
	if ctx.Payload == nil {
		return errors.New("缺少任务参数")
	}
	return nil
}

// reportProgress 上报进度（Report 为空时忽略，防御 manager 未注入）。
func reportProgress(ctx *ExecContext, pct int, msg string) {
	if ctx != nil && ctx.Report != nil {
		ctx.Report(pct, msg)
	}
}

// setTaskResultVM 回填任务结果与 VM 关联（Result 为 JSON 字符串）。
func setTaskResultVM(ctx *ExecContext, result map[string]interface{}, vmID uint, vmName string) {
	if b, err := json.Marshal(result); err == nil {
		ctx.Task.Result = string(b)
	}
	id := vmID
	ctx.Task.VMID = &id
	ctx.Task.VMName = vmName
}

// RegisterVMTasks 注册 6 个 VM 任务 executor（契约 service/tasks/vm_tasks.go，T2 产出）。
func RegisterVMTasks(m *Manager) {
	if m == nil {
		return
	}
	m.Register("create_vm", execCreateVM)
	m.Register("delete_vm", execDeleteVM)
	m.Register("clone_vm", execCloneVM)
	m.Register("clone_image_vm", execCloneImageVM)
	m.Register("stop_vm", execStopVM)
	m.Register("cleanup_volumes", execCleanupVolumes)
}

// execStopVM 停止虚拟机：先优雅关机（对应 virsh shutdown），轮询等待其真正关闭，
// 超时后强制断电（对应 virsh destroy）。
// payload：{vm_id*}。
func execStopVM(ctx *ExecContext) error {
	if err := checkExecContext(ctx); err != nil {
		return err
	}

	vmID, ok := intParam(ctx.Payload, "vm_id")
	if !ok || vmID <= 0 {
		return errors.New("缺少虚拟机 ID 参数")
	}

	var vm model.VM
	if err := ctx.DB.First(&vm, uint(vmID)).Error; err != nil {
		return fmt.Errorf("虚拟机不存在: %w", err)
	}

	// 先优雅关机，轮询等待其真正关闭（最多 ~15s），超时仍运行则强制。
	shutdownErr := ctx.Virt.ShutdownDomain(vm.Name)
	if shutdownErr != nil {
		// 优雅关机调用失败（如域不存在），直接强制断电。
		if ferr := ctx.Virt.DestroyDomain(vm.Name); ferr != nil {
			return fmt.Errorf("强制停止虚拟机失败: %w", ferr)
		}
	} else {
		shutOff := false
		for i := 0; i < 15; i++ {
			time.Sleep(1 * time.Second)
			state, err := ctx.Virt.GetDomainState(vm.Name)
			if err == nil && state == virt.StatusShutOff {
				shutOff = true
				break
			}
			reportProgress(ctx, 10+i*5, "等待虚拟机关闭")
		}
		if !shutOff {
			if err := ctx.Virt.DestroyDomain(vm.Name); err != nil {
				return fmt.Errorf("强制停止虚拟机失败: %w", err)
			}
		}
	}

	// 更新状态。
	if err := ctx.DB.Model(&vm).Update("status", model.VMStatusShutOff).Error; err != nil {
		return fmt.Errorf("更新虚拟机状态失败: %w", err)
	}

	setTaskResultVM(ctx, map[string]interface{}{"vm": vm.Name}, vm.ID, vm.Name)
	return nil
}
