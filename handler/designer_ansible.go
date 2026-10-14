// designer_ansible.go：架构设计器——画布 → Ansible 导出（DE1）。
// 按网络连线生成 inventory（组 = 网络名）与 site.yml 骨架（每 VM 一个 play，
// 引用的 playbook 以注释列出）；site.yml 落入 playbook 库目录，inventory 存 data/ansible/。
package handler

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/config"
)

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
