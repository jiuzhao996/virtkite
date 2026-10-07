// designer.go：eNSP 式架构设计器（P2B v2，2026-10-04）——画布预览 + 节点编辑 +
// 预置模板 + 部署计划导出/一键应用。v2：VM 节点真落地（create_vm 建机 + 等 IP +
// app_install 逐个装应用，口令边界就地加密），容器栈继续 compose up。
//
// 计划存 data/designer/*.json（id/名称/节点/连线，**不含 SSH 口令**——写盘前强制
// 剥离，口令只随 Apply 请求体一次性携带）；应用 = 顺序落地计划内的容器栈与 VM 节点
// （goroutine + recover + 内存进度，GET status 轮询）。
package handler

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/dbx"
	"github.com/jiuzhao/vmops/service/dockerx"
	"github.com/jiuzhao/vmops/service/secretbox"
	"github.com/jiuzhao/vmops/service/tasks"
	"github.com/jiuzhao/vmops/service/virt"
	"gorm.io/gorm"
)

// DesignerHandler 架构设计器处理器。
type DesignerHandler struct {
	Docker   *dockerx.Dockerx
	Tasks    *tasks.Manager
	DB       *gorm.DB
	UserID   uint
	Username string
	// VMCred 凭据托管：app_install 的 use_saved 通道需要；MasterSecret 供设计器
	// 就地加密 VM 节点的 SSH 口令（与 AppsHandler 同口径，明文不落库）
	VMCred *VMCredentialHandler
	Virt   *virt.Virt // VM 开机与 DHCP 租约查询（provisionVM 自动开机 + 等 IP 自给自足）
}

// NewDesignerHandler 创建设计器处理器。
func NewDesignerHandler(db *gorm.DB, taskMgr *tasks.Manager, vmCred *VMCredentialHandler) *DesignerHandler {
	return &DesignerHandler{Docker: dockerx.New(), Tasks: taskMgr, DB: db, VMCred: vmCred, Virt: virt.New()}
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
	// Playbooks 落地后逐个执行的 playbook id（P4-S3 编排联动）：应用装的是服务，
	// playbook 做的是初始化/加固/优化，各司其职；同样走 ansible_run 任务管线
	Playbooks []string `json:"playbooks,omitempty"`
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
				{ID: "n2", Kind: "vm", Ref: "", Name: "应用机×N（装 filebeat）", X: 300, Y: 400, Note: "落地前在右侧选云镜像、规格与 SSH 口令；filebeat 属应用目录时可直接挂应用"},
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
	// 写盘前强制剥离 VM 节点口令：计划文件长期留存，明文口令只允许随 Apply
	// 请求体一次性携带（与「任务 payload 只存密文」同一口径）。前端表单里的
	// 口令框本就不进保存载荷，这里是服务端兜底——即使调用方带了也不落盘。
	for i := range p.Nodes {
		p.Nodes[i].SSHSecret = ""
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
			pool, user := n.Pool, n.SSHUser
			if pool == "" {
				pool = "-"
			}
			if user == "" {
				user = "root"
			}
			b = append(b, fmt.Sprintf("# VM 节点：name=%s image_id=%s pool=%s spec=%dC/%dMB ssh_user=%s apps=%s\n"+
				"# （SSH 口令不导出；一键落地时在页面填入）\n#provision_vms:\n#  - name: %s\n#    source_image_id: %s\n",
				n.Name, n.Ref, pool, n.VCPU, n.MemoryMB, user, strings.Join(n.Apps, ","), n.Name, n.Ref)...)
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
	TaskID  uint // 关联 tasks 表记录（AD1：任务中心可见 + 重启后状态可查）
	// ExpectedSteps 进度粗估分母（节点数×2 + 1），running 时按 len(Steps)/它折算
	ExpectedSteps int
}

var (
	dsgApplyMu   sync.Mutex
	dsgApplyRuns = map[string]*dsgApplyState{}
)

// Apply POST /api/designer/plans/:id/apply（admin）——顺序落地计划内的容器栈与
// VM 节点。VM 凭据随请求体一次性携带（不落计划文件）。
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
	// VM 凭据随本次请求体一次性携带（计划文件里没有）：按节点 id overlay。
	// 请求体可选——纯容器栈计划可以不带 body。
	var credBody struct {
		Credentials map[string]struct {
			SSHUser   string `json:"ssh_user"`
			SSHSecret string `json:"ssh_secret"`
		} `json:"credentials"`
	}
	if body, rerr := c.GetRawData(); rerr == nil && len(body) > 0 {
		if json.Unmarshal(body, &credBody) != nil {
			Fail(c, http.StatusBadRequest, "请求体格式非法（应为 {credentials:{节点id:{ssh_user,ssh_secret}}}）")
			return
		}
	}
	for i := range p.Nodes {
		if cred, ok := credBody.Credentials[p.Nodes[i].ID]; ok {
			if cred.SSHUser != "" {
				p.Nodes[i].SSHUser = cred.SSHUser
			}
			p.Nodes[i].SSHSecret = cred.SSHSecret
		}
	}

	dsgApplyMu.Lock()
	if st := dsgApplyRuns[id]; st != nil && st.Status == "running" {
		dsgApplyMu.Unlock()
		Fail(c, http.StatusConflict, "该计划正在应用中")
		return
	}
	st := &dsgApplyState{Status: "running", Started: time.Now()}
	// AD1：落地同时落 tasks 表（不排队，仅记录）——任务中心可见，服务重启后
	// apply-status 回退查这条记录，进度不再随进程丢失
	if h.DB != nil {
		pl, _ := json.Marshal(map[string]string{"plan_id": id})
		st.ExpectedSteps = len(p.Nodes)*2 + 1
		t := model.Task{Type: "designer_apply", Title: "落地架构 " + p.Name, Status: "running", Payload: string(pl)}
		if uid, name := h.taskUser(); uid != nil {
			t.UserID = uid
			t.Username = name
		}
		if err := h.DB.Create(&t).Error; err == nil {
			st.TaskID = t.ID
		}
	}
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
			// AD1：goroutine 收口时把最终状态同步进 tasks 表（正常/失败/panic 三路都走这）。
			// 不依赖前端轮询触发的懒同步——用户中途关页面也要有终态落库。
			h.syncApplyTask(st)
		}()
		// 前置守卫：配了应用或 playbook 的 VM 节点必须拿到口令（凭据随请求体来，
		// 计划文件里没有）——两类动作都要用口令（app 加密入 payload / playbook 托管
		// 后走凭据通道），否则建出空口令机器必然失败——fail-fast 比半途失败省资源
		for _, n := range plan.Nodes {
			if n.Kind == "vm" && (len(n.Apps) > 0 || len(n.Playbooks) > 0) && n.SSHSecret == "" {
				st.Status = "failed"
				st.Error = "VM「" + n.Name + "」配了应用/playbook 但未提供 SSH 口令（落地时需在页面填入）"
				return
			}
		}
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
			case "net":
				created, nerr := h.provisionNet(n)
				if nerr != nil {
					st.Status = "failed"
					st.Error = "网络 " + n.Name + " 落地失败：" + nerr.Error()
					return
				}
				if created {
					st.Steps = append(st.Steps, "✓ 网络 "+n.Name+" 已创建（NAT）")
				} else {
					st.Steps = append(st.Steps, "· 网络 "+n.Name+" 已存在，跳过")
				}
			default:
				st.Steps = append(st.Steps, "跳过 "+n.Name+"（未知节点类型）")
			}
		}
		st.Status = "success"
	}(p, st)

	Accepted(c, "计划应用已启动", gin.H{"id": id})
}

// provisionNet 网络节点落地（D3）：libvirt 已存在同名网络则跳过（不覆盖用户网络），
// 否则按默认 NAT 模板定义并启动。节点 ref 若形如网段（10.10.0.0/24），取其第一个
// 可用地址作网关（当前模板固定 /24 掩码，不支持更宽网段）。
func (h *DesignerHandler) provisionNet(n dsgNode) (bool, error) {
	if h.Virt == nil {
		return false, fmt.Errorf("虚拟化服务不可用")
	}
	if !validateVMName(n.Name) {
		return false, fmt.Errorf("网络名 %q 非法（字母数字与下划线/连字符）", n.Name)
	}
	if nets, err := h.Virt.ListNetworks(); err == nil {
		for _, e := range nets {
			if e.Name == n.Name {
				return false, nil // 已存在：不覆盖
			}
		}
	}
	xml := virt.NetworkXMLFromParams(n.Name, "", netGatewayFromCIDR(n.Ref))
	if xml == "" {
		return false, fmt.Errorf("生成网络定义失败")
	}
	if err := h.Virt.DefineNetwork(xml); err != nil {
		return false, err
	}
	return true, nil
}

// netGatewayFromCIDR 若是 CIDR（如 10.10.0.0/24）返回其第一个可用地址作网关，否则空串。
func netGatewayFromCIDR(s string) string {
	_, ipnet, err := net.ParseCIDR(strings.TrimSpace(s))
	if err != nil {
		return ""
	}
	ip := ipnet.IP.To4()
	if ip == nil {
		return ""
	}
	next := net.IPv4(ip[0], ip[1], ip[2], ip[3]+1)
	if !ipnet.Contains(next) {
		return ""
	}
	return next.String()
}

// provisionVM VM 节点落地（P2B v2）：create_vm 建机（云镜像 + cloud-init 注入
// 设计器给的 SSH 口令）→ 轮询任务至成功 → 自动开机 → 等 IP（自查 DHCP 租约）→
// app_install 逐个装应用。create_vm / app_install 均复用既有 executor（与页面
// 操作同一套链路），本函数只做编排。
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
	// cloud_init 嵌套结构（vm_create executor 按此解析）：注入设计器给的
	// SSH 用户/口令——这是 VM 节点能被 app_install SSH 到的前提。
	// 过底线校验（换行可注入 cloud-config；节点名/口令来自画布自由输入）
	ciSpec := &virt.CloudInitSpec{Hostname: n.Name, User: sshUser, Password: n.SSHSecret}
	if verr := validateCloudInitText(ciSpec); verr != nil {
		return 0, fmt.Errorf("VM 节点 %s 的 cloud-init 配置不合法: %w", n.Name, verr)
	}
	createPayload := map[string]interface{}{
		"name":         n.Name,
		"host_id":      host.ID,
		"storage_pool": pool,
		"vcpu":         vcpu,
		"memory_mb":    mem,
		"disks":        []map[string]interface{}{{"source_image_id": img.ID, "create_gb": int(img.SizeGB)}},
		"interfaces":   []map[string]interface{}{{"type": "network", "source": "default", "model": "virtio"}},
		"network":      "default",
		"cloud_init":   ciSpec,
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

	// 2) 自动开机（对应 virsh start <域名>）：create_vm 只 define 不 boot，
	// 不开机则后面的等 IP 循环必然空转到超时
	if err := h.Virt.StartDomain(n.Name); err != nil {
		return vmID, fmt.Errorf("开机失败: %w", err)
	}
	dbx.PersistBestEffort(h.DB, "designer 开机回写状态", func() error {
		return h.DB.Model(&model.VM{}).Where("id = ?", vmID).Update("status", model.VMStatusRunning).Error
	})
	st.Steps = append(st.Steps, "已开机，等待系统启动与 IP 分配…")

	// 3) 等运行 + IP。IP 自给自足：vms.ip 平时靠列表/详情请求惰性回填（syncVMIPs），
	// 设计器后台流程没人开页面，必须自己查 DHCP 租约按 MAC 匹配。
	// 双窗口 + 域活性自愈（v3 遗留收口）：首次启动竞态（cloud-init seed 就绪时机/
	// 磁盘链首次打开慢）可能让域中途停掉，DB 状态是我们乐观写入的反映不出来——
	// 每轮用 GetDomainState 探活，域已停则自动再开机（最多 2 次），每窗 3 分钟。
	var vm model.VM
	restarts := 0
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		if err := h.DB.First(&vm, vmID).Error; err != nil {
			return 0, fmt.Errorf("VM 记录读取失败: %w", err)
		}
		if state, serr := h.Virt.GetDomainState(n.Name); serr == nil && state == virt.StatusShutOff && restarts < 2 {
			restarts++
			log.Printf("[designer] VM %s(%d) 域已停止（启动竞态），自动重启第 %d 次", n.Name, vmID, restarts)
			st.Steps = append(st.Steps, "检测到域已停止（启动竞态），自动重启（第 "+strconv.Itoa(restarts)+" 次）…")
			if serr := h.Virt.StartDomain(n.Name); serr != nil {
				return vmID, fmt.Errorf("自动重启失败: %w", serr)
			}
			deadline = time.Now().Add(3 * time.Minute)
		}
		if vm.Status == model.VMStatusRunning && vm.IP == "" && vm.MACAddress != "" {
			if ip := h.leaseIPFor(vm.MACAddress); ip != "" {
				if err := h.DB.Model(&vm).Update("ip", ip).Error; err != nil {
					log.Printf("[designer] 回填 %s IP=%s 失败: %v", vm.Name, ip, err)
				}
				vm.IP = ip
			}
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
		return 0, fmt.Errorf("VM 未获得 IP（DHCP 超时，已自动重启 %d 次）", restarts)
	}
	st.Steps = append(st.Steps, "VM 运行中，IP="+vm.IP)

	// 3) 口令自动托管（P4-S3）：cloud-init 的口令如果不落 vm_credentials，后续
	// ansible_run（凭据通道）与凭据类功能（终端免密/文件管理）全部不可用
	if n.SSHSecret != "" {
		if err := h.VMCred.UpsertForVM(vmID, sshUser, n.SSHSecret); err != nil {
			// 托管失败不终止落地：应用安装走显式 password_enc 通道不受影响，但
			// playbook/凭据功能需要用户手工补（留痕 + 步骤可见）
			log.Printf("[designer] 警告: VM %s(%d) 凭据托管失败: %v", n.Name, vmID, err)
			st.Steps = append(st.Steps, "⚠ 凭据托管失败（playbook/凭据功能需手工补录）："+err.Error())
		} else {
			st.Steps = append(st.Steps, "✓ SSH 口令已自动托管（凭据功能可用）")
		}
	}

	// 4) app_install 逐个装（口令边界就地加密；凭据不落明文）
	for _, appID := range n.Apps {
		cipherB64, saltHex, err := secretbox.SealWithMaster(h.VMCred.MasterSecret, n.SSHSecret)
		if err != nil {
			return vmID, fmt.Errorf("SSH 口令加密失败: %w", err)
		}
		payload := map[string]interface{}{
			"app_id":       appID,
			"vm_id":        vmID,
			"host":         vm.IP,
			"port":         22,
			"user":         sshUser,
			"password_enc": cipherB64,
			"salt":         saltHex,
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

	// 5) playbook 逐个跑（P4-S3 编排联动）：复用 ansible_run 任务管线（凭据解析/
	// inventory 生成/RECAP 全在 executor），这里只编排。playbook 跑的是初始化/加固/
	// 优化类动作，与应用安装互补
	for _, pbID := range n.Playbooks {
		if !playbookIDRe.MatchString(pbID) {
			return vmID, fmt.Errorf("playbook ID 非法: %s", pbID)
		}
		if _, perr := os.Stat(filepath.Join("data", "ansible", "playbooks", pbID+".yml")); perr != nil {
			return vmID, fmt.Errorf("playbook 不存在: %s", pbID)
		}
		t, err := h.Tasks.Submit("ansible_run", "设计器 playbook "+pbID+" → "+n.Name,
			map[string]interface{}{"targets": []uint{vmID}, "playbook": pbID}, userID, username, n.Name, &vmID)
		if err != nil {
			return vmID, fmt.Errorf("提交 playbook 执行失败（%s）: %w", pbID, err)
		}
		st.Steps = append(st.Steps, "执行 playbook "+pbID+"…（task="+strconv.FormatUint(uint64(t.ID), 10)+"）")
		if _, err := h.waitTask(t.ID, 25*time.Minute); err != nil {
			return vmID, err
		}
		st.Steps = append(st.Steps, "✓ "+pbID+" 完成")
	}
	return vmID, nil
}

// leaseIPFor 从 libvirt DHCP 租约按 MAC 找 IP（与 VMHandler.syncVMIPs 同源；
// 设计器后台流程专用——IP 回填不能依赖有人正开着虚拟机列表页）。
func (h *DesignerHandler) leaseIPFor(mac string) string {
	leases, err := h.Virt.ListDHCPLeases()
	if err != nil {
		log.Printf("[designer] 查询 DHCP 租约失败: %v", err)
		return ""
	}
	mac = strings.ToLower(mac)
	for _, it := range leases {
		if strings.ToLower(it.MAC) == mac {
			return it.IP
		}
	}
	return ""
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

// syncApplyTask 把内存态同步进 tasks 表（AD1）。progress 按「步骤行数」粗估：
// 节点数×2 为满分母（每节点大约「开始+完成」两行），封顶 95 留终态写 100。
func (h *DesignerHandler) syncApplyTask(st *dsgApplyState) {
	if h.DB == nil || st.TaskID == 0 {
		return
	}
	vals := map[string]interface{}{
		"status": st.Status,
		"result": strings.Join(st.Steps, "\n"),
	}
	if st.Status == "success" {
		vals["progress"] = 100
	} else if st.Status == "failed" {
		vals["error"] = st.Error
	} else if st.ExpectedSteps > 0 {
		// 粗估进度封顶 95：终态 100 由成功分支写
		pct := len(st.Steps) * 95 / st.ExpectedSteps
		if pct > 95 {
			pct = 95
		}
		vals["progress"] = pct
	}
	if err := h.DB.Model(&model.Task{}).Where("id = ?", st.TaskID).Updates(vals).Error; err != nil {
		log.Printf("[designer] 同步落地任务 %d 失败: %v", st.TaskID, err)
	}
}

// ApplyStatus GET /api/designer/plans/:id/apply-status。
// 内存命中优先并懒同步进 tasks 表（轮询天然节流）；内存未命中（服务重启）回退
// 查 tasks 表最近一条该计划的 designer_apply 记录——重启后状态与步骤不再丢失。
func (h *DesignerHandler) ApplyStatus(c *gin.Context) {
	id := c.Param("id")
	dsgApplyMu.Lock()
	st := dsgApplyRuns[id]
	dsgApplyMu.Unlock()
	if st != nil {
		h.syncApplyTask(st)
		Success(c, gin.H{"id": id, "status": st.Status, "steps": st.Steps, "error": st.Error})
		return
	}
	// 回退：查该计划最近一条落地任务（payload 含 plan_id）
	var t model.Task
	q := h.DB.Where("type = ? AND payload LIKE ?", "designer_apply", `%"plan_id":"`+id+`"%`).
		Order("id DESC").First(&t)
	if q.Error != nil {
		Success(c, gin.H{"id": id, "status": "idle"})
		return
	}
	steps := []string{}
	if t.Result != "" {
		steps = strings.Split(t.Result, "\n")
	}
	// 任务中心视角的 cancelled/超时映射为 failed 语义（有 error 文案）
	status := t.Status
	errMsg := t.Error
	if status == "cancelled" {
		status = "failed"
		errMsg = errMsg + "（任务被取消）"
	}
	Success(c, gin.H{"id": id, "status": status, "steps": steps, "error": errMsg, "task_id": t.ID})
}
