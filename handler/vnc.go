package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/virt"
	"github.com/jiuzhao/vmops/service/vnc"
	"gorm.io/gorm"
)

// VNCHandler 网页控制台处理器
type VNCHandler struct {
	DB    *gorm.DB
	Virt  *virt.Virt
	Token *vnc.TokenStore
}

// NewVNCHandler 创建控制台处理器
func NewVNCHandler(db *gorm.DB) *VNCHandler {
	return &VNCHandler{
		DB:    db,
		Virt:  virt.New(),
		Token: vnc.NewTokenStore(),
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
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	token := h.Token.Generate(vm.ID, "127.0.0.1", port)

	Success(c, gin.H{
		"token": token,
		"host":  "127.0.0.1",
		"port":  port,
	})
}

// ResolveToken 供 websockify JSONTokenApi 调用（内网，无需认证）。
// websockify 请求 GET /api/vnc/token/<token>，返回 {"host":..., "port":...}。
func (h *VNCHandler) ResolveToken(c *gin.Context) {
	token := c.Param("token")
	host, port, ok := h.Token.Lookup(token)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "token 无效或已过期"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"host": host,
		"port": port,
	})
}