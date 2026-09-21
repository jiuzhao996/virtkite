package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"gorm.io/gorm"
)

// SSHHostKeyHandler SSH 主机指纹管理（TOFU）。
// 指纹由 service/vmssh 的主机密钥回调在 SSH 首连时自动落库（host_keys 表），
// 本处理器只做 admin 的查看与删除：虚拟机重建会换主机密钥，删除旧指纹记录后
// 下一次连接重新走 TOFU 记录新指纹（错误文案里引导的就是这个动作）。
// 路由组挂 AdminMiddleware（/api/ssh-host-keys），handler 内不做二次收口
// （与 /users 组同款：组中间件即唯一闸门，组内无 /api/vms 前缀的 operator 放行问题）。
type SSHHostKeyHandler struct {
	DB *gorm.DB
}

// NewSSHHostKeyHandler 创建 SSH 主机指纹处理器。
func NewSSHHostKeyHandler(db *gorm.DB) *SSHHostKeyHandler {
	return &SSHHostKeyHandler{DB: db}
}

// List 查看全部已记录的主机指纹（admin）。GET /api/ssh-host-keys
func (h *SSHHostKeyHandler) List(c *gin.Context) {
	// 预分配非 nil 空切片：查无记录时序列化为 [] 而非 null（批 B② 既定约定）
	keys := make([]model.HostKey, 0)
	if err := h.DB.Order("host, port").Find(&keys).Error; err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"total": len(keys), "items": keys})
}

// Delete 删除某条主机指纹（admin）。DELETE /api/ssh-host-keys/:id
func (h *SSHHostKeyHandler) Delete(c *gin.Context) {
	// 路径参数主键必须先解析成数值再交 GORM，直传字符串会被当原始 SQL 拼接（见 param.go）
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	res := h.DB.Delete(&model.HostKey{}, id)
	if res.Error != nil {
		ErrorResponse(c, http.StatusInternalServerError, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		Fail(c, http.StatusNotFound, "指纹记录不存在")
		return
	}
	Success(c, gin.H{"message": "指纹记录已删除，下次连接将重新记录"})
}
