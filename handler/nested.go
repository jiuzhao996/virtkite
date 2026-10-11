// nested.go：宿主机嵌套虚拟化（VM 里再跑 VM）——状态查询与开关。admin 专属。
// 宿主内核层能力，与单台 VM 无关，故独立挂 /api/host-kvm（不进 hosts 的 /:id 路由树）。
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/service/hostkvm"
)

// NestedStatus GET /api/host-kvm/nested —— 查询嵌套虚拟化状态。
func (h *HostHandler) NestedStatus(c *gin.Context) {
	Success(c, hostkvm.Detect())
}

// SetNested POST /api/host-kvm/nested body {enable: bool} —— 开启/关闭嵌套虚拟化。
// 写 modprobe.d 持久化 + 尝试运行时重载（需所有 VM 关机，否则只持久化、重启生效）。
func (h *HostHandler) SetNested(c *gin.Context) {
	var req struct {
		Enable bool `json:"enable"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	st, note, err := hostkvm.Set(req.Enable)
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "设置嵌套虚拟化失败", err)
		return
	}
	Success(c, gin.H{"status": st, "note": note})
}
