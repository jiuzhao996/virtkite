package handler

// image_clone.go 镜像的生命周期动作：标记/取消模板、基于镜像异步创建虚拟机（linked clone）。

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/virt"
)

// SetImageTemplate 标记/取消镜像为模板（body: {is_template}）。
func (h *ImageHandler) SetImageTemplate(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	var img model.Image
	if err := h.DB.First(&img, id).Error; err != nil {
		Fail(c, http.StatusNotFound, "镜像不存在")
		return
	}

	var req struct {
		IsTemplate bool `json:"is_template"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}

	if err := h.DB.Model(&img).Update("is_template", req.IsTemplate).Error; err != nil {
		Fail(c, http.StatusInternalServerError, "更新镜像模板标记失败")
		return
	}
	img.IsTemplate = req.IsTemplate
	Success(c, img)
}

// CloneVM 基于镜像/模板创建虚拟机（异步：校验后 Submit clone_image_vm，后台执行 linked clone）。
// HTTP 202 返回 {task_id}，前端轮询 GET /api/tasks/:id。
func (h *ImageHandler) CloneVM(c *gin.Context) {
	if h.Tasks == nil {
		Fail(c, http.StatusInternalServerError, "任务系统未初始化")
		return
	}
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	var img model.Image
	if err := h.DB.First(&img, id).Error; err != nil {
		Fail(c, http.StatusNotFound, "镜像不存在")
		return
	}
	var req struct {
		Name        string              `json:"name" binding:"required"`
		StoragePool string              `json:"storage_pool"`
		VCPU        int                 `json:"vcpu"`
		MemoryMB    int                 `json:"memory_mb"`
		Network     string              `json:"network"`
		CloudInit   *virt.CloudInitSpec `json:"cloud_init,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}
	if !validateVMName(req.Name) {
		Fail(c, http.StatusBadRequest, "虚拟机名称只允许字母、数字、下划线和连字符")
		return
	}
	payload := map[string]interface{}{
		"image_id":     img.ID,
		"name":         req.Name,
		"storage_pool": req.StoragePool,
		"vcpu":         req.VCPU,
		"memory_mb":    req.MemoryMB,
		"network":      req.Network,
	}
	if req.CloudInit != nil {
		// 直连建机路径同样过底线校验（此前未校验直通 virt 层；模板侧本就有校验）
		if err := validateCloudInitText(req.CloudInit); err != nil {
			Fail(c, http.StatusBadRequest, "cloud-init 配置不合法："+err.Error())
			return
		}
		payload["cloud_init"] = req.CloudInit
	}
	userID, username := taskUserFromContext(c)
	task, err := h.Tasks.Submit("clone_image_vm", "从镜像创建虚拟机 "+req.Name, payload, userID, username, req.Name, nil)
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "提交任务失败", err)
		return
	}
	Accepted(c, "任务已提交", gin.H{"task_id": task.ID})
}
