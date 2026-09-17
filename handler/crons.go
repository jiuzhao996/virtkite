package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/cron"
	"gorm.io/gorm"
)

// CronsHandler 计划任务处理器：CRUD + 启停 + 手动触发。
// 属平台管理语义，路由应挂 AdminMiddleware（仅管理员）。
type CronsHandler struct {
	DB        *gorm.DB
	Scheduler *cron.Scheduler
}

// NewCronsHandler 创建计划任务处理器。
func NewCronsHandler(db *gorm.DB, sched *cron.Scheduler) *CronsHandler {
	return &CronsHandler{DB: db, Scheduler: sched}
}

// cronTaskItem 列表项：任务本体 + 下次执行时间预览。
type cronTaskItem struct {
	model.ScheduledTask
	NextRun *time.Time `json:"next_run"` // 表达式非法或 366 天内无匹配（如 2 月 31 日）时为 null
}

// cronTaskReq 创建/更新请求体。全部字段可选：Create 用默认值兜底，Update 只改出现的键，
// 校验始终针对「合并后的完整任务」做，避免部分更新造成 action 与 params 不配套。
type cronTaskReq struct {
	Name     *string `json:"name"`
	CronExpr *string `json:"cron_expr"`
	Action   *string `json:"action"`
	Params   *string `json:"params"`
	Enabled  *bool   `json:"enabled"`
}

// List GET /api/crons：全部任务 + 每条的下次执行时间预览。
func (h *CronsHandler) List(c *gin.Context) {
	var tasks []model.ScheduledTask
	if err := h.DB.Order("id").Find(&tasks).Error; err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	items := make([]cronTaskItem, 0, len(tasks))
	now := time.Now()
	for _, st := range tasks {
		item := cronTaskItem{ScheduledTask: st}
		if spec, err := cron.ParseCron(st.CronExpr); err == nil {
			if next := cron.Next(spec, now); !next.IsZero() {
				item.NextRun = &next
			}
		}
		items = append(items, item)
	}
	Success(c, gin.H{"total": len(items), "items": items})
}

// Create POST /api/crons：新建计划任务。
func (h *CronsHandler) Create(c *gin.Context) {
	var req cronTaskReq
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	st := model.ScheduledTask{Enabled: true} // 未显式给 enabled 时默认启用
	if err := applyCronReq(&st, req); err != nil {
		Fail(c, http.StatusBadRequest, err.Error()) // 校验错误为固定中文文案，可直接回显
		return
	}
	if !h.ensureVMExists(c, &st) {
		return
	}
	// Select 强制写入全部列：Enabled 的 gorm default:true 会让零值 false 被 INSERT 省略，
	// 显式列出字段才能落库「创建即停用」的语义
	if err := h.DB.Select("Name", "CronExpr", "Action", "Params", "Enabled").Create(&st).Error; err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Created(c, "计划任务已创建", st)
}

// Update PUT /api/crons/:id：修改计划任务（只更新请求中出现的字段）。
func (h *CronsHandler) Update(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	var st model.ScheduledTask
	if err := h.DB.First(&st, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "计划任务不存在", err)
		return
	}
	var req cronTaskReq
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if err := applyCronReq(&st, req); err != nil {
		Fail(c, http.StatusBadRequest, err.Error()) // 校验错误为固定中文文案，可直接回显
		return
	}
	if !h.ensureVMExists(c, &st) {
		return
	}
	if err := h.DB.Save(&st).Error; err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, st)
}

// Delete DELETE /api/crons/:id：删除计划任务（不影响已产生的快照与备份文件）。
func (h *CronsHandler) Delete(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	res := h.DB.Delete(&model.ScheduledTask{}, id)
	if res.Error != nil {
		ErrorResponse(c, http.StatusInternalServerError, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		Fail(c, http.StatusNotFound, "计划任务不存在")
		return
	}
	Success(c, gin.H{"message": "计划任务已删除"})
}

// Toggle POST /api/crons/:id/toggle：启用/停用翻转（停用后调度器跳过，定义保留）。
func (h *CronsHandler) Toggle(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	var st model.ScheduledTask
	if err := h.DB.First(&st, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "计划任务不存在", err)
		return
	}
	if err := h.DB.Model(&st).Update("enabled", !st.Enabled).Error; err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	st.Enabled = !st.Enabled
	Created(c, taskEnabledText(st.Enabled), st)
}

// RunNow POST /api/crons/:id/run：手动触发一次，与定时执行共用同一执行通道。
func (h *CronsHandler) RunNow(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	var st model.ScheduledTask
	if err := h.DB.First(&st, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "计划任务不存在", err)
		return
	}
	if err := h.Scheduler.ExecuteNow(st); err != nil {
		// 执行细节（libvirt/docker 错误链）已进服务端日志，前端只给固定文案
		ErrorWithMessage(c, http.StatusInternalServerError, "计划任务执行失败，详情请查看服务端日志", err)
		return
	}
	// execute 已更新 LastRun/RunCount，尽力重读返回最新值；重读失败不影响执行成功语义
	if err := h.DB.First(&st, id).Error; err == nil {
		Success(c, gin.H{"message": "已执行一次", "item": st})
		return
	}
	Success(c, gin.H{"message": "已执行一次"})
}

// applyCronReq 把请求中出现的字段合并到任务上，并对合并后的完整任务做校验与参数归一化。
// 返回的 error 均为固定中文文案（可直接回显），不含内部细节。
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
	_, err := validateCronTask(st)
	return err
}

// validateCronTask 校验任务的名称/cron 表达式/动作/参数（纯校验，不查库），
// 并把 Params 归一化为白名单键的 JSON。vm_snapshot 时返回解析出的 vm_id（>0）。
// 错误消息为固定中文文案，调用方可直接 Fail 回显。
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
	spec, err := cron.ParseCron(st.CronExpr)
	if err != nil {
		return 0, err // ParseCron 错误为固定中文文案，可直接回显
	}
	// 排除「永不匹配」的表达式（如 0 0 31 2 *——2 月 31 日不存在），建了也永远不跑
	if next := cron.Next(spec, time.Now()); next.IsZero() {
		return 0, errors.New("cron 表达式在未来 366 天内没有可执行时刻，请检查字段取值")
	}

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
		return params.VMID, nil
	case cron.ActionDBBackup:
		st.Params = "{}" // 预留扩展位，当前无可配参数
		return 0, nil
	default:
		return 0, errors.New("action 只支持 vm_snapshot（定时快照）或 db_backup（定时备份数据库）")
	}
}

// ensureVMExists 校验 vm_snapshot 任务的 vm_id 指向存在的虚拟机（软删的查不到）。
// 返回 false 表示响应已写好（DB 故障 500 / 查无此项 400），调用方直接 return。
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

// taskEnabledText 启停结果文案
func taskEnabledText(enabled bool) string {
	if enabled {
		return "计划任务已启用"
	}
	return "计划任务已停用"
}
