package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/console"
	"github.com/jiuzhao/vmops/service/virt"
	"github.com/jiuzhao/vmops/service/vnc"
	"gorm.io/gorm"
)

// VNCHandler 网页控制台处理器
type VNCHandler struct {
	DB       *gorm.DB
	Virt     *virt.Virt
	Token    *vnc.TokenStore
	Sessions *console.Registry
}

// NewVNCHandler 创建控制台处理器
func NewVNCHandler(db *gorm.DB, sessions *console.Registry) *VNCHandler {
	return &VNCHandler{
		DB:       db,
		Virt:     virt.New(),
		Token:    vnc.NewTokenStore(),
		Sessions: sessions,
	}
}

// RequestToken 为指定 VM 生成 VNC 访问令牌（admin）。
// 返回 websocket 连接地址，供前端 noVNC 使用。
func (h *VNCHandler) RequestToken(c *gin.Context) {
	id := c.Param("id")
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "虚拟机不存在"})
		return
	}

	// 获取 VM VNC 端口
	port, err := h.Virt.GetVNCInfo(vm.Name)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, err)
		return
	}

	token := h.Token.Generate(vm.ID, "127.0.0.1", port)

	// 记录 VNC 会话（关闭事件不可见，靠 token 解析刷新 + 过期清扫收敛）
	if h.Sessions != nil {
		uid, uname := taskUserFromContext(c)
		h.Sessions.OpenVNC(vm.ID, vm.Name, uname, uid, c.ClientIP(), token)
	}

	Success(c, gin.H{
		"token": token,
		"host":  "127.0.0.1",
		"port":  port,
	})
}

// ResolveToken 供 websockify JSONTokenApi 调用（内网，无需认证）。
// websockify 请求 GET /api/vnc/token/<token>，返回 {"host":..., "port":...}。
// 解析即代表浏览器真正连上，刷新对应 VNC 会话存活。
func (h *VNCHandler) ResolveToken(c *gin.Context) {
	token := c.Param("token")
	host, port, ok := h.Token.Lookup(token)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "token 无效或已过期"})
		return
	}
	if h.Sessions != nil {
		h.Sessions.TouchByToken(token)
	}

	c.JSON(http.StatusOK, gin.H{
		"host": host,
		"port": port,
	})
}
