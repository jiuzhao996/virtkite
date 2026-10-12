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
	"github.com/jiuzhao/vmops/config"
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
	Docker *dockerx.Dockerx
	Tasks  *tasks.Manager
	DB     *gorm.DB
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
	// DeployedAt 基线建立时间（RFC3339）：只由落地/收敛成功时的快照写入，计划文件里为空。
	// 它把「快照 = 已落地基线」的语义显式化——快照不是用户点「保存」时的版本，而是
	// 「上一次成功落地/收敛那一刻的期望」，三方对比里作为基线一侧（上次期望）。
	DeployedAt string `json:"deployed_at,omitempty"`
}

func designerDir() string { return config.DataPath("designer") }

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

// ── 快照 = 已落地基线（DE2）────────────────────────────────

// snapshotDir 快照目录：data/designer/snapshots/。<plan>.latest.json 是当前基线
// （上次成功落地/收敛时的期望——由落地器写，前端点「保存」不写）；<plan>-<unixts>.json
// 为历史存档（不可变，保留追溯）。
//
// 为什么基线必须与「期望」同源：三方对比要拿基线直接与 JSON 计划、与平台现实逐节点
// 比较。基线存的就是一份 dsgPlan（nodes+links），与计划文件结构完全一致，才能零转换 diff。
func snapshotDir() string { return config.DataPath("designer", "snapshots") }

// saveSnapshot 落地/收敛成功后覆盖基线：latest（当前基线）+ 带时间戳历史档。
// 只在 Apply/Reconcile 成功路径调用——这就是「快照语义 = 已落地基线」的落点。
//
// 写盘前强制剥离 VM 口令：Apply/Reconcile 的 plan 里带着请求体 overlay 进来的明文口令，
// 直接 Marshal 会把明文落到快照文件（与计划文件写盘前剥离同一口径的兜底）。
// 用副本再剥离，避免改动调用方共享的 Nodes 切片底层数组。
func (h *DesignerHandler) saveSnapshot(p dsgPlan) {
	snap := p
	snap.DeployedAt = time.Now().Format(time.RFC3339)
	snap.Nodes = make([]dsgNode, len(p.Nodes))
	copy(snap.Nodes, p.Nodes)
	for i := range snap.Nodes {
		snap.Nodes[i].SSHSecret = ""
	}
	raw, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		log.Printf("[designer] 快照序列化失败 plan=%s err=%v", p.ID, err)
		return
	}
	if err := os.MkdirAll(snapshotDir(), 0o755); err != nil {
		log.Printf("[designer] 快照目录创建失败 plan=%s err=%v", p.ID, err)
		return
	}
	ts := time.Now().Unix()
	hist := filepath.Join(snapshotDir(), fmt.Sprintf("%s-%d.json", p.ID, ts))
	latest := filepath.Join(snapshotDir(), p.ID+".latest.json")
	if err := os.WriteFile(hist, raw, 0o644); err != nil {
		log.Printf("[designer] 历史快照写入失败 plan=%s err=%v", p.ID, err)
	}
	if err := os.WriteFile(latest, raw, 0o644); err != nil {
		log.Printf("[designer] 基线快照写入失败 plan=%s err=%v", p.ID, err)
	}
}

// loadLatestSnapshot 读某计划的当前基线；无基线（从未落地）返回 nil。
func loadLatestSnapshot(planID string) *dsgPlan {
	raw, err := os.ReadFile(filepath.Join(snapshotDir(), planID+".latest.json"))
	if err != nil {
		return nil
	}
	var p dsgPlan
	if json.Unmarshal(raw, &p) != nil {
		return nil
	}
	return &p
}

// loadPlanFile 读计划文件（= 期望 / intended）；不存在或解析失败返回 nil（无画布）。
func loadPlanFile(planID string) *dsgPlan {
	raw, err := os.ReadFile(filepath.Join(designerDir(), planID+".json"))
	if err != nil {
		return nil
	}
	var p dsgPlan
	if json.Unmarshal(raw, &p) != nil {
		return nil
	}
	return &p
}

// 收敛动作枚举（对齐 containerlab 的 reconcile 动作集）。每个差异项都要带「为什么是
// 这个动作」的理由，而不是只给动作名——理由才是用户判断要不要收敛的依据。
const (
	dsgActionAdded     = "added"     // 期望有、现实无：创建
	dsgActionDeleted   = "deleted"   // 计划已移除、现实仍在（本计划创建）：下线
	dsgActionRecreated = "recreated" // 现实存在但规格与期望不符：重建（破坏性）
	dsgActionRestarted = "restarted" // 现实存在、规格相符但运行态异常：重启
	dsgActionNone      = "none"      // 无需动作（一致，或现实资源非本计划管理）
)

// 漂移侧别：区分「画布改了还没落地」与「现实被外部改了」——这两种漂移的处理方式
// 完全不同，必须在数据里分清，前端也要分组展示。
const (
	dsgSidePending  = "pending"  // 计划 vs 基线：用户改了画布但还没落地，收敛动作=落地
	dsgSideExternal = "external" // 基线 vs 现实：现实被外部改动，收敛动作=修复现实
)

// DriftItem 单条差异。三方对比的产物：
//   - Kind：差异分类（missing 计划/基线有现实无 | extra 现实有期望无 | mismatch 存在但规格/态不符）
//   - Action：收敛动作（containerlab 枚举），Reason 说明为什么是该动作
//   - Side：pending（画布改动未落地）| external（现实被外部改动）
type DriftItem struct {
	Kind     string `json:"kind"`               // missing | extra | mismatch
	Node     string `json:"node"`               // 资源名（VM 名 / 栈 id / 网络名）
	Name     string `json:"name,omitempty"`     // 展示名
	Resource string `json:"resource,omitempty"` // vm | container | net
	Action   string `json:"action"`             // added | deleted | recreated | restarted | none
	Reason   string `json:"reason"`             // 为什么是这个动作
	Side     string `json:"side"`               // pending | external
	Detail   string `json:"detail"`             // 人读描述
}

// DriftResult 一个计划的三方对比结果。
type DriftResult struct {
	PlanID    string      `json:"plan_id"`
	HasSnap   bool        `json:"has_snap"` // false=从未落地，无从谈漂移
	Drifted   bool        `json:"drifted"`
	Pending   int         `json:"pending"`  // 未落地改动条数（画布改了没落地）
	External  int         `json:"external"` // 外部漂移条数（现实被外部改了）
	Items     []DriftItem `json:"items"`
	CheckedAt time.Time   `json:"checked_at"`
}

// dsgDiff 单条三方对比结果：Item 给前端展示，Node 供收敛执行时定位要操作的资源。
type dsgDiff struct {
	Item DriftItem
	Node dsgNode // 要操作的计划/基线节点（deleted 用基线节点，其余用计划节点）
}

// dsgSlot 三方对比的逐资源槽位：同一资源键在 计划/基线/现实 三侧的存在与规格。
type dsgSlot struct {
	kind        string
	res         string
	plan        *dsgNode
	base        *dsgNode
	real        bool
	vm          model.VM
	stackStatus string
}

// dsgReality 平台现实（DB + docker + libvirt）——三方对比的「实际」一侧。
type dsgReality struct {
	VMs    map[string]model.VM // VM 名 → 记录
	Stacks map[string]string   // compose 项目名 → 状态串
	Nets   map[string]bool     // libvirt 网络名
}

// loadReality 采集平台现实。任一侧不可用（DB/Docker/Virt 为 nil 或调用失败）按空处理，
// 与旧口径一致：拿不到的现实不臆造，只报「现实为空」。
func (h *DesignerHandler) loadReality() dsgReality {
	r := dsgReality{VMs: map[string]model.VM{}, Stacks: map[string]string{}, Nets: map[string]bool{}}
	if h.DB != nil {
		var vms []model.VM
		if err := h.DB.Find(&vms).Error; err == nil {
			for _, v := range vms {
				r.VMs[v.Name] = v
			}
		}
	}
	if h.Docker != nil {
		if projects, err := h.Docker.ComposeList(); err == nil {
			for _, pr := range projects {
				r.Stacks[pr.Name] = pr.Status
			}
		}
	}
	if h.Virt != nil {
		if nets, err := h.Virt.ListNetworks(); err == nil {
			for _, n := range nets {
				r.Nets[n.Name] = true
			}
		}
	}
	return r
}

// nodeResource 节点在现实里的资源名：VM/网络用 Name，容器栈用 Ref（= compose 项目名）。
func nodeResource(n dsgNode) string {
	if n.Kind == "container" {
		return n.Ref
	}
	return n.Name
}

// nodeKey 资源唯一键：kind + 资源名（同名不同类型不混淆）。
func nodeKey(kind, res string) string { return kind + "\x00" + res }

// dsgKindLabel 资源类型的中文名（拼理由文案用）。
func dsgKindLabel(kind string) string {
	switch kind {
	case "vm":
		return "虚拟机"
	case "container":
		return "容器栈"
	case "net":
		return "网络"
	default:
		return "资源"
	}
}

// specDrift 判断「现实已存在且计划也期望」的资源是否发生规格/运行态漂移。
// 只对能拿到确凿规格的资源判定（VM 的 vcpu/内存、容器栈的运行态），拿不准不报，
// 避免把「用户主动关机」这类正常态误报成需要重启。
func specDrift(want dsgNode, s *dsgSlot) (action, reason string, ok bool) {
	switch want.Kind {
	case "vm":
		vcpu := want.VCPU
		if vcpu <= 0 {
			vcpu = 1
		}
		mem := want.MemoryMB
		if mem <= 0 {
			mem = 1024
		}
		if vcpu != s.vm.VCPU || mem != s.vm.MemoryMB {
			return dsgActionRecreated, fmt.Sprintf("规格与期望不符（期望 %dC/%dMB，现实 %dC/%dMB）：需重建（破坏性，会删除并重建该虚拟机）", vcpu, mem, s.vm.VCPU, s.vm.MemoryMB), true
		}
		// 仅 paused/error 视为需重启的异常态；shut off 是合法态（用户可能主动关机），不报
		if s.vm.Status == model.VMStatusPaused || s.vm.Status == model.VMStatusError {
			return dsgActionRestarted, "虚拟机处于异常状态（" + s.vm.Status + "）：需重启恢复", true
		}
	case "container":
		if s.stackStatus != "" && !strings.Contains(s.stackStatus, "running") {
			return dsgActionRestarted, "容器栈未在运行（" + s.stackStatus + "）：需重启", true
		}
	}
	return "", "", false
}

// mkDiff 组装一条差异项（Node 由调用方按场景补齐）。
func mkDiff(s *dsgSlot, action, side, kind, reason string) *dsgDiff {
	return &dsgDiff{
		Item: DriftItem{
			Kind: kind, Node: s.res, Name: s.res, Resource: s.kind,
			Action: action, Reason: reason, Side: side, Detail: reason,
		},
	}
}

// computeDiff 三方对比：JSON 计划(期望) / 基线(上次期望) / 现实(DB+libvirt)。
//
// 为什么三方：只看「计划 vs 现实」会把两种成因混为一谈——
//   - 画布新增/删除但还没落地（side=pending）：收敛动作是「落地」；
//   - 现实被外部改了（side=external）：收敛动作是「修复现实」。
//
// 二者对用户的操作完全不同，故逐项标注 Side。
//
// plan 为 nil（计划文件缺失）时按 plan=baseline 处理——没有画布时「期望」即上次落地的
// 基线，这样纯外部漂移仍能检出，也兼容无画布的历史快照（旧测试即此路径）。
func computeDiff(plan, baseline *dsgPlan, reality dsgReality) []dsgDiff {
	if baseline == nil {
		return nil
	}
	if plan == nil {
		plan = baseline
	}
	slots := map[string]*dsgSlot{}
	get := func(kind, res string) *dsgSlot {
		k := nodeKey(kind, res)
		s := slots[k]
		if s == nil {
			s = &dsgSlot{kind: kind, res: res}
			slots[k] = s
		}
		return s
	}
	for i := range plan.Nodes {
		n := plan.Nodes[i]
		nn := n
		get(n.Kind, nodeResource(n)).plan = &nn
	}
	for i := range baseline.Nodes {
		n := baseline.Nodes[i]
		nn := n
		get(n.Kind, nodeResource(n)).base = &nn
	}
	for name, vm := range reality.VMs {
		s := get("vm", name)
		s.real = true
		s.vm = vm
	}
	for name, status := range reality.Stacks {
		s := get("container", name)
		s.real = true
		s.stackStatus = status
	}
	for name := range reality.Nets {
		get("net", name).real = true
	}

	keys := make([]string, 0, len(slots))
	for k := range slots {
		keys = append(keys, k)
	}
	sort.Strings(keys) // 稳定输出顺序（map 迭代无序）

	out := []dsgDiff{}
	for _, k := range keys {
		s := slots[k]
		p, b := s.plan, s.base
		var d *dsgDiff
		switch {
		case p != nil && !s.real:
			// 期望有、现实无
			if b == nil {
				d = mkDiff(s, dsgActionAdded, dsgSidePending, "missing", "画布新增，尚未落地：将创建"+dsgKindLabel(s.kind))
			} else {
				d = mkDiff(s, dsgActionAdded, dsgSideExternal, "missing", "已落地资源在现实中不存在（被外部删除）：需重建")
			}
			d.Node = *p
		case p != nil && s.real:
			// 期望有、现实也有
			if b == nil {
				d = mkDiff(s, dsgActionNone, dsgSidePending, "mismatch", "画布新增但现实已有同名资源：落地将复用/跳过（VM 同名会失败）")
				d.Node = *p
			} else if act, reason, ok := specDrift(*p, s); ok {
				d = mkDiff(s, act, dsgSideExternal, "mismatch", reason)
				d.Node = *p
			}
		case p == nil && b != nil && s.real:
			// 画布已移除该节点，但现实仍在：本计划创建的资源，收敛=下线
			d = mkDiff(s, dsgActionDeleted, dsgSidePending, "extra", "画布已移除该节点，现实仍在：收敛将下线"+dsgKindLabel(s.kind))
			d.Node = *b
		case p == nil && b == nil && s.real:
			// 现实有、计划/基线都没有：非本计划管理，收敛忽略（不做无差别删除）
			d = mkDiff(s, dsgActionNone, dsgSideExternal, "extra", "现实存在但非本计划创建/管理：收敛忽略（可用「从现状导入」纳入画布）")
		}
		// 其余分支（p==nil && b!=nil && !s.real：画布已删且现实已无）净效果已一致，不报
		if d != nil {
			out = append(out, *d)
		}
	}
	return out
}

// ComputeDrift 三方对比入口：读计划(期望)、基线(上次期望)、现实，逐资源产出差异项。
// h.DB/h.Docker/h.Virt 为空时按空现实处理。
func (h *DesignerHandler) ComputeDrift(planID string) DriftResult {
	res := DriftResult{PlanID: planID, HasSnap: false, Items: []DriftItem{}, CheckedAt: time.Now()}
	baseline := loadLatestSnapshot(planID)
	if baseline == nil {
		return res
	}
	res.HasSnap = true
	plan := loadPlanFile(planID) // 期望；无画布时为 nil（按 plan=baseline 处理）
	for _, d := range computeDiff(plan, baseline, h.loadReality()) {
		res.Items = append(res.Items, d.Item)
		switch d.Item.Side {
		case dsgSidePending:
			res.Pending++
		case dsgSideExternal:
			res.External++
		}
	}
	res.Drifted = len(res.Items) > 0
	return res
}

// ReconcileAction 一条收敛动作（收敛预演的列表项 + 执行依据）。
type ReconcileAction struct {
	Action string `json:"action"` // added | deleted | recreated | restarted
	Node   string `json:"node"`   // 资源名
	Name   string `json:"name,omitempty"`
	Kind   string `json:"kind"` // vm | container | net
	Side   string `json:"side"` // pending | external
	Reason string `json:"reason"`
}

// ReconcileResult 收敛计划：先列现状 → 算出需要 added/deleted/recreated/restarted 的集合。
type ReconcileResult struct {
	PlanID    string            `json:"plan_id"`
	HasSnap   bool              `json:"has_snap"` // false=从未落地，收敛退化为全量落地
	Actions   []ReconcileAction `json:"actions"`
	Counts    map[string]int    `json:"counts"` // 动作 → 条数
	CheckedAt time.Time         `json:"checked_at"`
}

// reconcilePlan 由三方对比算出收敛动作集合与可执行 ops。
//   - baseline 为 nil（从未落地）：退化为全量落地，所有节点视作 added；
//   - 否则逐条取 computeDiff 里 action != none 的项。VMID 已在 computeDiff 里带出。
func reconcilePlan(plan, baseline *dsgPlan, reality dsgReality, planID string) (ReconcileResult, []dsgOp) {
	res := ReconcileResult{PlanID: planID, HasSnap: baseline != nil, Actions: []ReconcileAction{}, Counts: map[string]int{}, CheckedAt: time.Now()}
	ops := []dsgOp{}
	if plan == nil {
		return res, ops
	}
	if baseline == nil {
		for _, n := range orderNodes(plan.Nodes, plan.Links) {
			reason := "首次落地：创建" + dsgKindLabel(n.Kind)
			ops = append(ops, dsgOp{Action: dsgActionAdded, Node: n, Reason: reason})
			res.Actions = append(res.Actions, ReconcileAction{Action: dsgActionAdded, Node: nodeResource(n), Name: n.Name, Kind: n.Kind, Side: dsgSidePending, Reason: reason})
			res.Counts[dsgActionAdded]++
		}
		return res, ops
	}
	for _, d := range computeDiff(plan, baseline, reality) {
		if d.Item.Action == "" || d.Item.Action == dsgActionNone {
			continue
		}
		ops = append(ops, dsgOp{Action: d.Item.Action, Node: d.Node, Reason: d.Item.Reason})
		res.Actions = append(res.Actions, ReconcileAction{Action: d.Item.Action, Node: d.Item.Node, Name: d.Item.Name, Kind: d.Item.Resource, Side: d.Item.Side, Reason: d.Item.Reason})
		res.Counts[d.Item.Action]++
	}
	return res, ops
}

// computeReconcile 从磁盘计划读期望并算出收敛集合（收敛预演用）。
func (h *DesignerHandler) computeReconcile(planID string) (ReconcileResult, []dsgOp) {
	return reconcilePlan(loadPlanFile(planID), loadLatestSnapshot(planID), h.loadReality(), planID)
}

// DriftSummary 遍历全部快照逐计划对比，汇总给人读摘要（DE2 的 cron 巡检入口）。
// 只巡检有 latest 快照的计划（从未落地谈不上漂移）。
func (h *DesignerHandler) DriftSummary() (checked int, drifted int, summary string) {
	entries, err := os.ReadDir(snapshotDir())
	if err != nil {
		return 0, 0, "（暂无快照：还没有成功落地过任何计划）"
	}
	ids := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".latest.json") {
			ids[strings.TrimSuffix(name, ".latest.json")] = true
		}
	}
	var b strings.Builder
	for id := range ids {
		checked++
		d := h.ComputeDrift(id)
		if !d.Drifted {
			continue
		}
		drifted++
		b.WriteString("计划 " + id + "：" + strconv.Itoa(len(d.Items)) + " 处漂移\n")
		for i, it := range d.Items {
			if i >= 5 {
				b.WriteString("  …（其余 " + strconv.Itoa(len(d.Items)-5) + " 处省略）\n")
				break
			}
			b.WriteString("  - [" + it.Kind + "] " + it.Node + "：" + it.Detail + "\n")
		}
	}
	if drifted == 0 {
		return checked, 0, "全部 " + strconv.Itoa(checked) + " 个已落地计划与现实一致"
	}
	return checked, drifted, b.String()
}

// Drift GET /api/designer/plans/:id/drift——计划与现实的漂移对比（DE2）。
func (h *DesignerHandler) Drift(c *gin.Context) {
	id := c.Param("id")
	if !dsgIDRe.MatchString(id) {
		Fail(c, http.StatusBadRequest, "计划 ID 非法")
		return
	}
	Success(c, h.ComputeDrift(id))
}

// ExportAnsible GET /api/designer/plans/:id/export-ansible（DE1）——画布 → Ansible：
// 按网络连线生成 inventory（组=网络名），生成 site.yml 骨架（每 VM 一个 play，
// 引用的 playbook 以注释列出）。site.yml 直接落 data/ansible/playbooks/（playbook
// 库自动可见可执行）；inventory 存 data/ansible/inventory/ 供人工 -i 使用。
// 图→代码是开源空档（Brainboard 收费闭源）：画布 JSON 即 SoT，此端点即渲染器。
func (h *DesignerHandler) ExportAnsible(c *gin.Context) {
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
	if !playbookIDRe.MatchString(id) {
		Fail(c, http.StatusBadRequest, "计划 ID 不能用作 playbook 名（字母数字与 -_，字母开头）")
		return
	}

	// 分组：VM → 所连网络（连线两端任一为 net 节点即归组）；无网络连线归 ungrouped
	nodeByID := map[string]dsgNode{}
	for _, n := range p.Nodes {
		nodeByID[n.ID] = n
	}
	groupOf := map[string]string{} // vm 节点 id → 组名（网络名）
	for _, l := range p.Links {
		a, b := nodeByID[l.From], nodeByID[l.To]
		if a.Kind == "net" && b.Kind == "vm" {
			groupOf[l.To] = a.Name
		} else if b.Kind == "net" && a.Kind == "vm" {
			groupOf[l.From] = b.Name
		}
	}

	// inventory.yml
	inv := &strings.Builder{}
	inv.WriteString("# VirtKite 设计器导出 inventory（计划: " + p.Name + "）\n")
	inv.WriteString("# 组 = 画布网络连线；host_vars 来自节点属性\n")
	inv.WriteString("all:\n  children:\n")
	groups := map[string][]dsgNode{}
	var groupOrder []string
	for _, n := range p.Nodes {
		if n.Kind != "vm" {
			continue
		}
		g := groupOf[n.ID]
		if g == "" {
			g = "ungrouped"
		}
		if _, ok := groups[g]; !ok {
			groupOrder = append(groupOrder, g)
		}
		groups[g] = append(groups[g], n)
	}
	if len(groupOrder) == 0 {
		inv.WriteString("    ungrouped:\n      hosts: {}\n")
	}
	for _, g := range groupOrder {
		inv.WriteString("    " + g + ":\n      hosts:\n")
		for _, n := range groups[g] {
			user := n.SSHUser
			if user == "" {
				user = "root"
			}
			inv.WriteString("        " + n.Name + ":\n")
			inv.WriteString("          ansible_user: " + user + "\n")
		}
	}
	invBytes := []byte(inv.String())

	// site.yml（playbook 库识别的元数据头 + 每 VM 一个 play）
	site := &strings.Builder{}
	site.WriteString("# vmops-playbook: name=" + id + "-site | desc=设计器导出：计划「" + p.Name + "」 | targets=linux\n")
	site.WriteString("# 由架构设计器生成（画布 → Ansible，DE1）；重复导出覆盖本文件\n")
	site.WriteString("---\n")
	for _, g := range groupOrder {
		for _, n := range groups[g] {
			site.WriteString("\n# VM: " + n.Name + "（组 " + g + "，规格 " + strconv.Itoa(n.VCPU) + "C/" + strconv.Itoa(n.MemoryMB) + "MB）\n")
			if len(n.Apps) > 0 {
				site.WriteString("# 落地时装的应用: " + strings.Join(n.Apps, ", ") + "\n")
			}
			site.WriteString("- name: 配置 " + n.Name + "\n")
			site.WriteString("  hosts: " + n.Name + "\n")
			site.WriteString("  become: true\n")
			site.WriteString("  gather_facts: true\n")
			site.WriteString("  tasks:\n")
			site.WriteString("    - name: 占位任务（按需补充——设计器导出骨架）\n")
			site.WriteString("      ansible.builtin.debug:\n")
			site.WriteString("        msg: \"" + n.Name + " 已就绪（" + strconv.Itoa(n.VCPU) + "C/" + strconv.Itoa(n.MemoryMB) + "MB）\"\n")
		}
	}
	if len(groupOrder) == 0 {
		site.WriteString("# 计划中没有 VM 节点（纯容器栈/网络）\n")
	}
	site.WriteString("\n# 节点引用的 playbook（在「运维自动化 → Playbook 库」对目标执行）：\n")
	seenPB := map[string]bool{}
	for _, n := range p.Nodes {
		for _, pb := range n.Playbooks {
			if !seenPB[pb] {
				seenPB[pb] = true
				site.WriteString("#   - " + pb + "\n")
			}
		}
	}
	if len(seenPB) == 0 {
		site.WriteString("#   （无）\n")
	}
	siteBytes := []byte(site.String())

	// 落盘：site.yml 进 playbook 库目录（立即出现在自动化页）；inventory 存档目录
	if err := os.MkdirAll(config.DataPath("ansible", "playbooks"), 0o755); err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "创建 playbook 目录失败", err)
		return
	}
	if err := os.MkdirAll(config.DataPath("ansible", "inventory"), 0o755); err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "创建 inventory 目录失败", err)
		return
	}
	sitePath := config.DataPath("ansible", "playbooks", id+"-site.yml")
	if err := os.WriteFile(sitePath, siteBytes, 0o644); err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "写入 site.yml 失败", err)
		return
	}
	invPath := config.DataPath("ansible", "inventory", id+".yml")
	if err := os.WriteFile(invPath, invBytes, 0o644); err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "写入 inventory 失败", err)
		return
	}
	Success(c, gin.H{
		"message":        "已导出",
		"playbook_id":    id + "-site",
		"inventory":      string(invBytes),
		"site":           string(siteBytes),
		"inventory_path": invPath,
	})
}

// ── 落地 / 收敛（M3：容器栈与 VM 实际落地；DE3：按差异收敛）──────────

// dsgApplyState 一次落地/收敛的运行态（内存 + tasks 表双写）。
type dsgApplyState struct {
	Status  string   `json:"status"` // running | success | failed
	Steps   []string `json:"steps"`
	Error   string   `json:"error,omitempty"`
	Mode    string   `json:"mode,omitempty"` // apply（全量落地）| reconcile（按差异收敛）
	Started time.Time
	TaskID  uint // 关联 tasks 表记录（AD1：任务中心可见 + 重启后状态可查）
	// ExpectedSteps 进度粗估分母（操作数×2 + 1），running 时按 len(Steps)/它折算
	ExpectedSteps int
}

var (
	dsgApplyMu   sync.Mutex
	dsgApplyRuns = map[string]*dsgApplyState{}
)

// dsgOp 一个落地/收敛操作单元。Action 用 containerlab 枚举；Node 是期望节点；
// Reason 是「为什么做这个动作」的人读理由（进步骤日志与任务结果）。
type dsgOp struct {
	Action string
	Node   dsgNode
	Reason string
}

// loadPlanForApply 读计划文件并把请求体里的 VM 口令按节点 id overlay 上去（不落盘）。
// 请求体可选（纯容器栈计划可无 body）。失败时已写好响应，返回 ok=false。
func (h *DesignerHandler) loadPlanForApply(c *gin.Context, id string) (dsgPlan, bool) {
	raw, err := os.ReadFile(filepath.Join(designerDir(), id+".json"))
	if err != nil {
		Fail(c, http.StatusNotFound, "计划不存在")
		return dsgPlan{}, false
	}
	var p dsgPlan
	if json.Unmarshal(raw, &p) != nil {
		Fail(c, http.StatusInternalServerError, "计划解析失败")
		return dsgPlan{}, false
	}
	var credBody struct {
		Credentials map[string]struct {
			SSHUser   string `json:"ssh_user"`
			SSHSecret string `json:"ssh_secret"`
		} `json:"credentials"`
	}
	if body, rerr := c.GetRawData(); rerr == nil && len(body) > 0 {
		if json.Unmarshal(body, &credBody) != nil {
			Fail(c, http.StatusBadRequest, "请求体格式非法（应为 {credentials:{节点id:{ssh_user,ssh_secret}}}）")
			return dsgPlan{}, false
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
	return p, true
}

// startRun 落地/收敛的公共入口：登记运行态（内存 + tasks 表）后起 goroutine 执行 ops。
// 同一计划已有 running 任务时返回 409（apply 与 reconcile 共用一把运行位，互斥）。
// 用户归属在提交时从 HTTP 上下文取定并传进 goroutine，不在共享 handler 上回写字段
// （避免两个计划并发落地时对 h.UserID 的数据竞争）。goroutine 自带 recover（AGENTS 并发规范）。
func (h *DesignerHandler) startRun(c *gin.Context, p dsgPlan, ops []dsgOp, taskType, title, mode string) {
	id := p.ID
	userID, username := taskUserFromContext(c)
	if username == "" {
		username = "designer"
	}
	dsgApplyMu.Lock()
	if st := dsgApplyRuns[id]; st != nil && st.Status == "running" {
		dsgApplyMu.Unlock()
		Fail(c, http.StatusConflict, "该计划正在落地/收敛中")
		return
	}
	st := &dsgApplyState{Status: "running", Mode: mode, Started: time.Now(), ExpectedSteps: len(ops)*2 + 1}
	// AD1：落地同时落 tasks 表（不排队，仅记录）——任务中心可见，服务重启后
	// apply-status 回退查这条记录，进度不再随进程丢失
	if h.DB != nil {
		pl, _ := json.Marshal(map[string]string{"plan_id": id})
		t := model.Task{Type: taskType, Title: title, Status: "running", Payload: string(pl), UserID: userID, Username: username}
		if err := h.DB.Create(&t).Error; err == nil {
			st.TaskID = t.ID
		}
	}
	dsgApplyRuns[id] = st
	dsgApplyMu.Unlock()

	go func(plan dsgPlan, ops []dsgOp, st *dsgApplyState) {
		defer func() {
			if r := recover(); r != nil {
				st.Status = "failed"
				st.Error = "应用协程异常（详见服务日志）"
				log.Printf("[designer] 计划 %s %s panic: %v\n%s", plan.ID, st.Mode, r, debug.Stack())
			}
			// AD1：goroutine 收口时把最终状态同步进 tasks 表（正常/失败/panic 三路都走这）。
			// 不依赖前端轮询触发的懒同步——用户中途关页面也要有终态落库。
			h.syncApplyTask(st)
		}()
		h.runOps(&plan, ops, st, userID, username)
	}(p, ops, st)

	Accepted(c, title+"已启动", gin.H{"id": id, "mode": mode, "actions": len(ops)})
}

// runOps 执行操作序列：先删除（释放资源、为重建腾位）→ 再新增（按依赖排序）→ 最后重启。
//
// 幂等/可重入：任何一步失败即置 failed 并返回，已完成的步骤与内存态保留，前端可重试。
// 重试时收敛会重新 computeDiff——已完成的 added 不再出现在差异里，deleted 的资源现实
// 已无也不再出现，天然幂等；recreated 的「先删后建」中途失败，重试时会看到「现实已无 →
// added」，等价于继续把建做下去。
func (h *DesignerHandler) runOps(plan *dsgPlan, ops []dsgOp, st *dsgApplyState, userID *uint, username string) {
	// 前置守卫：要落地（非删除/重启）的 VM 节点若配了应用/playbook，必须有口令——
	// 两类动作都要用口令（app 加密入 payload / playbook 托管后走凭据通道），否则建出
	// 空口令机器必然失败。fail-fast 比半途失败省资源。
	for _, op := range ops {
		n := op.Node
		if n.Kind == "vm" && op.Action != dsgActionDeleted && op.Action != dsgActionRestarted &&
			(len(n.Apps) > 0 || len(n.Playbooks) > 0) && n.SSHSecret == "" {
			st.Status = "failed"
			st.Error = "VM「" + n.Name + "」配了应用/playbook 但未提供 SSH 口令（落地时需在页面填入）"
			return
		}
	}

	var dels, adds, restarts []dsgOp
	for _, op := range ops {
		switch op.Action {
		case dsgActionDeleted:
			dels = append(dels, op)
		case dsgActionRecreated:
			dels = append(dels, op) // 重建 = 先删
			adds = append(adds, op) // 再建
		case dsgActionAdded:
			adds = append(adds, op)
		case dsgActionRestarted:
			restarts = append(restarts, op)
		}
	}

	// 1) 删除
	for _, op := range dels {
		st.Steps = append(st.Steps, "下线 "+nodeResource(op.Node)+"（"+dsgKindLabel(op.Node.Kind)+"）："+op.Reason)
		if err := h.teardownNode(op, st, userID, username); err != nil {
			st.Status = "failed"
			st.Error = "下线 " + nodeResource(op.Node) + " 失败：" + err.Error()
			return
		}
	}

	// 2) 新增（按依赖排序：net → vm → container）
	addNodes := make([]dsgNode, 0, len(adds))
	for _, op := range adds {
		addNodes = append(addNodes, op.Node)
	}
	for _, n := range orderNodes(addNodes, plan.Links) {
		if err := h.provisionNode(plan, n, st, userID, username); err != nil {
			st.Status = "failed"
			st.Error = dsgKindLabel(n.Kind) + " " + nodeResource(n) + " 落地失败：" + err.Error()
			return
		}
	}

	// 3) 重启
	for _, op := range restarts {
		st.Steps = append(st.Steps, "重启 "+nodeResource(op.Node)+"："+op.Reason)
		if err := h.restartNode(op, st); err != nil {
			st.Status = "failed"
			st.Error = "重启 " + nodeResource(op.Node) + " 失败：" + err.Error()
			return
		}
	}

	st.Status = "success"
	// DE2：成功即覆盖基线——「已落地基线」= 本次落地/收敛时的期望。下次三方对比的
	// 基线一侧即此；手改画布（未落地）不会动它。
	h.saveSnapshot(*plan)
}

// provisionNode 落地单个节点（net/vm/container）。原 Apply 内联循环抽出，供全量落地
// 与按差异收敛共用；本身对已存在的网络幂等（provisionNet 同名跳过）。
func (h *DesignerHandler) provisionNode(p *dsgPlan, n dsgNode, st *dsgApplyState, userID *uint, username string) error {
	switch n.Kind {
	case "container":
		st.Steps = append(st.Steps, "部署栈 "+n.Ref+"（"+n.Name+"）…")
		meta, sraw, err := parseStackMeta(filepath.Join(stacksDir(), n.Ref+".yml"))
		if err != nil {
			return fmt.Errorf("栈不存在：%s", n.Ref)
		}
		target := config.DataPath("stacks", n.Ref)
		if err := os.MkdirAll(target, 0o755); err != nil {
			return fmt.Errorf("创建部署目录失败: %w", err)
		}
		_ = os.WriteFile(filepath.Join(target, "docker-compose.yml"), sraw, 0o644)
		if err := h.Docker.ComposeUpFromFile(target); err != nil {
			return fmt.Errorf("栈 %s 部署失败: %w", n.Ref, err)
		}
		st.Steps = append(st.Steps, "✓ "+n.Ref+" 完成（"+meta.Desc+"）")
	case "vm":
		vmID, err := h.provisionVM(p, n, st, userID, username)
		if err != nil {
			return err
		}
		st.Steps = append(st.Steps, "✓ VM "+n.Name+" 就绪（id="+strconv.FormatUint(uint64(vmID), 10)+"）")
	case "net":
		created, nerr := h.provisionNet(n)
		if nerr != nil {
			return nerr
		}
		if created {
			st.Steps = append(st.Steps, "✓ 网络 "+n.Name+" 已创建（NAT）")
		} else {
			st.Steps = append(st.Steps, "· 网络 "+n.Name+" 已存在，跳过")
		}
	default:
		st.Steps = append(st.Steps, "跳过 "+n.Name+"（未知节点类型）")
	}
	return nil
}

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

// Apply POST /api/designer/plans/:id/apply（admin）——全量落地：把计划内所有节点按依赖
// 顺序落地（net→vm→container）。VM 凭据随请求体一次性携带（不落计划文件）。
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
	p, ok := h.loadPlanForApply(c, id)
	if !ok {
		return
	}
	// 全量落地 = 每个节点都当作「需要落地」（provisionNet 对已存在网络幂等跳过）
	ops := make([]dsgOp, 0, len(p.Nodes))
	for _, n := range p.Nodes {
		ops = append(ops, dsgOp{Action: dsgActionAdded, Node: n, Reason: "全量落地"})
	}
	h.startRun(c, p, ops, "designer_apply", "落地架构 "+p.Name, "apply")
}

// Reconcile POST /api/designer/plans/:id/reconcile（admin）——按差异收敛：先算三方差异，
// 只对 added/deleted/recreated/restarted 的资源执行，不动未变化的资源（deploy 即 reconcile，
// 不是每次全量重建）。收敛走与落地同一套异步任务管线与运行位（apply/reconcile 互斥）；
// 失败保留状态可重试。
func (h *DesignerHandler) Reconcile(c *gin.Context) {
	if !roleIsAdmin(c) {
		Fail(c, http.StatusForbidden, "按差异收敛仅管理员可用")
		return
	}
	id := c.Param("id")
	if !dsgIDRe.MatchString(id) {
		Fail(c, http.StatusBadRequest, "计划 ID 非法")
		return
	}
	p, ok := h.loadPlanForApply(c, id)
	if !ok {
		return
	}
	// 用带凭据的计划重算差异（凭据来自请求体，磁盘计划里没有）
	_, ops := reconcilePlan(&p, loadLatestSnapshot(id), h.loadReality(), id)
	if len(ops) == 0 {
		Success(c, gin.H{"message": "已与现实一致，无需收敛", "actions": 0})
		return
	}
	h.startRun(c, p, ops, "designer_reconcile", "收敛架构 "+p.Name, "reconcile")
}

// ReconcilePlan GET /api/designer/plans/:id/reconcile-plan（operator+）——收敛预演：
// 不执行，只返回「先列现状 → 算出的动作集合」（containerlab planApply 的 plan 阶段）。
func (h *DesignerHandler) ReconcilePlan(c *gin.Context) {
	id := c.Param("id")
	if !dsgIDRe.MatchString(id) {
		Fail(c, http.StatusBadRequest, "计划 ID 非法")
		return
	}
	res, _ := h.computeReconcile(id)
	Success(c, res)
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

// orderNodes 落地顺序：按 plan.Links 做拓扑排序（to 依赖 from），入度 0 中按 kind 优先级
// net(0) → vm(1) → container(2) 挑选；有环或链接不足时退化为 kind 优先级稳定排序。
// 目标：net 先建（VM/容器要接入），vm 先于依赖它的 container。
func orderNodes(nodes []dsgNode, links []dsgLink) []dsgNode {
	priority := func(kind string) int {
		switch kind {
		case "net":
			return 0
		case "vm":
			return 1
		case "container":
			return 2
		default:
			return 3
		}
	}
	ids := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		ids[n.ID] = true
	}
	// dep[to] = 依赖的 from 集合；indeg[to] = 未满足的依赖数
	dep := make(map[string][]string)
	indeg := make(map[string]int, len(nodes))
	for _, n := range nodes {
		indeg[n.ID] = 0
	}
	for _, l := range links {
		if l.From == l.To || !ids[l.From] || !ids[l.To] {
			continue
		}
		dep[l.To] = append(dep[l.To], l.From)
		indeg[l.To]++
	}
	remaining := append([]dsgNode(nil), nodes...)
	out := make([]dsgNode, 0, len(nodes))
	for len(remaining) > 0 {
		best := -1
		for i, n := range remaining {
			if indeg[n.ID] == 0 && (best == -1 || priority(n.Kind) < priority(remaining[best].Kind)) {
				best = i
			}
		}
		if best == -1 {
			// 有环：剩余节点退化为 kind 优先级稳定排序后收尾
			rest := append([]dsgNode(nil), remaining...)
			sort.SliceStable(rest, func(i, j int) bool { return priority(rest[i].Kind) < priority(rest[j].Kind) })
			out = append(out, rest...)
			break
		}
		chosen := remaining[best]
		out = append(out, chosen)
		remaining = append(remaining[:best], remaining[best+1:]...)
		for _, n := range remaining {
			for _, f := range dep[n.ID] {
				if f == chosen.ID {
					indeg[n.ID]--
				}
			}
		}
	}
	return out
}

// resolveVMNetwork 找与本 VM 相连的 net 节点，返回其节点名（= libvirt 网络名，provisionNet 即按此建网）；
// 无 net 连线时回落 "default"。
func resolveVMNetwork(p *dsgPlan, n dsgNode) string {
	if p == nil {
		return "default"
	}
	netName := make(map[string]string)
	for _, nd := range p.Nodes {
		if nd.Kind == "net" && nd.Name != "" {
			netName[nd.ID] = nd.Name
		}
	}
	for _, l := range p.Links {
		if l.From == n.ID {
			if name, ok := netName[l.To]; ok {
				return name
			}
		}
		if l.To == n.ID {
			if name, ok := netName[l.From]; ok {
				return name
			}
		}
	}
	return "default"
}

// provisionVM VM 节点落地：create_vm 建机（云镜像 + cloud-init + 设计网络 + provision 块）→
// 由 create_vm executor 统一完成「开机 → 等 IP → 托管凭据」→ 本函数再逐个装应用 / 跑 playbook。
// create_vm / app_install / ansible_run 均复用既有 executor（与页面操作同一套链路），本函数只做编排。
// userID/username 由调用方（goroutine 起手时）从 HTTP 上下文取定传入，不再读共享 handler 字段。
func (h *DesignerHandler) provisionVM(p *dsgPlan, n dsgNode, st *dsgApplyState, userID *uint, username string) (uint, error) {
	// 前置校验：云镜像必须存在于镜像库（images 表按 id）。
	// n.Ref 来自计划文件（用户可控的字符串），必须先解析成 uint 再交给 GORM——
	// 直传非纯数字字符串会被 GORM 当原始 SQL 片段拼进 WHERE（注入面，见 AGENTS 第 8 条）。
	imgID, ok := parseID(n.Ref)
	if !ok {
		return 0, fmt.Errorf("云镜像 ID 非法（%s）", n.Ref)
	}
	var img model.Image
	if err := h.DB.First(&img, imgID).Error; err != nil {
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

	// 1) 提交 create_vm（payload 与镜像管理页「基于云镜像创建」同构：source_image_id 引用不拷贝）。
	// 网络接画布上与本 VM 相连的 net 节点（无连线回落 default）；口令以密文进 provision 块，
	// 由 executor 解密后做 cloud-init seed 并托管到 vm_credentials——payload/日志不留明文。
	var host model.Host
	if err := h.DB.First(&host).Error; err != nil {
		return 0, fmt.Errorf("请先登记宿主机")
	}
	ciSpec := &virt.CloudInitSpec{Hostname: n.Name, User: sshUser, Password: n.SSHSecret}
	if verr := validateCloudInitText(ciSpec); verr != nil {
		return 0, fmt.Errorf("VM 节点 %s 的 cloud-init 配置不合法: %w", n.Name, verr)
	}
	netName := resolveVMNetwork(p, n)
	prov := map[string]interface{}{"start": true, "wait_ip": true, "ssh_user": sshUser}
	if n.SSHSecret != "" {
		cipherB64, saltHex, err := secretbox.SealWithMaster(h.VMCred.MasterSecret, n.SSHSecret)
		if err != nil {
			return 0, fmt.Errorf("SSH 口令加密失败: %w", err)
		}
		prov["password_enc"] = cipherB64
		prov["salt"] = saltHex
	}
	createPayload := map[string]interface{}{
		"name":         n.Name,
		"host_id":      host.ID,
		"storage_pool": pool,
		"vcpu":         vcpu,
		"memory_mb":    mem,
		"disks":        []map[string]interface{}{{"source_image_id": img.ID, "create_gb": int(img.SizeGB)}},
		"interfaces":   []map[string]interface{}{{"type": "network", "source": netName, "model": "virtio"}},
		"network":      netName,
		"cloud_init":   ciSpec,
		"provision":    prov,
	}
	task, err := h.Tasks.Submit("create_vm", "设计器创建 VM "+n.Name, createPayload, userID, username, n.Name, nil)
	if err != nil {
		return 0, fmt.Errorf("提交建机任务失败: %w", err)
	}
	st.Steps = append(st.Steps, "建机任务已提交（task="+strconv.FormatUint(uint64(task.ID), 10)+"），等待建机+初始化…")
	// 建机任务已含「等 IP」，窗口放宽到 12 分钟
	result, err := h.waitTask(task.ID, 12*time.Minute)
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
		return 0, fmt.Errorf("建机任务未返回 vm_id")
	}
	var vm model.VM
	if err := h.DB.First(&vm, vmID).Error; err != nil {
		return 0, fmt.Errorf("读取虚拟机记录失败: %w", err)
	}
	st.Steps = append(st.Steps, "VM 运行中，IP="+vm.IP+"（凭据已自动托管）")
	if vmID == 0 {
		return 0, fmt.Errorf("建机任务成功但未返回 vm_id")
	}

	// 2) app_install 逐个装（口令边界就地加密；凭据不落明文）
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
		if _, perr := os.Stat(config.DataPath("ansible", "playbooks", pbID+".yml")); perr != nil {
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

// syncApplyTask 把内存态同步进 tasks 表（AD1）。progress 按「步骤行数」粗估：
// 操作数×2 为满分母（每操作大约「开始+完成」两行），封顶 95 留终态写 100。
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
// 查 tasks 表最近一条该计划的 designer_apply / designer_reconcile 记录——重启后
// 状态与步骤不再丢失（落地与收敛共用同一状态端点）。
func (h *DesignerHandler) ApplyStatus(c *gin.Context) {
	id := c.Param("id")
	dsgApplyMu.Lock()
	st := dsgApplyRuns[id]
	dsgApplyMu.Unlock()
	if st != nil {
		h.syncApplyTask(st)
		Success(c, gin.H{"id": id, "status": st.Status, "steps": st.Steps, "error": st.Error, "mode": st.Mode})
		return
	}
	// 回退：查该计划最近一条落地/收敛任务（payload 含 plan_id）
	var t model.Task
	q := h.DB.Where("type IN ? AND payload LIKE ?", []string{"designer_apply", "designer_reconcile"}, `%"plan_id":"`+id+`"%`).
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
