// designer_apply_lifecycle.go：架构设计器落地——节点下线与重启。
// teardownNode 下线单个节点（VM 走 delete_vm 任务并等终态、容器栈 compose down、网络 undefine，
// 资源已不存在视为成功，幂等供收敛重入）；restartNode 重启单个节点（VM 运行中走 reboot，
// 异常态走 destroy+start 兜底；容器栈走 compose restart）。供全量落地与按差异收敛的
// 删除 / 重建（先删后建）/ 重启三类动作共用。
package handler

import (
	"fmt"
	"strconv"
	"time"

	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/dbx"
)

// teardownNode 下线单个节点：VM 走 delete_vm 任务（异步 + 等终态），容器栈 compose down，
// 网络 undefine。资源已不存在视为成功（幂等，供收敛重入）。
func (h *DesignerHandler) teardownNode(op dsgOp, st *dsgApplyState, userID *uint, username string) error {
	n := op.Node
	switch n.Kind {
	case "vm":
		var vm model.VM
		if err := h.DB.Where("name = ?", n.Name).First(&vm).Error; err != nil {
			st.Steps = append(st.Steps, "· VM "+n.Name+" 已不存在，跳过下线")
			return nil
		}
		vmID := vm.ID
		task, err := h.Tasks.Submit("delete_vm", "设计器下线 VM "+vm.Name, map[string]interface{}{"vm_id": vm.ID}, userID, username, vm.Name, &vmID)
		if err != nil {
			return fmt.Errorf("提交删除任务失败: %w", err)
		}
		st.Steps = append(st.Steps, "删除任务已提交（task="+strconv.FormatUint(uint64(task.ID), 10)+"），等待完成…")
		if _, err := h.waitTask(task.ID, 15*time.Minute); err != nil {
			return err
		}
		st.Steps = append(st.Steps, "✓ VM "+vm.Name+" 已删除")
	case "container":
		if h.Docker == nil {
			return fmt.Errorf("容器服务不可用")
		}
		if err := h.Docker.ComposeAction(n.Ref, "down"); err != nil {
			return err
		}
		st.Steps = append(st.Steps, "✓ 栈 "+n.Ref+" 已下线")
	case "net":
		if h.Virt == nil {
			return fmt.Errorf("虚拟化服务不可用")
		}
		if nets, err := h.Virt.ListNetworks(); err == nil {
			found := false
			for _, e := range nets {
				if e.Name == n.Name {
					found = true
					break
				}
			}
			if !found {
				st.Steps = append(st.Steps, "· 网络 "+n.Name+" 已不存在，跳过")
				return nil
			}
		}
		if err := h.Virt.DeleteNetwork(n.Name); err != nil {
			return err
		}
		st.Steps = append(st.Steps, "✓ 网络 "+n.Name+" 已删除")
	}
	return nil
}

// restartNode 重启单个节点：VM 运行中走 reboot，异常态（paused/error）走 destroy+start
// 兜底；容器栈走 compose restart。用于收敛里的 restarted 动作。
func (h *DesignerHandler) restartNode(op dsgOp, st *dsgApplyState) error {
	n := op.Node
	switch n.Kind {
	case "vm":
		var vm model.VM
		if err := h.DB.Where("name = ?", n.Name).First(&vm).Error; err != nil {
			st.Steps = append(st.Steps, "· VM "+n.Name+" 已不存在，跳过重启")
			return nil
		}
		if vm.Status == model.VMStatusRunning {
			if err := h.Virt.RebootDomain(vm.Name); err != nil {
				return err
			}
		} else {
			// paused/error 下 start 会失败，先硬停再起（恢复异常态）
			_ = h.Virt.DestroyDomain(vm.Name)
			if err := h.Virt.StartDomain(vm.Name); err != nil {
				return err
			}
		}
		// 状态回写失败留痕（AGENTS：DB 写不静默吞）——重启已由 libvirt 生效
		dbx.PersistBestEffort(h.DB, "designer 收敛重启 VM 回写状态", func() error {
			return h.DB.Model(&model.VM{}).Where("id = ?", vm.ID).Update("status", model.VMStatusRunning).Error
		})
		st.Steps = append(st.Steps, "✓ VM "+vm.Name+" 已重启")
	case "container":
		if h.Docker == nil {
			return fmt.Errorf("容器服务不可用")
		}
		if err := h.Docker.ComposeAction(n.Ref, "restart"); err != nil {
			return err
		}
		st.Steps = append(st.Steps, "✓ 栈 "+n.Ref+" 已重启")
	}
	return nil
}
