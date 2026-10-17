package handler

// crons_validate.go 计划任务的校验与参数归一化：请求字段合并、链式引用与目标存在性校验、
// 动作与 params 白名单校验。返回的错误均为固定中文文案，调用方可直接 Fail 回显。

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/ansible"
	"github.com/jiuzhao/vmops/service/cron"
)

// applyCronReq 把请求中出现的字段合并到任务上，并对合并后的完整任务做校验与参数归一化。
// 返回的 error 均为固定中文文案（可直接回显），不含内部细节。
//
// 请求字段全部用指针：nil=「本次没改」，非 nil=「显式改成该值」。这是 PATCH 语义，
// 让编辑页可以只提交用户实际改动的字段；因此合并后再整体校验，而不是逐字段校验
// （例如只改名字时，存量 cron 表达式也必须仍然合法）。
func applyCronReq(st *model.ScheduledTask, req cronTaskReq) error {
	if req.Name != nil {
		st.Name = strings.TrimSpace(*req.Name)
	}
	if req.CronExpr != nil {
		st.CronExpr = strings.TrimSpace(*req.CronExpr)
	}
	if req.Action != nil {
		st.Action = strings.TrimSpace(*req.Action)
	}
	if req.Params != nil {
		st.Params = *req.Params
	}
	if req.Enabled != nil {
		st.Enabled = *req.Enabled
	}
	if req.OnSuccessTaskID != nil {
		st.OnSuccessTaskID = nilIfZero(*req.OnSuccessTaskID)
	}
	if req.OnFailureTaskID != nil {
		st.OnFailureTaskID = nilIfZero(*req.OnFailureTaskID)
	}
	if req.GraceMinutes != nil {
		st.GraceMinutes = *req.GraceMinutes
	}
	if req.RetryCount != nil {
		st.RetryCount = *req.RetryCount
	}
	if req.RetryInterval != nil {
		st.RetryInterval = *req.RetryInterval
	}
	if req.Keep != nil {
		st.Keep = *req.Keep
	} else if st.Keep <= 0 {
		// 未显式给出且存量值非法（历史行零值）时兜底默认值，避免「只改名字」被
		// validateCronTask 的范围校验误拒
		st.Keep = cron.DefaultKeep
	}
	// 重试间隔与死开关宽限的存量零值兜底（RetryInterval 校验下限为 1，零值会被拒）
	if st.RetryInterval <= 0 {
		st.RetryInterval = cron.DefaultRetryInterval
	}
	if st.GraceMinutes < 0 {
		st.GraceMinutes = cron.DefaultGrace
	}
	_, err := validateCronTask(st)
	return err
}

// nilIfZero 把 0 转成 nil（链式引用清空语义：任务 id 从 1 起，0 表示不链）。
// 前端用 0 表示「取消链式引用」，DB 里用 NULL 表示同一语义；
// 这里做边界转换，避免把 0 当成真实任务 id 去查库。
func nilIfZero(v uint) *uint {
	if v == 0 {
		return nil
	}
	return &v
}

// ensureChainTargets 校验链式下一跳任务存在且不自指。返回 false 表示响应已写好。
// 成功/失败链各自独立校验；只查存在性，不递归检查环（A→B→A 在执行侧靠
// 任务深度/状态兜底，建链时不做图遍历，避免一次保存引发大量查询）。
func (h *CronsHandler) ensureChainTargets(c *gin.Context, st *model.ScheduledTask) bool {
	for _, ref := range []*uint{st.OnSuccessTaskID, st.OnFailureTaskID} {
		if ref == nil {
			continue
		}
		// 自指只可能在更新场景出现（新建时 ID 还是 0）
		if st.ID != 0 && *ref == st.ID {
			Fail(c, http.StatusBadRequest, "链式下一跳不能指向任务自身")
			return false
		}
		var n int64
		if err := h.DB.Model(&model.ScheduledTask{}).Where("id = ?", *ref).Count(&n).Error; err != nil {
			ErrorResponse(c, http.StatusInternalServerError, err)
			return false
		}
		if n == 0 {
			Fail(c, http.StatusBadRequest, fmt.Sprintf("链式下一跳任务 %d 不存在", *ref))
			return false
		}
	}
	return true
}

// validateCronTask 校验任务的名称/cron 表达式/动作/参数（纯校验，不查库），
// 并把 Params 归一化为白名单键的 JSON。vm_snapshot 时返回解析出的 vm_id（>0）。
// 错误消息为固定中文文案，调用方可直接 Fail 回显。
//
// 「归一化」是安全设计：Params 是用户可控 JSON，原样落库会被执行侧整包解析；
// 每个 action 只挑出自己认识的键重新序列化，多余键一律丢弃，既屏蔽脏数据，
// 也缩小未来执行侧误用未知字段的攻击面。
func validateCronTask(st *model.ScheduledTask) (uint, error) {
	if st.Name == "" {
		return 0, errors.New("任务名不能为空")
	}
	if utf8.RuneCountInString(st.Name) > 100 {
		return 0, errors.New("任务名不能超过 100 个字符")
	}
	if utf8.RuneCountInString(st.CronExpr) > 50 {
		return 0, errors.New("cron 表达式不能超过 50 个字符")
	}
	// 保留份数：1-365 份（未显式给值时 applyCronReq 已兜底默认值，0/负数到此即为非法入参）
	if st.Keep < 1 || st.Keep > 365 {
		return 0, fmt.Errorf("keep（保留份数）必须在 1-365 之间，当前为 %d", st.Keep)
	}
	// 死开关宽限：0=关闭；上限 1440（一天），超出按非法入参拒绝，避免逐分钟回看退化成大循环
	if st.GraceMinutes < 0 || st.GraceMinutes > 1440 {
		return 0, fmt.Errorf("grace_minutes（死开关宽限分钟）必须在 0-1440 之间，当前为 %d（0 表示关闭）", st.GraceMinutes)
	}
	// 重试次数/间隔上限都很小：定时任务的重试是「抖动补偿」，不是无限重跑
	if st.RetryCount < 0 || st.RetryCount > 3 {
		return 0, fmt.Errorf("retry_count（失败重试次数）必须在 0-3 之间，当前为 %d", st.RetryCount)
	}
	if st.RetryInterval < 1 || st.RetryInterval > 120 {
		return 0, fmt.Errorf("retry_interval（重试间隔秒）必须在 1-120 之间，当前为 %d", st.RetryInterval)
	}
	spec, err := cron.ParseCron(st.CronExpr)
	if err != nil {
		return 0, err // ParseCron 错误为固定中文文案，可直接回显
	}
	// 排除「永不匹配」的表达式（如 0 0 31 2 *——2 月 31 日不存在），建了也永远不跑
	if next := cron.Next(spec, time.Now()); next.IsZero() {
		return 0, errors.New("cron 表达式在未来 366 天内没有可执行时刻，请检查字段取值")
	}

	// 按 action 分派参数校验与归一化；未知 action 一律拒绝（白名单，不是黑名单）
	switch st.Action {
	case cron.ActionVMSnapshot:
		var params struct {
			VMID uint `json:"vm_id"`
		}
		if strings.TrimSpace(st.Params) == "" {
			return 0, errors.New(`vm_snapshot 需要 params，例如 {"vm_id":14}`)
		}
		if err := json.Unmarshal([]byte(st.Params), &params); err != nil || params.VMID == 0 {
			return 0, errors.New(`params 必须为 JSON 且包含有效的 vm_id，例如 {"vm_id":14}`)
		}
		// 归一化存储：只保留白名单键，屏蔽多余内容与注入面
		b, merr := json.Marshal(map[string]uint{"vm_id": params.VMID})
		if merr != nil {
			return 0, errors.New("params 序列化失败")
		}
		st.Params = string(b)
		// 返回 vm_id 供调用方做存在性/权限校验（本函数刻意不查库）
		return params.VMID, nil
	case cron.ActionDBBackup:
		st.Params = "{}" // 预留扩展位，当前无可配参数
		return 0, nil
	case cron.ActionAnsiblePlaybook:
		// P4-S3：归一化 playbook id（白名单正则）与目标；executor 会逐台复核运行态/IP/托管凭据，
		// 这里只做结构性校验。目标支持显式 targets 或主机组 group_id（后者成员动态生效）。
		var params struct {
			Playbook string `json:"playbook"`
			Targets  []uint `json:"targets"`
			GroupID  uint   `json:"group_id"`
		}
		if strings.TrimSpace(st.Params) == "" {
			return 0, errors.New(`ansible_playbook 需要 params，例如 {"playbook":"sysctl-tuning","targets":[89]} 或 {"playbook":"sysctl-tuning","group_id":3}`)
		}
		if err := json.Unmarshal([]byte(st.Params), &params); err != nil {
			return 0, errors.New(`params 必须为 JSON，例如 {"playbook":"sysctl-tuning","targets":[89]}`)
		}
		// playbook 名会拼进 data/ansible/playbooks/<id>.yml，必须先过正则白名单防目录穿越
		if !cron.AnsiblePlaybookIDRe.MatchString(params.Playbook) {
			return 0, errors.New("playbook ID 非法（字母数字与 -_，字母开头）")
		}
		// 存在性检查必须走 ResolvePlaybook：只认 <id>.yml 会把目录形态
		// （<id>/site.yml，如 web-stack）误判为不存在，而手动执行/executor 都支持。
		if _, _, ok := ansible.ResolvePlaybook(config.DataPath("ansible", "playbooks"), params.Playbook); !ok {
			return 0, fmt.Errorf("playbook 不存在: %s", params.Playbook)
		}
		// targets 与 group_id 至少给一个：否则任务会「成功执行 0 台」，看起来像静默失败
		if len(params.Targets) == 0 && params.GroupID == 0 {
			return 0, errors.New("目标不能为空（targets 或 group_id 至少其一）")
		}
		// 单次 50 台上限：批量执行是串行/小并发，放开会导致任务长时间占用 worker
		if len(params.Targets) > 50 {
			return 0, errors.New("单次执行最多 50 台")
		}
		norm := map[string]interface{}{"playbook": params.Playbook}
		if len(params.Targets) > 0 {
			norm["targets"] = params.Targets
		}
		if params.GroupID > 0 {
			norm["group_id"] = params.GroupID
		}
		b, merr := json.Marshal(norm)
		if merr != nil {
			return 0, errors.New("params 序列化失败")
		}
		st.Params = string(b)
		return 0, nil
	case cron.ActionContainerHealthcheck:
		// 归一化：只保留白名单键 notify（默认 true），屏蔽多余内容。
		// 解析失败也放行并按默认值处理：健康巡检本身无危险动作，不值得因脏参数拒绝。
		notifyOn := true
		if strings.TrimSpace(st.Params) != "" {
			var params struct {
				Notify *bool `json:"notify"`
			}
			if err := json.Unmarshal([]byte(st.Params), &params); err == nil && params.Notify != nil {
				notifyOn = *params.Notify
			}
		}
		b, merr := json.Marshal(map[string]bool{"notify": notifyOn})
		if merr != nil {
			return 0, errors.New("params 序列化失败")
		}
		st.Params = string(b)
		return 0, nil
	case cron.ActionImageVersionCheck:
		st.Params = "{}" // 无参数
		return 0, nil
	case cron.ActionDesignerDrift:
		st.Params = "{}" // 无参数：巡检全部有快照的计划
		return 0, nil
	default:
		return 0, errors.New("action 只支持 vm_snapshot（定时快照）/ db_backup（定时备份数据库）/ ansible_playbook（定时执行 playbook）/ container_healthcheck（容器健康巡检）/ image_version_check（镜像版本巡检）/ designer_drift_check（架构漂移巡检）")
	}
}

// ensureVMExists 校验 vm_snapshot 任务的 vm_id 指向存在的虚拟机（软删的查不到）。
// 返回 false 表示响应已写好（DB 故障 500 / 查无此项 400），调用方直接 return。
// 非快照动作直接放行；权限层面由路由中间件与执行侧负责，这里只保证目标存在。
func (h *CronsHandler) ensureVMExists(c *gin.Context, st *model.ScheduledTask) bool {
	if st.Action != cron.ActionVMSnapshot {
		return true
	}
	var params struct {
		VMID uint `json:"vm_id"`
	}
	// validateCronTask 已归一化 Params，这里解析失败只可能是脏数据，按参数错误处理
	if err := json.Unmarshal([]byte(st.Params), &params); err != nil || params.VMID == 0 {
		Fail(c, http.StatusBadRequest, "params 必须包含有效的 vm_id")
		return false
	}
	var count int64
	if err := h.DB.Model(&model.VM{}).Where("id = ?", params.VMID).Count(&count).Error; err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return false
	}
	if count == 0 {
		Fail(c, http.StatusBadRequest, fmt.Sprintf("虚拟机 %d 不存在", params.VMID))
		return false
	}
	return true
}
