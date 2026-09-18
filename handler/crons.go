package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/cron"
	"gorm.io/gorm"
)

// 列表内嵌执行记录的条数与执行历史接口的默认页大小。
const (
	// listRecentRunLimit 列表项 recent_runs 内嵌的最近执行记录条数。
	listRecentRunLimit = 3
	// runsPageSize 执行历史接口 page_size 默认值（同时是越界回退值），上限 100。
	runsPageSize = 20
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

// cronTaskItem 列表项：任务本体 + 下次执行时间预览 + 最近执行记录。
type cronTaskItem struct {
	model.ScheduledTask
	NextRun    *time.Time    `json:"next_run"` // 表达式非法或 366 天内无匹配（如 2 月 31 日）时为 null
	RecentRuns []cronRunItem `json:"recent_runs"`
}

// cronRunItem 执行历史条目（列表内嵌精简版；完整历史走 GET /api/crons/:id/runs）。
type cronRunItem struct {
	Status    string    `json:"status"`     // running / success / failed（model.CronRun 状态常量）
	StartedAt time.Time `json:"started_at"` // 执行开始时间
	Output    string    `json:"output"`     // 结果摘要：成功为成果描述、失败为错误摘要（≤2000 字符）
}

// cronTaskReq 创建/更新请求体。全部字段可选：Create 用默认值兜底，Update 只改出现的键，
// 校验始终针对「合并后的完整任务」做，避免部分更新造成 action 与 params 不配套。
type cronTaskReq struct {
	Name     *string `json:"name"`
	CronExpr *string `json:"cron_expr"`
	Action   *string `json:"action"`
	Params   *string `json:"params"`
	Enabled  *bool   `json:"enabled"`
	Keep     *int    `json:"keep"` // 保留最近 N 份产物（快照/备份），1-365
}

// List GET /api/crons：全部任务 + 每条的下次执行时间预览 + 最近 3 条执行记录。
func (h *CronsHandler) List(c *gin.Context) {
	var tasks []model.ScheduledTask
	if err := h.DB.Order("id").Find(&tasks).Error; err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	items := make([]cronTaskItem, 0, len(tasks))
	now := time.Now()
	for _, st := range tasks {
		item := cronTaskItem{ScheduledTask: st, RecentRuns: []cronRunItem{}}
		if spec, err := cron.ParseCron(st.CronExpr); err == nil {
			if next := cron.Next(spec, now); !next.IsZero() {
				item.NextRun = &next
			}
		}
		item.RecentRuns = h.recentRuns(st.ID, listRecentRunLimit)
		items = append(items, item)
	}
	Success(c, gin.H{"total": len(items), "items": items})
}

// recentRuns 查询任务最近 limit 条执行记录（按开始时间倒序）。
// 查询失败降级返回空切片并记日志——执行历史缺席不影响任务列表可用性。
// 每任务一条走 task_id 索引的 limit 小查询，任务数量级为个位数/十位数，不构成热点。
func (h *CronsHandler) recentRuns(taskID uint, limit int) []cronRunItem {
	runs := make([]model.CronRun, 0, limit)
	if err := h.DB.Where("task_id = ?", taskID).
		Order("started_at DESC, id DESC").Limit(limit).Find(&runs).Error; err != nil {
		log.Printf("[crons] 查询执行历史失败 task_id=%d: %v", taskID, err)
		return []cronRunItem{}
	}
	items := make([]cronRunItem, 0, len(runs))
	for _, r := range runs {
		items = append(items, cronRunItem{Status: r.Status, StartedAt: r.StartedAt, Output: r.Output})
	}
	return items
}

// ListRuns GET /api/crons/:id/runs?page=&page_size=：分页返回该任务的执行历史（倒序）。
// page 从 1 起，page_size 默认 20、上限 100，兼容 limit 参数（语义等价 page_size）。
// 返回 {total, page, page_size, items}，total 为该任务执行记录的真实总数。
func (h *CronsHandler) ListRuns(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	// 先验任务存在：与其它端点 404 口径一致，也避免对不存在任务翻历史
	var count int64
	if err := h.DB.Model(&model.ScheduledTask{}).Where("id = ?", id).Count(&count).Error; err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	if count == 0 {
		Fail(c, http.StatusNotFound, "计划任务不存在")
		return
	}
	page := 1
	if s := c.Query("page"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			page = n
		}
	}
	pageSize := runsPageSize
	if s := c.Query("page_size"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			pageSize = n
		}
	} else if s := c.Query("limit"); s != "" {
		// 旧参数兼容：limit 语义等价 page_size
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			pageSize = n
		}
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = runsPageSize
	}
	var total int64
	if err := h.DB.Model(&model.CronRun{}).Where("task_id = ?", id).Count(&total).Error; err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	runs := make([]model.CronRun, 0, pageSize)
	if err := h.DB.Where("task_id = ?", id).
		Order("started_at DESC, id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&runs).Error; err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{
		"total":     total,
		"page":      page,
		"page_size": pageSize,
		"items":     runs,
	})
}

// Create POST /api/crons：新建计划任务。
func (h *CronsHandler) Create(c *gin.Context) {
	var req cronTaskReq
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	// 未显式给 enabled 时默认启用；未给 keep 时取保留份数默认值（7）
	st := model.ScheduledTask{Enabled: true, Keep: cron.DefaultKeep}
	if err := applyCronReq(&st, req); err != nil {
		Fail(c, http.StatusBadRequest, err.Error()) // 校验错误为固定中文文案，可直接回显
		return
	}
	if !h.ensureVMExists(c, &st) {
		return
	}
	// Select 强制写入全部列：Enabled 的 gorm default:true 会让零值 false 被 INSERT 省略，
	// 显式列出字段才能落库「创建即停用」的语义（Keep 已显式赋默认值，一并列入）
	if err := h.DB.Select("Name", "CronExpr", "Action", "Params", "Enabled", "Keep").Create(&st).Error; err != nil {
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
	if req.Keep != nil {
		st.Keep = *req.Keep
	} else if st.Keep <= 0 {
		// 未显式给出且存量值非法（历史行零值）时兜底默认值，避免「只改名字」被
		// validateCronTask 的范围校验误拒
		st.Keep = cron.DefaultKeep
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
	// 保留份数：1-365 份（未显式给值时 applyCronReq 已兜底默认值，0/负数到此即为非法入参）
	if st.Keep < 1 || st.Keep > 365 {
		return 0, fmt.Errorf("keep（保留份数）必须在 1-365 之间，当前为 %d", st.Keep)
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
