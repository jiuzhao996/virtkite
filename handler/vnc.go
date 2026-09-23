package handler

import (
	"fmt"
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

// NewVNCHandler 创建控制台处理器。
//
// tokens 必须由外部注入共享实例：签发（POST /api/vms/:id/vnc-token，认证组内）与解析
// （GET /api/vnc/token/:token，公开组）分属两个路由注册函数，若各自 new 一个 TokenStore，
// 签发写进 A 库的 token 在 B 库里永远查不到——接口全程 200、无任何报错，控制台业务链却整条死掉。
// 统一由 Deps 装配下发，禁止在 handler 内部自建。
func NewVNCHandler(db *gorm.DB, sessions *console.Registry, tokens *vnc.TokenStore) *VNCHandler {
	if tokens == nil {
		panic("NewVNCHandler: VNC TokenStore 未注入，必须由 Deps 提供共享实例")
	}
	return &VNCHandler{
		DB:       db,
		Virt:     virt.New(),
		Token:    tokens,
		Sessions: sessions,
	}
}

// RequestToken 为指定 VM 生成 VNC 访问令牌。
// 返回 websocket 连接地址供前端 noVNC 使用；只读角色（viewer）附带 view_only 标记，
// 由前端以 noVNC 的 view_only 模式打开——图形控制台协议本身没有只读模式，
// 键盘鼠标必须在客户端侧禁用，这样「只读运维」角色才名副其实。
func (h *VNCHandler) RequestToken(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		Fail(c, http.StatusNotFound, "虚拟机不存在")
		return
	}
	// 授权决定可见性：非 admin 未持有效授权与不存在同响应
	if !vmVisible(c, h.DB, vm.ID) {
		Fail(c, http.StatusNotFound, "虚拟机不存在")
		return
	}

	// 获取 VM VNC 端口
	port, err := h.Virt.GetVNCInfo(vm.Name)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, err)
		return
	}

	// 随机源故障时 Generate 返回错误：此时不签发、不返回 token（带病 token 等同放行任意连接）
	token, err := h.Token.Generate(vm.ID, "127.0.0.1", port)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, fmt.Errorf("生成 VNC 令牌失败：%w", err))
		return
	}

	// 记录 VNC 会话（关闭事件不可见，靠 token 解析刷新 + 过期清扫收敛）
	if h.Sessions != nil {
		uid, uname := taskUserFromContext(c)
		h.Sessions.OpenVNC(vm.ID, vm.Name, uname, uid, c.ClientIP(), token)
	}

	// 仅 viewer（及无法识别的角色）只读观看；operator 是操作角色，控制台键鼠可用
	role, _ := c.Get("role")
	roleStr, _ := role.(string)

	Success(c, gin.H{
		"token":     token,
		"host":      "127.0.0.1",
		"port":      port,
		"view_only": roleStr != "admin" && roleStr != "operator",
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
