// storage_meta.go：存储池的平台侧元数据——角色推断/覆盖、描述、种子目录与默认池查询。
package handler

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/setting"
	"gorm.io/gorm"
)

// poolRoleNames 平台认池角色的全集（pool_meta.role 白名单；空串 = 跟随自动推断）。
var poolRoleNames = []string{"模板基盘", "系统盘", "数据盘", "安装镜像", "系统池", "其他"}

// inferPoolRole 按池名/路径推断池角色（纯函数，供列表展示与单测）。
// libvirt 池 XML 没有语义字段，这里按本平台的目录约定给缺省角色：
// base=模板基盘兼镜像库（backing 父盘、模板与上传/下载镜像）、images=系统盘（增量克隆子卷）、
// exten=数据盘（热挂数据盘）、iso=安装镜像（ISO）。
func inferPoolRole(name, path string) string {
	switch {
	case name == "base" || strings.HasSuffix(path, "/storage/base"):
		return "模板基盘"
	case name == "images" || strings.HasSuffix(path, "/storage/images"):
		return "系统盘"
	case name == "exten" || strings.HasSuffix(path, "/storage/exten"):
		return "数据盘"
	case name == "iso" || strings.HasSuffix(path, "/storage/iso"):
		return "安装镜像"
	default:
		return ""
	}
}

// inferPoolDesc 按池角色给出缺省描述（纯函数）。
//
// 与 inferPoolRole 同一套路：libvirt 池 XML 没有语义字段，而描述又是纯平台概念，
// 不补缺省的话卡片上就是一片空白——此前只能显示「点击编辑添加描述」诱导用户去点，
// 反而让整行成了误触热区。这里把文案内置，用户不需要为此动手。
// DB 里显式设过描述时以 DB 为准（见列表装配处）。
func inferPoolDesc(name, path string) string {
	switch inferPoolRole(name, path) {
	case "模板基盘":
		return "模板与云镜像落点，增量克隆的父盘"
	case "系统盘":
		return "平台主力系统盘池，增量克隆落点"
	case "数据盘":
		return "给虚拟机热挂的扩展数据盘"
	case "安装镜像":
		return "系统安装 ISO 存放处"
	default:
		return ""
	}
}

// loadPoolMeta 读取池元数据（角色覆盖 + 描述），key 为池名。
func (h *StorageHandler) loadPoolMeta() map[string]model.PoolMeta {
	metas := map[string]model.PoolMeta{}
	var rows []model.PoolMeta
	if err := h.DB.Find(&rows).Error; err != nil {
		return metas
	}
	for _, r := range rows {
		metas[r.PoolName] = r
	}
	return metas
}

// seedDirInfo cloud-init 种子目录信息（路径 + seed 文件数）：seed 是每 VM 一份的生成物，
// 不进池管理，在存储页汇总条展示。
func (h *StorageHandler) seedDirInfo() gin.H {
	dir := config.GlobalConfig.SeedDir
	if dir == "" {
		// 兜底跟随 DATA_DIR（配置初始化后通常已有值，这里只防极端情况）
		dir = filepath.Join("data", "seed")
	}
	count := 0
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), "-seed.iso") {
				count++
			}
		}
	}
	return gin.H{"path": dir, "count": count}
}

// defaultPoolName 平台默认存储池（建机未指定池时落点），读系统设置，缺省走 setting 兜底值。
func (h *StorageHandler) defaultPoolName() string {
	var row model.Setting
	if err := h.DB.Where("`key` = ?", "default_storage_pool").First(&row).Error; err == nil && row.Value != "" {
		return row.Value
	}
	return setting.DefaultStoragePoolFallback
}

// UpdatePoolMeta 更新池的平台侧元数据（PUT /api/storage/pools/:name/meta）。
// role 必须在白名单内或空串（空 = 跟随自动推断）；description 最长 500 字符。
func (h *StorageHandler) UpdatePoolMeta(c *gin.Context) {
	name := c.Param("name")
	if !validVolName(name) {
		Fail(c, http.StatusBadRequest, "存储池名称不合法")
		return
	}
	var req struct {
		Role        string `json:"role"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}
	validRole := req.Role == ""
	for _, r := range poolRoleNames {
		if req.Role == r {
			validRole = true
		}
	}
	if !validRole {
		Fail(c, http.StatusBadRequest, "角色只允许："+strings.Join(poolRoleNames, "/")+"或留空自动推断")
		return
	}
	if len(req.Description) > 500 {
		Fail(c, http.StatusBadRequest, "描述最长 500 字符")
		return
	}

	var meta model.PoolMeta
	err := h.DB.Where("pool_name = ?", name).First(&meta).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			meta = model.PoolMeta{PoolName: name}
		} else {
			ErrorWithMessage(c, http.StatusInternalServerError, "查询池元数据失败", err)
			return
		}
	}
	meta.Role = req.Role
	meta.Description = req.Description
	if err := h.DB.Save(&meta).Error; err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "保存池元数据失败", err)
		return
	}
	Success(c, meta)
}
