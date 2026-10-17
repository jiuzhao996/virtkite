// designer.go：eNSP 式架构设计器（P2B v2，2026-10-04）——画布预览 + 节点编辑 +
// 预置模板 + 部署计划导出/一键应用。v2：VM 节点真落地（create_vm 建机 + 等 IP +
// app_install 逐个装应用，口令边界就地加密），容器栈继续 compose up。
//
// 本文件承载设计器核心：处理器装配、画布数据结构（节点/连线/计划）、内置模板与
// 部署计划的 CRUD/导出。漂移对比见 designer_drift.go，落地/收敛执行见
// designer_apply.go，画布 → Ansible 导出见 designer_ansible.go。
//
// 计划存 data/designer/*.json（id/名称/节点/连线，**不含 SSH 口令**——写盘前强制
// 剥离，口令只随 Apply 请求体一次性携带）；应用 = 顺序落地计划内的容器栈与 VM 节点
// （goroutine + recover + 内存进度，GET status 轮询）。
package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/service/dockerx"
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
	// Kind 连线语义：net（网络接入，默认）/ storage（存储挂载）/ dep（启动依赖）。
	// 前端画布会改类型；旧版结构体没有该字段，保存后类型被丢弃、重新加载一律变回 net。
	Kind string `json:"kind,omitempty"`
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
	// 与其他设计器端点同口径校验 ID：此前漏校验，异常字符会进入文件路径
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
	var b []byte
	b = append(b, "# VirtKite 部署计划（设计器导出）\n# 计划: "+p.Name+"\n\n"...)
	// 容器节点必须先聚合再输出：每个节点各写一个顶层 stacks: 会产生重复键，
	// 不是合法/可用的 compose YAML。
	var stackRefs []string
	for _, n := range p.Nodes {
		switch n.Kind {
		case "container":
			stackRefs = append(stackRefs, n.Ref+"   # 节点 "+n.Name)
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
	if len(stackRefs) > 0 {
		b = append(b, "stacks:\n"...)
		for _, ref := range stackRefs {
			b = append(b, "  - id: "+ref+"\n"...)
		}
		b = append(b, "\n"...)
	}
	for _, l := range p.Links {
		b = append(b, "# link: "+l.From+" -> "+l.To+"\n"...)
	}
	c.Data(http.StatusOK, "text/yaml; charset=utf-8", b)
}
