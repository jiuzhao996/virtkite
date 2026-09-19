package handler

// 工具箱（批次 E）：面向管理员的宿主机快捷运维工具（进程 / 磁盘 / docker 清理 / 任务记录清理）。
//
// 实现约定：
//   - 全部仅 admin（每个方法先过 requireAdminRole 二次收口，路由建议另挂 admin 组双保险）。
//   - 外部命令一律 exec.CommandContext + 数组参数，禁止 shell 拼接；统一 30s 超时
//     （与 service/dockerx 的 runTimeout 同一模式，工具类命令不依赖 dockerx 故在本文件自持）。
//   - 命令完整失败原因进日志，前端只收中文友好消息（handler 层错误纪律）。

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/dockerx"
	"gorm.io/gorm"
)

// ToolboxHandler 工具箱处理器。
type ToolboxHandler struct {
	DB      *gorm.DB
	Docker  *dockerx.Dockerx // 仅用于可用性探测；docker prune 直接 exec，避免为一条命令扩封装层
	DataDir string           // 平台数据目录（du 统计占用），由组装方传入
}

// NewToolboxHandler 创建工具箱处理器（dataDir 传平台数据目录绝对路径）。
func NewToolboxHandler(db *gorm.DB, docker *dockerx.Dockerx, dataDir string) *ToolboxHandler {
	return &ToolboxHandler{DB: db, Docker: docker, DataDir: dataDir}
}

// runTool 执行工具类命令：数组参数 + 超时兜底，失败带原始输出包装（供日志留痕）。
func runTool(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("%s 命令超时: %w", name, err)
		}
		if msg != "" {
			return "", fmt.Errorf("%s: %w", msg, err)
		}
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return string(out), nil
}

// procItem 进程列表条目（ps 字段原样数值化，便于前端排序）。
type procItem struct {
	PID  int     `json:"pid"`
	PPID int     `json:"ppid"`
	User string  `json:"user"`
	CPU  float64 `json:"cpu"`
	Mem  float64 `json:"mem"`
	Comm string  `json:"comm"`
}

// Processes 宿主机进程列表（admin）。GET /api/toolbox/processes?sort=cpu|mem
// 对应 `ps -eo pid,ppid,user,pcpu,pmem,comm --sort=-pcpu`，取占用前 20。
func (h *ToolboxHandler) Processes(c *gin.Context) {
	if !requireAdminRole(c) {
		return
	}
	sortKey := "-pcpu"
	if c.Query("sort") == "mem" {
		sortKey = "-pmem"
	}
	out, err := runTool(30*time.Second, "ps", "-eo", "pid,ppid,user,pcpu,pmem,comm", "--sort="+sortKey)
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "读取进程列表失败", err)
		return
	}
	items := parseProcTop(out, 20)
	Success(c, gin.H{"total": len(items), "items": items})
}

// parseProcTop 解析 ps 输出前 limit 行数据（跳表头，脏行跳过不毁整表）。
// comm 可能含空格（如 "tmux: server"），故固定取前 5 个数值字段、其余归并回 comm。
func parseProcTop(out string, limit int) []procItem {
	items := make([]procItem, 0, limit)
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue // 表头或空行
		}
		pid, errPID := strconv.Atoi(fields[0])
		ppid, errPPID := strconv.Atoi(fields[1])
		cpu, errCPU := strconv.ParseFloat(fields[3], 64)
		mem, errMEM := strconv.ParseFloat(fields[4], 64)
		if errPID != nil || errPPID != nil || errCPU != nil || errMEM != nil {
			continue
		}
		items = append(items, procItem{
			PID:  pid,
			PPID: ppid,
			User: fields[2],
			CPU:  cpu,
			Mem:  mem,
			Comm: strings.Join(fields[5:], " "),
		})
		if len(items) >= limit {
			break
		}
	}
	return items
}

// DiskUsage 磁盘占用（admin）。GET /api/toolbox/disk
// 根分区走 `df -h /`；平台数据目录占用走 `du -sh`（目录不存在或统计失败降级为空串，不拖垮请求）。
func (h *ToolboxHandler) DiskUsage(c *gin.Context) {
	if !requireAdminRole(c) {
		return
	}
	out, err := runTool(30*time.Second, "df", "-h", "/")
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "读取磁盘占用失败", err)
		return
	}
	root := parseDFRoot(out)
	if root == nil {
		Fail(c, http.StatusInternalServerError, "磁盘占用输出解析失败")
		return
	}

	dataDirSize := ""
	if h.DataDir != "" {
		if _, err := os.Stat(h.DataDir); err == nil {
			if duOut, err := runTool(30*time.Second, "du", "-sh", h.DataDir); err == nil {
				if fields := strings.Fields(duOut); len(fields) > 0 {
					dataDirSize = fields[0]
				}
			} else {
				log.Printf("[toolbox] 统计数据目录占用失败 dir=%s err=%v", h.DataDir, err)
			}
		}
	}
	Success(c, gin.H{
		"root":          root,
		"data_dir":      h.DataDir,
		"data_dir_size": dataDirSize,
	})
}

// parseDFRoot 解析 `df -h /` 输出，取数据行（第二行起）的 total/used/avail/use_pct。
func parseDFRoot(out string) gin.H {
	for i, line := range strings.Split(out, "\n") {
		if i == 0 {
			continue // 表头
		}
		fields := strings.Fields(strings.TrimSpace(line))
		// 挂载点带空格会拆出多余字段，故从左取固定 4 列 + Use%
		if len(fields) < 5 {
			continue
		}
		return gin.H{
			"total":   fields[1],
			"used":    fields[2],
			"avail":   fields[3],
			"use_pct": strings.TrimSuffix(fields[4], "%"),
		}
	}
	return nil
}

// DockerPrune 清理 docker 悬空镜像（admin）。POST /api/toolbox/docker-prune
// 对应 `docker image prune -f`（仅悬空镜像，不动在用镜像，无 -a 不做全量清理）。
func (h *ToolboxHandler) DockerPrune(c *gin.Context) {
	if !requireAdminRole(c) {
		return
	}
	if ok, msg := h.Docker.Available(); !ok {
		ErrorWithMessage(c, http.StatusServiceUnavailable, "docker 不可用："+msg, nil)
		return
	}
	out, err := runTool(30*time.Second, "docker", "image", "prune", "-f")
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "docker 镜像清理失败", err)
		return
	}
	trimmed := strings.TrimSpace(out)
	message := "清理完成"
	// 成功输出末行为 "Total reclaimed space: 1.234GB"，提取给用户看释放了多少
	if i := strings.LastIndex(trimmed, "Total reclaimed space:"); i >= 0 {
		if freed := strings.TrimSpace(trimmed[i+len("Total reclaimed space:"):]); freed != "" {
			message = "清理完成，释放空间 " + freed
		}
	}
	Success(c, gin.H{"message": message, "output": trimmed})
}

// PurgeOldTasks 清理历史任务记录（admin）。POST /api/toolbox/tasks-purge?days=30
// 物理删除 created_at 早于 N 天前的 tasks 行（过程表只增不改，清理即删除），返回删除行数。
func (h *ToolboxHandler) PurgeOldTasks(c *gin.Context) {
	if !requireAdminRole(c) {
		return
	}
	days, err := strconv.Atoi(c.DefaultQuery("days", "30"))
	if err != nil {
		days = 30
	}
	if days < 1 || days > 365 {
		Fail(c, http.StatusBadRequest, "days 取值范围为 1-365")
		return
	}
	res := h.DB.Where("created_at < ?", time.Now().AddDate(0, 0, -days)).Delete(&model.Task{})
	if res.Error != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "清理任务记录失败", res.Error)
		return
	}
	Success(c, gin.H{
		"deleted": res.RowsAffected,
		"days":    days,
		"message": fmt.Sprintf("已清理 %d 天前的任务记录 %d 条", days, res.RowsAffected),
	})
}
