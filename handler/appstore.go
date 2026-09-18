// appstore.go 应用商店 v2（声明式 compose 应用包）处理器。
//
// 与 AppsHandler（bash 脚本版，经 SSH 装进虚拟机）并存：本处理器操作**宿主机**上的
// docker compose，机制见 service/appstore 包文档。安装是同步执行（compose up 含镜像
// 拉取最长 10 分钟，gin 直接等待，前端 loading），不走 tasks 异步任务。
//
// 权限由路由挂载的 NonViewerMiddleware 统一收口（viewer 全部 403，operator/admin 可用），
// handler 内不做二次鉴权。key 为字符串主键且会被拼进文件路径，service 层 keyPattern
// 已做白名单校验（路径穿越成分一律按「应用不存在」处理）。
package handler

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/service/appstore"
)

// AppStoreV2Handler 应用商店 v2 处理器（无状态：目录即数据，无需 DB/libvirt 依赖）
type AppStoreV2Handler struct{}

// NewAppStoreV2Handler 创建应用商店 v2 处理器
func NewAppStoreV2Handler() *AppStoreV2Handler {
	return &AppStoreV2Handler{}
}

// ListV2 GET /api/appstore
// 应用目录：元数据 + formFields（前端据此动态渲染安装表单）。
func (h *AppStoreV2Handler) ListV2(c *gin.Context) {
	metas, err := appstore.List()
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"items": metas})
}

// GetV2 GET /api/appstore/:key
// 单应用详情：元数据 + formFields + compose 预览（安装前审查将启动的内容）。
func (h *AppStoreV2Handler) GetV2(c *gin.Context) {
	meta, compose, fields, err := appstore.Get(c.Param("key"))
	if err != nil {
		respondAppStoreError(c, err)
		return
	}
	Success(c, gin.H{
		"key":         meta.Key,
		"name":        meta.Name,
		"category":    meta.Category,
		"description": meta.Description,
		"tags":        meta.Tags,
		"version":     meta.Version,
		"formFields":  fields,
		"compose":     compose,
	})
}

// InstallV2 POST /api/appstore/:key/install
// body {"version":"", "values":{"PANEL_APP_PORT":"8080",...}}（body 可为空对象，全走默认值）。
// 同步执行 compose up（服务层 10 分钟超时）；成功返回 {message, app_dir}；
// 校验失败 400 透出字段级中文文案，compose 失败 500 透出 stderr 摘要。
func (h *AppStoreV2Handler) InstallV2(c *gin.Context) {
	var req struct {
		Version string            `json:"version"`
		Values  map[string]string `json:"values"`
	}
	// 允许空 body：无表单项（或全默认值）的应用直接 {} 安装
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}
	// 刻意用 Background 派生上下文而非请求 ctx：同步安装最长 10 分钟，浏览器断开/刷新
	// 不应把 compose up 中途砍掉（半建的容器组要靠重装覆盖收尾）；服务层自带 10 分钟超时兜底
	appDir, err := appstore.Install(context.Background(), c.Param("key"), req.Version, req.Values)
	if err != nil {
		respondAppStoreError(c, err)
		return
	}
	Created(c, "安装完成", gin.H{"app_dir": appDir})
}

// UninstallV2 POST /api/appstore/:key/uninstall
// body {"remove_data":false}：false 只 compose down 保留数据目录，true 连卷带目录一起删。
func (h *AppStoreV2Handler) UninstallV2(c *gin.Context) {
	var req struct {
		RemoveData bool `json:"remove_data"`
	}
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}
	if err := appstore.Uninstall(c.Param("key"), req.RemoveData); err != nil {
		respondAppStoreError(c, err)
		return
	}
	Created(c, "卸载完成", gin.H{"remove_data": req.RemoveData})
}

// StatusV2 GET /api/appstore/status
// 全部已装应用的运行概览（name/services/running）。路由挂 /status 静态段与 :key 同级，
// gin 静态路由优先匹配，实测无冲突（gin v1.9）。
func (h *AppStoreV2Handler) StatusV2(c *gin.Context) {
	items, err := appstore.Status()
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"items": items})
}

// respondAppStoreError 统一翻译 appstore 层错误：
//   - *ValidationError：消息本身即中文字段级文案，无内部细节 → 400 原样透出
//   - ErrNotFound：应用不存在/未安装 → 404（friendlyMessage 取冒号前中文段）
//   - *ComposeError：docker 执行失败，stderr 摘要是操作者排障必需信息 → 500 透出
//     （与 terminal WS 帧同口径的显式例外，完整错误已进日志）
//   - 其余（文件系统等内部错误）→ 500 通用文案，完整错误只进日志
func respondAppStoreError(c *gin.Context, err error) {
	var validationErr *appstore.ValidationError
	var composeErr *appstore.ComposeError
	switch {
	case errors.As(err, &validationErr):
		LogError(c, err)
		Fail(c, http.StatusBadRequest, validationErr.Msg)
	case errors.Is(err, appstore.ErrNotFound):
		ErrorResponse(c, http.StatusNotFound, err)
	case errors.As(err, &composeErr):
		LogError(c, err)
		Fail(c, http.StatusInternalServerError, err.Error())
	default:
		ErrorResponse(c, http.StatusInternalServerError, err)
	}
}
