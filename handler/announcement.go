package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/setting"
	"gorm.io/gorm"
)

// AnnouncementHandler 系统公告公开读取处理器（v3 批次 P）。
// 公告面向全员（含未登录的登录页访客），读接口无需认证；写入口收敛在
// PUT /api/settings（AdminMiddleware），内容与长度由 service/setting.Validate 把关。
type AnnouncementHandler struct {
	DB         *gorm.DB
	SettingMgr *setting.Manager
}

// NewAnnouncementHandler 创建公告读取处理器。
func NewAnnouncementHandler(db *gorm.DB, mgr *setting.Manager) *AnnouncementHandler {
	return &AnnouncementHandler{DB: db, SettingMgr: mgr}
}

// Get 返回当前公告 {content, updated_at}（平台自身数据，无 virsh 等价命令）。
// content 为空表示无公告，仅回 {content:""}（updated_at 对空公告无意义，不下发）。
// 内容读 Settings 进程内缓存（登录页匿名高频访问不打 DB）；updated_at 取
// system_settings 表行的更新时间（即公告发布/更新时间），查库失败降级为不下发该字段
// 并留日志——公开端点恒 200，不因辅助字段失败影响公告展示。
func (h *AnnouncementHandler) Get(c *gin.Context) {
	content := h.SettingMgr.Announcement()
	resp := gin.H{"content": content}
	if content != "" {
		var s model.Setting
		err := h.DB.Where(&model.Setting{Key: setting.KeyAnnouncement}).First(&s).Error
		switch {
		case err == nil:
			resp["updated_at"] = s.UpdatedAt
		case err != gorm.ErrRecordNotFound:
			LogError(c, err)
		}
	}
	Success(c, resp)
}
