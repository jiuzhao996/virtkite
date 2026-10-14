// ansible_roles.go：运维自动化——共享 role 库（data/ansible/roles/）。
// playbook 是「一次执行」，role 是「可复用的能力单元」；共享库让多个 playbook 引用同一份实现。
package handler

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/service/ansible"
)

// ---------- 共享 role 库（data/ansible/roles/）----------
//
// role 与 playbook 的区别：playbook 是「一次执行」，role 是「可复用的能力单元」。
// 共享库让 role 脱离单个 playbook 目录独立存在，多个 playbook 引用同一份实现；
// 想特化的 playbook 在本地放同名 role 即可覆盖（优先级已实测）。

// roleItem 清单条目：RoleMeta + 是否内置种子（种子判定依赖仓库路径，故在 handler 侧补）。
type roleItem struct {
	ansible.RoleMeta
	BuiltIn bool `json:"built_in"`
}

// ListRoles GET /api/ansible/roles
func (h *AnsibleHandler) ListRoles(c *gin.Context) {
	items, err := ansible.ListRoles(roleDir())
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "读取 role 库失败", err)
		return
	}
	out := make([]roleItem, 0, len(items))
	for _, m := range items {
		_, serr := os.Stat(filepath.Join(seedRoleDir(), m.Name))
		out = append(out, roleItem{RoleMeta: m, BuiltIn: serr == nil})
	}
	Success(c, gin.H{"items": out, "total": len(out)})
}

// CreateRole POST /api/ansible/roles body {name*, desc?} —— 按标准骨架新建。
func (h *AnsibleHandler) CreateRole(c *gin.Context) {
	var req struct {
		Name string `json:"name"`
		Desc string `json:"desc"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, "参数格式非法")
		return
	}
	name := strings.TrimSpace(req.Name)
	if err := ansible.ScaffoldRole(roleDir(), name, strings.TrimSpace(req.Desc)); err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	log.Printf("[ansible] 新建共享 role %s by=%s from=%s", name, c.GetString("username"), c.ClientIP())
	Success(c, gin.H{"name": name, "created": true})
}

// DeleteRole DELETE /api/ansible/roles/:name —— 整目录删除。
// 不检查「是否被 playbook 引用」：引用关系藏在 playbook 正文里，静态扫不干净；
// 而删掉后的表现是执行时明确的「role 不存在」报错，比留一堆猜疑更清楚。
func (h *AnsibleHandler) DeleteRole(c *gin.Context) {
	name := c.Param("name")
	if err := ansible.RemoveDir(roleDir(), name); err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	log.Printf("[ansible] 删除共享 role %s by=%s from=%s", name, c.GetString("username"), c.ClientIP())
	Success(c, gin.H{"name": name, "deleted": true})
}

// ListRoleFiles GET /api/ansible/roles/:name/files
func (h *AnsibleHandler) ListRoleFiles(c *gin.Context) {
	files, err := ansible.ListDirFiles(roleDir(), c.Param("name"))
	if err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	Success(c, gin.H{"items": files})
}

// ReadRoleFile GET /api/ansible/roles/:name/file?path=tasks/main.yml
func (h *AnsibleHandler) ReadRoleFile(c *gin.Context) {
	content, err := ansible.ReadDirFile(roleDir(), c.Param("name"), c.Query("path"))
	if err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	Success(c, gin.H{"content": content})
}

// WriteRoleFile PUT /api/ansible/roles/:name/file body {path, content}
// 写后做一次 role 语法校验（把 role 临时挂进一个最小 playbook 跑 --syntax-check）：
// role 单独没有「语法检查」入口，但它最终是被 playbook 加载的，等价校验最接近真实用法。
func (h *AnsibleHandler) WriteRoleFile(c *gin.Context) {
	name := c.Param("name")
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Path) == "" {
		Fail(c, http.StatusBadRequest, "缺少 path 参数")
		return
	}
	if err := ansible.WriteDirFile(roleDir(), name, req.Path, req.Content); err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	warning := ""
	if err := ansible.SyntaxCheckRole(roleDir(), name); err != nil {
		warning = err.Error()
	}
	Success(c, gin.H{"saved": true, "warning": warning})
}

// UploadRoleFile POST /api/ansible/roles/:name/upload（multipart：file + path=相对路径）
func (h *AnsibleHandler) UploadRoleFile(c *gin.Context) {
	name := c.Param("name")
	rel := strings.TrimSpace(c.PostForm("path"))
	fh, err := c.FormFile("file")
	if err != nil {
		Fail(c, http.StatusBadRequest, "缺少上传文件")
		return
	}
	if rel == "" {
		rel = fh.Filename
	}
	src, err := fh.Open()
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "读取上传文件失败", err)
		return
	}
	defer src.Close()
	n, err := ansible.SaveDirUpload(roleDir(), name, rel, src)
	if err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	Success(c, gin.H{"message": "已上传", "size": n})
}

// DeleteRoleFile DELETE /api/ansible/roles/:name/file?path=
func (h *AnsibleHandler) DeleteRoleFile(c *gin.Context) {
	if err := ansible.DeleteDirFile(roleDir(), c.Param("name"), c.Query("path")); err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	Success(c, gin.H{"message": "已删除"})
}
