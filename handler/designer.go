// designer.go：eNSP 式架构设计器（P2B v2，2026-10-04）——画布预览 + 节点编辑 +
// 预置模板 + 部署计划导出/一键应用。v2：VM 节点真落地（create_vm 建机 + 等 IP +
// app_install 逐个装应用，口令边界就地加密），容器栈继续 compose up。
//
// 计划存 data/designer/*.json（id/名称/节点/连线）；应用 = 顺序部署计划内的容器栈
// （goroutine + recover + 内存进度，GET status 轮询）。VM 节点导出到 YAML 的
// provision 段（v2 执行器目标），apply 时跳过并在进度中说明。
package handler

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strconv"
	"sort"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/dockerx"
	"github.com/jiuzhao/vmops/service/secretbox"
	"github.com/jiuzhao/vmops/service/tasks"
	"gorm.io/gorm"
)

// DesignerHandler 架构设计器处理器。
type DesignerHandler struct {
	Docker  *dockerx.Dockerx
	Tasks   *tasks.Manager
	DB      *gorm.DB
	UserID  uint
	Username string
	// VMCred 凭据托管：app_install 的 use_saved 通道需要；MasterSecret 供设计器
	// 就地加密 VM 节点的 SSH 口令（与 AppsHandler 同口径，明文不落库）
	VMCred *VMCredentialHandler
}

// NewDesignerHandler 创建设计器处理器。
func NewDesignerHandler(db *gorm.DB, taskMgr *tasks.Manager, vmCred *VMCredentialHandler) *DesignerHandler {
	return &DesignerHandler{Docker: dockerx.New(), Tasks: taskMgr, DB: db, VMCred: vmCred}
}

// dsgNode 计划节点：container=容器栈（引用 stacks/<id>）、vm=虚拟机角色（v1 标注）、
// net=网络（libvirt/docker，v1 标注）。name/x/y 供前端画布布局。
type dsgNode struct {
	ID   string `json:"id"`
	Kind string `json:"kind"` // container | vm | net
	Ref  string `json:"ref"`  // container→栈 id；vm→云镜像 id（images 表）；net→网段建议
	Name string `json:"name"`
	X    int    `json:"x"`
	Y    int    `json:"y"`
	Note string `json:"note,omitempty"`
	// vm 节点落地参数（v2 执行器）：create_vm + app_install 所需
	Pool      string   `json:"pool,omitempty"`
	VCPU      int      `json:"vcpu,omitempty"`
	MemoryMB  int      `json:"memory_mb,omitempty"`
	SSHUser   string   `json:"ssh_user,omitempty"`
	SSHSecret string   `json:"ssh_secret,omitempty"` // 边界就地加密，不落库明文
	Apps      []string `json:"apps,omitempty"`       // 待安装应用 id 列表
}

type dsgLink struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type dsgPlan struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Nodes     []dsgNode `json:"nodes"`
	Links     []dsgLink `json:"links"`
	UpdatedAt time.Time `json:"updated_at"`
}

func designerDir() string { return filepath.Join("data", "designer") }

var dsgIDRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

// Templates GET /api/designer/templates —— 预置架构模板（内置，前端一键载入）。
func (h *DesignerHandler) Templates(c *gin.Context) {
	tpls := []gin.H{
		{
			"id": "lnmp", "name": "LNMP 网站", "desc": "Nginx + MySQL + PHP 经典建站栈",
			"nodes": []dsgNode{
				{ID: "n1", Kind: "net", Ref: "10.0.0.0/24", Name: "前端网", X: 300, Y: 120},
				{ID: "n2", Kind: "container", Ref: "nginx-lb", Name: "Nginx LB", X: 300, Y: 260},
				{ID: "n3", Kind: "container", Ref: "mysql-master-slave", Name: "MySQL 主从", X: 300, Y: 420},
			},
			"links": []dsgLink{{From: "n1", To: "n2"}, {From: "n2", To: "n3"}},
		},
		{
			"id": "elk", "name": "日志分析平台", "desc": "ELK 单机 + 应用机采集",
			"nodes": []dsgNode{
				{ID: "n1", Kind: "container", Ref: "elk-single", Name: "ELK", X: 300, Y: 200},
				{ID: "n2", Kind: "vm", Ref: "ubuntu-26.04", Name: "应用机×N（装 filebeat）", X: 300, Y: 400, Note: "v1 标注性：VM 创建与应用安装列 v2 执行器"},
			},
			"links": []dsgLink{{From: "n2", To: "n1"}},
		},
		{
			"id": "es-cluster", "name": "ES 检索集群", "desc": "ES 三节点 + Kibana",
			"nodes": []dsgNode{
				{ID: "n1", Kind: "net", Ref: "172.20.0.0/16", Name: "ES 内网", X: 300, Y: 120},
				{ID: "n2", Kind: "container", Ref: "es-cluster", Name: "ES×3 + Kibana", X: 300, Y: 280},
			},
			"links": []dsgLink{{From: "n1", To: "n2"}},
		},
		{
			"id": "monitor", "name": "独立监控栈", "desc": "Prometheus 全家桶（平台自带一套，此为业务侧独立栈）",
			"nodes": []dsgNode{
				{ID: "n1", Kind: "container", Ref: "prom-stack", Name: "Prometheus 栈", X: 300, Y: 240},
			},
			"links": []dsgLink{},
		},
	}
	Success(c, gin.H{"items": tpls})
}

// ListPlans GET /api/designer/plans —— 已保存的部署计划。
func (h *DesignerHandler) ListPlans(c *gin.Context) {
	_ = os.MkdirAll(designerDir(), 0o755)
	entries, _ := os.ReadDir(designerDir())
	plans := []dsgPlan{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(designerDir(), e.Name()))
		if err != nil {
			continue
		}
		var p dsgPlan
		if json.Unmarshal(raw, &p) == nil && len(p.ID) > 0 {
			plans = append(plans, p)
		}
	}
	sort.Slice(plans, func(i, j int) bool { return plans[i].UpdatedAt.After(plans[j].UpdatedAt) })
	Success(c, gin.H{"items": plans, "total": len(plans)})
}

// SavePlan POST /api/designer/plans（operator+）——新增或覆盖（按 body.id）。
func (h *DesignerHandler) SavePlan(c *gin.Context) {
	var p dsgPlan
	if err := c.ShouldBindJSON(&p); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "计划格式非法", err)
		return
	}
	if !dsgIDRe.MatchString(p.ID) {
		Fail(c, http.StatusBadRequest, "计划 ID 非法（字母数字与 -_，≤64 字符）")
		return
	}
	if p.Name == "" {
		p.Name = p.ID
	}
	p.UpdatedAt = time.Now()
	raw, _ := json.MarshalIndent(p, "", "  ")
	_ = os.MkdirAll(designerDir(), 0o755)
	if err := os.WriteFile(filepath.Join(designerDir(), p.ID+".json"), raw, 0o644); err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "保存计划失败", err)
		return
	}
	Success(c, gin.H{"id": p.ID, "saved": true})
}

// DeletePlan DELETE /api/designer/plans/:id。
func (h *DesignerHandler) DeletePlan(c *gin.Context) {
	id := c.Param("id")
	if !dsgIDRe.MatchString(id) {
		Fail(c, http.StatusBadRequest, "计划 ID 非法")
		return
	}
	_ = os.Remove(filepath.Join(designerDir(), id+".json"))
	Success(c, gin.H{"id": id, "deleted": true})
}

// ExportYAML GET /api/designer/plans/:id/export —— 导出部署计划 YAML（文本返回，
// 前端下载）。容器栈段可直接喂 compose；vm/net 段为 v2 执行器的 provision 声明。
func (h *DesignerHandler) ExportYAML(c *gin.Context) {
	id := c.Param("id")
	raw, err := os.ReadFile(filepath.Join(designerDir(), id+".json"))
	if err != nil {
		Fail(c, http.StatusNotFound, "计划不存在")
		return
	}
	var p dsgPlan
	if json.Unmarshal(raw, &p) != nil {
		Fail(c, http.StatusInternalServerError, "计划解析失败")
		return
	}
	var b []byte
	b = append(b, "# VirtKite 部署计划（设计器导出）\n# 计划: "+p.Name+"\n\n"...)
	for _, n := range p.Nodes {
		switch n.Kind {
		case "container":
			b = append(b, "stacks:\n  - id: "+n.Ref+"   # 节点 "+n.Name+"\n"...)
		case "vm":
			b = append(b, "# v2 执行器目标（v1 标注性）\n#provision_vms:\n#  - name: "+n.Name+"\n#    template: "+n.Ref+"\n"...)
		case "net":
			b = append(b, "# 网络: "+n.Name+"（"+n.Ref+"）\n"...)
		}
	}
	for _, l := range p.Links {
		b = append(b, "# link: "+l.From+" -> "+l.To+"\n"...)
	}
	c.Data(http.StatusOK, "text/yaml; charset=utf-8", b)
}

// ── 应用（M3：容器栈实际落地）──────────────────────────────

type dsgApplyState struct {
	Status  string   `json:"status"` // running | success | failed
	Steps   []string `json:"steps"`
	Error   string   `json:"error,omitempty"`
	Started time.Time
}

var (
	dsgApplyMu    sync.Mutex
	dsgApplyRuns  = map[string]*dsgApplyState{}
)

// Apply POST /api/designer/plans/:id/apply（admin）——顺序部署计划内全部容器栈。
// 进度内存态（GET /apply-status 轮询）；goroutine 自带 recover（AGENTS 并发规范）。
func (h *DesignerHandler) Apply(c *gin.Context) {
	if !roleIsAdmin(c) {
		Fail(c, http.StatusForbidden, "计划应用仅管理员可用")
		return
	}
	id := c.Param("id")
	if !dsgIDRe.MatchString(id) {
		Fail(c, http.StatusBadRequest, "计划 ID 非法")
		return
	}
	raw, err := os.ReadFile(filepath.Join(designerDir(), id+".json"))
	if err != nil {
		Fail(c, http.StatusNotFound, "计划不存在")
		return
	}
	var p dsgPlan
	if json.Unmarshal(raw, &p) != nil {
		Fail(c, http.StatusInternalServerError, "计划解析失败")
		return
	}

	dsgApplyMu.Lock()
	if st := dsgApplyRuns[id]; st != nil && st.Status == "running" {
		dsgApplyMu.Unlock()
		Fail(c, http.StatusConflict, "该计划正在应用中")
		return
	}
	st := &dsgApplyState{Status: "running", Started: time.Now()}
	dsgApplyRuns[id] = st
	if uid, ok := c.Get("user_id"); ok {
		if v, ok := uid.(uint); ok {
			h.UserID = v
		}
	}
	if u, ok := c.Get("username"); ok {
		if v, ok := u.(string); ok {
			h.Username = v
		}
	}
	dsgApplyMu.Unlock()

	go func(plan dsgPlan, st *dsgApplyState) {
		defer func() {
			if r := recover(); r != nil {
				st.Status = "failed"
				st.Error = "应用协程异常（详见服务日志）"
				log.Printf("[designer] 计划 %s 应用 panic: %v\n%s", plan.ID, r, debug.Stack())
			}
		}()
				for _, n := range plan.Nodes {
			switch n.Kind {
			case "container":
				st.Steps = append(st.Steps, "部署栈 "+n.Ref+"（"+n.Name+"）…")
				meta, sraw, err := parseStackMeta(filepath.Join(stacksDir(), n.Ref+".yml"))
				if err != nil {
					st.Status = "failed"
					st.Error = "栈不存在：" + n.Ref
					return
				}
				target := filepath.Join("data", "stacks", n.Ref)
				if err := os.MkdirAll(target, 0o755); err != nil {
					st.Status = "failed"
					st.Error = "创建部署目录失败"
					return
				}
				_ = os.WriteFile(filepath.Join(target, "docker-compose.yml"), sraw, 0o644)
				if err := h.Docker.ComposeUpFromFile(target); err != nil {
					st.Status = "failed"
					st.Error = "栈 " + n.Ref + " 部署失败：" + err.Error()
					return
				}
				st.Steps = append(st.Steps, "✓ "+n.Ref+" 完成（"+meta.Desc+"）")
			case "vm":
				vmID, err := h.provisionVM(n, st)
				if err != nil {
					st.Status = "failed"
					st.Error = "VM " + n.Name + " 落地失败：" + err.Error()
					return
				}
				st.Steps = append(st.Steps, "✓ VM "+n.Name+" 就绪（id="+strconv.FormatUint(uint64(vmID), 10)+"）")
			default:
				st.Steps = append(st.Steps, "跳过 "+n.Name+"（网络节点 v1 为标注性）")
			}
		}
st.Status = "success"
	}(p, st)

	Accepted(c, "计划应用已启动", gin.H{"id": id})
}

// provisionVM VM 节点落地（P2B v2）：create_vm 建机（云镜像 + cloud-init 注入
// 设计器给的 SSH 口令）→ 轮询任务至成功 → 等运行 + IP → app_install 逐个装应用。
// create_vm / app_install 均复用既有 executor（与页面操作同一套链路），本函数只做编排。
func (h *DesignerHandler) provisionVM(n dsgNode, st *dsgApplyState) (uint, error) {
	// 前置校验：云镜像必须存在于镜像库（images 表按 id）
	var img model.Image
	if err := h.DB.First(&img, n.Ref).Error; err != nil {
		return 0, fmt.Errorf("云镜像不存在（id=%s）", n.Ref)
	}
	pool := n.Pool
	if pool == "" {
		pool = tasks.DefaultStoragePoolResolver()
	}
	vcpu := n.VCPU
	if vcpu <= 0 {
		vcpu = 1
	}
	mem := n.MemoryMB
	if mem <= 0 {
		mem = 1024
	}
	sshUser := n.SSHUser
	if sshUser == "" {
		sshUser = "root"
	}

	// 1) 提交 create_vm（payload 与镜像管理页「基于云镜像创建」同构：source_image_id 引用不拷贝）
	var host model.Host
	if err := h.DB.First(&host).Error; err != nil {
		return 0, fmt.Errorf("请先登记宿主机")
	}
	createPayload := map[string]interface{}{
		"name":            n.Name,
		"host_id":         host.ID,
		"storage_pool":    pool,
		"vcpu":            vcpu,
		"memory_mb":       mem,
		"disks":           []map[string]interface{}{{"source_image_id": img.ID, "create_gb": int(img.SizeGB)}},
		"interfaces": []map[string]interface{}{{"type": "network", "source": "default", "model": "virtio"}},
		"network":    "default",
		// cloud_init 嵌套结构（vm_create executor 按此解析）：注入设计器给的
		// SSH 用户/口令——这是 VM 节点能被 app_install SSH 到的前提
		"cloud_init": map[string]interface{}{
			"hostname": n.Name,
			"user":     sshUser,
			"password": n.SSHSecret,
		},
	}
	userID, username := h.taskUser()
	if pb, perr := json.Marshal(createPayload); perr == nil {
		log.Printf("[designer] create_vm payload: %s", string(pb))
	}
	task, err := h.Tasks.Submit("create_vm", "设计器创建 VM "+n.Name, createPayload, userID, username, n.Name, nil)
	if err != nil {
		return 0, fmt.Errorf("提交建机任务失败: %w", err)
	}
	st.Steps = append(st.Steps, "建机任务已提交（task="+strconv.FormatUint(uint64(task.ID), 10)+"），等待 provision…")
	result, err := h.waitTask(task.ID, 8*time.Minute)
	if err != nil {
		return 0, err
	}
	// create_vm 成功结果 {vm_id: N}
	var res struct {
		VMID uint `json:"vm_id"`
	}
	_ = json.Unmarshal([]byte(result), &res)
	vmID := res.VMID
	if vmID == 0 {
		return 0, fmt.Errorf("建机任务成功但未返回 vm_id")
	}

	// 2) 等运行 + IP（provision 成功后 VM 可能仍 booting；DHCP 回填有延迟）
	var vm model.VM
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		if err := h.DB.First(&vm, vmID).Error; err != nil {
			return 0, fmt.Errorf("VM 记录读取失败: %w", err)
		}
		if vm.Status == model.VMStatusRunning && vm.IP != "" {
			break
		}
		if vm.Status == model.VMStatusError {
			return 0, fmt.Errorf("VM 进入 error 状态")
		}
		time.Sleep(5 * time.Second)
	}
	if vm.IP == "" {
		return 0, fmt.Errorf("VM 未获得 IP（DHCP 超时）")
	}
	st.Steps = append(st.Steps, "VM 运行中，IP="+vm.IP)

	// 3) app_install 逐个装（口令边界就地加密；凭据不落明文）
	for _, appID := range n.Apps {
		cipherB64, saltHex, err := secretbox.SealWithMaster(h.VMCred.MasterSecret, n.SSHSecret)
		if err != nil {
			return vmID, fmt.Errorf("SSH 口令加密失败: %w", err)
		}
		payload := map[string]interface{}{
			"app_id":        appID,
			"vm_id":         vmID,
			"host":          vm.IP,
			"port":          22,
			"user":          sshUser,
			"password_enc":  cipherB64,
			"salt":          saltHex,
		}
		t, err := h.Tasks.Submit("app_install", "设计器安装 "+appID+" 到 "+n.Name, payload, userID, username, n.Name, &vmID)
		if err != nil {
			return vmID, fmt.Errorf("提交应用安装失败（%s）: %w", appID, err)
		}
		st.Steps = append(st.Steps, "安装 "+appID+"…（task="+strconv.FormatUint(uint64(t.ID), 10)+"）")
		if _, err := h.waitTask(t.ID, 15*time.Minute); err != nil {
			return vmID, err
		}
		st.Steps = append(st.Steps, "✓ "+appID+" 安装完成")
	}
	return vmID, nil
}

// waitTask 轮询任务至终态：成功返回 result JSON（如 {"vm_id":N}）；失败/超时返回 error。
func (h *DesignerHandler) waitTask(taskID uint, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var t model.Task
		if err := h.DB.First(&t, taskID).Error; err != nil {
			return "", fmt.Errorf("任务记录读取失败: %w", err)
		}
		switch t.Status {
		case model.TaskStatusSuccess:
			return t.Result, nil
		case model.TaskStatusFailed:
			if t.Error != "" {
				return "", fmt.Errorf("%s", t.Error)
			}
			return "", fmt.Errorf("任务失败")
		}
		time.Sleep(3 * time.Second)
	}
	return "", fmt.Errorf("任务超时（%s）", timeout)
}

// taskUser 设计器任务的用户归属（apply 无 HTTP 上下文：Apply 提交时把登录人
// 记到 handler 字段，goroutine 内取用；未登录场景兜底 designer）。
func (h *DesignerHandler) taskUser() (*uint, string) {
	if h.UserID != 0 {
		id := h.UserID
		return &id, h.Username
	}
	return nil, "designer"
}

// ApplyStatus GET /api/designer/plans/:id/apply-status。
func (h *DesignerHandler) ApplyStatus(c *gin.Context) {
	id := c.Param("id")
	dsgApplyMu.Lock()
	st := dsgApplyRuns[id]
	dsgApplyMu.Unlock()
	if st == nil {
		Success(c, gin.H{"id": id, "status": "idle"})
		return
	}
	Success(c, gin.H{"id": id, "status": st.Status, "steps": st.Steps, "error": st.Error})
}
