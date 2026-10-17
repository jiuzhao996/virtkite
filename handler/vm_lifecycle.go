package handler

// vm_lifecycle.go —— 虚拟机生命周期异步入口：创建 / 克隆 / 删除。
// 三个接口只做参数校验与任务提交（Tasks.Submit create_vm / clone_vm / delete_vm），
// 耗时逻辑在 service/tasks 的 executor 后台执行；HTTP 202 返回 {task_id}，
// 前端轮询 GET /api/tasks/:id。电源操作见 vm_power.go，规格调整见 vm_spec.go。

import (
	"encoding/json"
	"github.com/jiuzhao/vmops/service/tasks"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/secretbox"
	"github.com/jiuzhao/vmops/service/virt"
)

// CreateVM 创建虚拟机（异步：校验基础参数后 Submit create_vm，后台执行 provision）。
// 对应 virsh vol-create-as + virsh define，耗时逻辑已搬运至 service/tasks executor。
// HTTP 202 返回 {task_id}，前端轮询 GET /api/tasks/:id。
//
// 本 handler 的职责边界：只做「提交前就能判定」的校验（名称、cloud-init 文本、
// 宿主机存在性、自动初始化凭据），绝不在请求线程里碰 libvirt/磁盘；任何耗时操作
// 都交给任务队列，避免 HTTP 超时与重复提交。
func (h *VMHandler) CreateVM(c *gin.Context) {
	if !h.submitTaskGuard(c) {
		return
	}
	// 原样透传请求体：先读原始字节，再分别做校验与 payload 透传。
	// 原因：create_vm 的 payload 字段多于本文件显式建模的字段（如 cloud_init、
	// 磁盘细节等），用结构体反序列化再序列化会丢字段；保留 raw body 才能无损透传。
	body, err := c.GetRawData()
	if err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}
	var req struct {
		Name        string                   `json:"name"`
		HostID      uint                     `json:"host_id"`
		StoragePool string                   `json:"storage_pool"`
		VCPU        int                      `json:"vcpu"`
		MemoryMB    int                      `json:"memory_mb"`
		Disks       []map[string]interface{} `json:"disks"`
		Interfaces  []virt.InterfaceSpec     `json:"interfaces"`
		Network     string                   `json:"network"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}
	if req.Name == "" {
		Fail(c, http.StatusBadRequest, "虚拟机名称不能为空")
		return
	}
	// 校验名称合法性
	if !validateVMName(req.Name) {
		Fail(c, http.StatusBadRequest, "虚拟机名称只允许字母、数字、下划线和连字符")
		return
	}
	// cloud-init 底线校验（换行可注入 cloud-config 顶层键；非法 net_mode/静态缺参
	// 会静默产出坏 seed）：body 原样透传任务前先探测把关，错误直接回给创建向导
	var ciProbe struct {
		CloudInit *virt.CloudInitSpec `json:"cloud_init"`
	}
	if err := json.Unmarshal(body, &ciProbe); err == nil && ciProbe.CloudInit != nil {
		if verr := validateCloudInitText(ciProbe.CloudInit); verr != nil {
			Fail(c, http.StatusBadRequest, "cloud-init 配置不合法："+verr.Error())
			return
		}
	}
	// 轻量校验宿主机存在性：未指定时确认平台已登记首台
	// （单机部署语义：host_id 省略即用第一台；多宿主机尚未实现，见 AGENTS.md 遗留清单）
	if req.HostID != 0 {
		var host model.Host
		if err := h.DB.First(&host, req.HostID).Error; err != nil {
			ErrorWithMessage(c, http.StatusBadRequest, "宿主机不存在", err)
			return
		}
	} else {
		if _, err := h.firstHost(); err != nil {
			ErrorWithMessage(c, http.StatusBadRequest, "请先在宿主机管理中登记宿主机", err)
			return
		}
	}
	// 二次反序列化为通用 map 作为任务 payload：保留原始全部字段（见上方 raw body 说明）。
	payload := map[string]interface{}{}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &payload); err != nil {
			ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
			return
		}
	}
	if payload == nil {
		payload = map[string]interface{}{}
	}
	// 口令边界加密：cloud_init.password 绝不能明文进 tasks.payload（会落库）。
	// executor 用同一主密钥解回明文生成 seed ISO。
	if err := sealPayloadCloudInit(payload); err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "初始化配置加密失败", err)
		return
	}
	// 可选「自动初始化」（向导勾选）：建机任务在 define 后自动 开机 → 等 IP → 托管凭据，
	// 与设计器落地共用 create_vm 的 provision 块。口令在此边界加密，payload/日志不留明文。
	var autoReq struct {
		AutoProvision bool `json:"auto_provision"`
	}
	_ = json.Unmarshal(body, &autoReq)
	if autoReq.AutoProvision {
		// 自动初始化必须能登录 VM 才能托管凭据：缺用户名/口令直接拒绝，
		// 而不是提交一个注定在后台失败的任务。
		if ciProbe.CloudInit == nil || ciProbe.CloudInit.User == "" || ciProbe.CloudInit.Password == "" {
			Fail(c, http.StatusBadRequest, "自动初始化需要 cloud-init 的用户名与口令（用于托管 SSH 凭据）")
			return
		}
		prov, perr := buildProvisionBlock(ciProbe.CloudInit.User, ciProbe.CloudInit.Password)
		if perr != nil {
			ErrorWithMessage(c, http.StatusInternalServerError, "初始化配置加密失败", perr)
			return
		}
		payload["provision"] = prov
	}
	userID, username := taskUserFromContext(c)
	task, err := h.Tasks.Submit("create_vm", "创建虚拟机 "+req.Name, payload, userID, username, req.Name, nil)
	if err != nil {
		SubmitTaskError(c, err)
		return
	}
	Accepted(c, "任务已提交", gin.H{"task_id": task.ID})
}

// buildProvisionBlock 组装 create_vm 的 provision 块：开机 + 等 IP + 托管凭据（口令以密文进 payload）。
// 与设计器 provisionVM 内的组装同义——向导在此边界加密，避免明文口令落 tasks.payload / 日志。
func buildProvisionBlock(user, password string) (map[string]interface{}, error) {
	cipherB64, saltHex, err := secretbox.SealWithMaster(CredentialMasterSecret(), password)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"start":        true,
		"wait_ip":      true,
		"ssh_user":     user,
		"password_enc": cipherB64,
		"salt":         saltHex,
	}, nil
}

// CloneVM 克隆虚拟机（异步：校验后 Submit clone_vm，后台执行 vol-clone + define）。
// HTTP 202 返回 {task_id}，前端轮询 GET /api/tasks/:id。
// 注意：增量克隆靠卷 XML 的 <backingStore> 建立父子盘关系（见 AGENTS.md 第 11 条），
// 不是 StorageVolCreateXMLFrom 的全量拷贝；因此删除父盘前必须过删卷守卫。
func (h *VMHandler) CloneVM(c *gin.Context) {
	if !h.submitTaskGuard(c) {
		return
	}
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	// Preload Host 便于任务侧取宿主机信息；同时按 id 参数化查询，避免注入
	var src model.VM
	if err := h.DB.Preload("Host").First(&src, id).Error; err != nil {
		ErrorWithMessage(c, http.StatusNotFound, "虚拟机不存在", err)
		return
	}
	if !vmVisible(c, h.DB, src.ID) {
		Fail(c, http.StatusNotFound, "虚拟机不存在")
		return
	}
	// 源机有未完结任务时拒绝提交：delete_vm 进行到一半时提交克隆，
	// 克隆出的子盘 backing 指向即将被删的父盘，克隆机磁盘立即不可读
	if !h.guardVMIdle(c, src.ID) {
		return
	}
	// 克隆期间禁止源机被并发改动（双开提交会克隆出两份脏卷）
	release, ok := h.lockVM(c, src.ID)
	if !ok {
		return
	}
	defer release()
	var req struct {
		Name        string `json:"name" binding:"required"`
		StoragePool string `json:"storage_pool"`
		VCPU        int    `json:"vcpu"`
		MemoryMB    int    `json:"memory_mb"`
		Network     string `json:"network"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}
	if !validateVMName(req.Name) {
		Fail(c, http.StatusBadRequest, "虚拟机名称只允许字母、数字、下划线和连字符")
		return
	}
	// 提交 payload 只带结构性字段；口令等敏感信息不在此出现。
	// source_id 同时传给 Submit 的关联 ID（&srcID），任务侧据此做资源级权限/展示关联。
	payload := map[string]interface{}{
		"source_id":    src.ID,
		"name":         req.Name,
		"storage_pool": req.StoragePool,
		"vcpu":         req.VCPU,
		"memory_mb":    req.MemoryMB,
		"network":      req.Network,
	}
	userID, username := taskUserFromContext(c)
	srcID := src.ID
	task, err := h.Tasks.Submit("clone_vm", "克隆虚拟机 "+req.Name, payload, userID, username, req.Name, &srcID)
	if err != nil {
		SubmitTaskError(c, err)
		return
	}
	Accepted(c, "任务已提交", gin.H{"task_id": task.ID})
}

// DeleteVM 删除虚拟机（异步：Submit delete_vm，后台执行 undefine + 卷清理 + 软删除）。
// HTTP 202 返回 {task_id}，前端轮询 GET /api/tasks/:id。
// DeletePreview GET /api/vms/:id/delete-preview（A 批次）——删除前亮家底：
// 哪些盘会被物理删除（含容量）、哪些盘因守卫保留及原因、几个快照会被一并丢弃。
// 与真删共用 tasks.VolumeGuard 判定，预览与执行不会说两套话。
func (h *VMHandler) DeletePreview(c *gin.Context) {
	vm, ok := h.findVM(c)
	if !ok {
		return
	}
	Success(c, tasks.BuildDeletePreview(h.DB, h.Virt, vm))
}

// DeleteVM 删除虚拟机（异步：Submit delete_vm，后台执行 undefine + 卷清理 + 软删除）。
// HTTP 202 返回 {task_id}，前端轮询 GET /api/tasks/:id。
//
// 后台执行时会过「删卷三重守卫」（池外文件 / 共享基镜像 / 仍被引用的父盘），
// 命中即保留卷并在任务结果 kept_volumes 里给出中文原因；不要为了删干净拆守卫。
func (h *VMHandler) DeleteVM(c *gin.Context) {
	if !h.submitTaskGuard(c) {
		return
	}
	vm, ok := h.findVM(c)
	if !ok {
		return
	}
	// 与 CloneVM 互斥：克隆任务未完结时提交删除，后台删掉的正是克隆体刚要引用的父盘
	if !h.guardVMIdle(c, vm.ID) {
		return
	}
	release, ok := h.lockVM(c, vm.ID)
	if !ok {
		return
	}
	defer release()
	payload := map[string]interface{}{"vm_id": vm.ID}
	userID, username := taskUserFromContext(c)
	vmID := vm.ID
	task, err := h.Tasks.Submit("delete_vm", "删除虚拟机 "+vm.Name, payload, userID, username, vm.Name, &vmID)
	if err != nil {
		SubmitTaskError(c, err)
		return
	}
	Accepted(c, "任务已提交", gin.H{"task_id": task.ID})
}
