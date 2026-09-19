package handler

// cloud-init 配置模板 CRUD（v3 批次 L）。
//
// 模板 = 一份可复用的 virt.CloudInitSpec（主机名/用户/密码/SSH 公钥/网络模式/IP/网关/DNS），
// 供创建向导「套用模板」一键回填、并把当前配置「保存为模板」。
// 入库时白名单校验后序列化为 JSON 字符串（model.CloudInitTemplate.Spec），
// 出库时反序列化为对象返回——前端拿到的 spec 始终是对象，可直接回填表单。
//
// 路由建议挂 OperatorMiddleware（模板属建机操作语义，operator 可用；viewer 无读写场景）：
//
//	cit := api.Group("/cloud-init-templates")
//	cit.Use(middleware.OperatorMiddleware())
//	{
//	    cit.GET("", h.List)
//	    cit.GET("/:id", h.Get)
//	    cit.POST("", h.Create)
//	    cit.PUT("/:id", h.Update)
//	    cit.DELETE("/:id", h.Delete)
//	}

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/virt"
	"gorm.io/gorm"
)

// cloud-init 网络模式取值（与 virt.CloudInitSpec 注释及创建向导保持一致）
const (
	ciNetModeDHCP   = "dhcp"
	ciNetModeStatic = "static"
)

var (
	// hostnameRe 主机名：字母/数字开头结尾，中间允许点、连字符、下划线
	hostnameRe = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9._-]*[a-zA-Z0-9])?$`)
	// linuxUserRe Linux 用户名：字母/数字开头，中间允许下划线、连字符、点
	linuxUserRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)
)

// CloudInitTemplateHandler cloud-init 配置模板处理器
type CloudInitTemplateHandler struct {
	DB *gorm.DB
}

// NewCloudInitTemplateHandler 创建 cloud-init 模板处理器。
func NewCloudInitTemplateHandler(db *gorm.DB) *CloudInitTemplateHandler {
	return &CloudInitTemplateHandler{DB: db}
}

// cloudInitTemplateItem 对外契约：Spec 以反序列化后的对象返回（前端直接回填表单），
// 模型里的原始 JSON 字符串不外泄（model.CloudInitTemplate.Spec 已标 json:"-"）。
type cloudInitTemplateItem struct {
	ID          uint               `json:"id"`
	Name        string             `json:"name"`
	Spec        virt.CloudInitSpec `json:"spec"`
	Description string             `json:"description"`
	CreatedAt   time.Time          `json:"created_at"`
	UpdatedAt   time.Time          `json:"updated_at"`
}

// cloudInitTemplateReq 创建/更新请求体。Spec 用 RawMessage 接住原始 JSON，
// 经 parseTemplateSpec（DisallowUnknownFields 白名单）+ validateTemplateSpec 校验后落库。
type cloudInitTemplateReq struct {
	Name        string          `json:"name"`
	Spec        json.RawMessage `json:"spec"`
	Description string          `json:"description"`
}

// List GET /api/cloud-init-templates：全部模板，Spec 反序列化为对象。
func (h *CloudInitTemplateHandler) List(c *gin.Context) {
	var rows []model.CloudInitTemplate
	if err := h.DB.Order("id").Find(&rows).Error; err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	items := make([]cloudInitTemplateItem, 0, len(rows))
	for _, row := range rows {
		var spec virt.CloudInitSpec
		if err := json.Unmarshal([]byte(row.Spec), &spec); err != nil {
			// 存量数据理论上都经校验写入；万一损坏，降级为空配置并留日志，不拖垮整个列表
			log.Printf("[cloud-init-templates] 模板 %d(%s) Spec 解析失败，按空配置返回: %v", row.ID, row.Name, err)
		}
		items = append(items, newTemplateItem(row, spec))
	}
	Success(c, gin.H{"total": len(items), "items": items})
}

// Get GET /api/cloud-init-templates/:id：单个模板（Spec 为对象）。
func (h *CloudInitTemplateHandler) Get(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	row, err := h.getTemplateRow(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			Fail(c, http.StatusNotFound, "模板不存在")
			return
		}
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	var spec virt.CloudInitSpec
	if err := json.Unmarshal([]byte(row.Spec), &spec); err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "模板配置数据损坏，请删除后重建", err)
		return
	}
	Success(c, newTemplateItem(*row, spec))
}

// Create POST /api/cloud-init-templates：新建模板（name 必填 + spec 白名单校验）。
func (h *CloudInitTemplateHandler) Create(c *gin.Context) {
	var req cloudInitTemplateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	row, spec, err := buildTemplateRow(model.CloudInitTemplate{}, req)
	if err != nil {
		Fail(c, http.StatusBadRequest, err.Error()) // 校验错误为固定中文文案，可直接回显
		return
	}
	var count int64
	if err := h.DB.Model(&model.CloudInitTemplate{}).Where("name = ?", row.Name).Count(&count).Error; err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	if count > 0 {
		Fail(c, http.StatusConflict, "同名模板已存在")
		return
	}
	if err := h.DB.Create(&row).Error; err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Created(c, "模板已创建", newTemplateItem(row, spec))
}

// Update PUT /api/cloud-init-templates/:id：整体更新（name/spec/description 全量提交）。
func (h *CloudInitTemplateHandler) Update(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	row, err := h.getTemplateRow(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			Fail(c, http.StatusNotFound, "模板不存在")
			return
		}
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	var req cloudInitTemplateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	updated, spec, err := buildTemplateRow(*row, req)
	if err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	var count int64
	if err := h.DB.Model(&model.CloudInitTemplate{}).
		Where("name = ? AND id <> ?", updated.Name, id).Count(&count).Error; err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	if count > 0 {
		Fail(c, http.StatusConflict, "同名模板已存在")
		return
	}
	if err := h.DB.Save(&updated).Error; err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, newTemplateItem(updated, spec))
}

// Delete DELETE /api/cloud-init-templates/:id：删除模板（仅删配置记录，不影响任何虚拟机）。
func (h *CloudInitTemplateHandler) Delete(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}
	res := h.DB.Delete(&model.CloudInitTemplate{}, id)
	if res.Error != nil {
		ErrorResponse(c, http.StatusInternalServerError, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		Fail(c, http.StatusNotFound, "模板不存在")
		return
	}
	Success(c, gin.H{"message": "模板已删除"})
}

// getTemplateRow 按主键取模板行（已过 paramID 解析，走参数化查询）。
func (h *CloudInitTemplateHandler) getTemplateRow(id uint) (*model.CloudInitTemplate, error) {
	var row model.CloudInitTemplate
	if err := h.DB.First(&row, id).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// buildTemplateRow 把请求体规整为模型行（纯校验逻辑，DB 无关，可单测）。
// 同时返回规整后的 spec 对象，供响应 DTO 直接使用，免去回读再解析。
func buildTemplateRow(row model.CloudInitTemplate, req cloudInitTemplateReq) (model.CloudInitTemplate, virt.CloudInitSpec, error) {
	name, err := validateTemplateName(req.Name)
	if err != nil {
		return row, virt.CloudInitSpec{}, err
	}
	desc := strings.TrimSpace(req.Description)
	if utf8.RuneCountInString(desc) > 500 {
		return row, virt.CloudInitSpec{}, errors.New("描述不能超过 500 个字符")
	}
	spec, err := parseTemplateSpec(req.Spec)
	if err != nil {
		return row, virt.CloudInitSpec{}, err
	}
	if err := validateTemplateSpec(&spec); err != nil {
		return row, virt.CloudInitSpec{}, err
	}
	b, err := json.Marshal(spec)
	if err != nil {
		return row, virt.CloudInitSpec{}, errors.New("模板配置序列化失败")
	}
	row.Name = name
	row.Spec = string(b)
	row.Description = desc
	return row, spec, nil
}

// validateTemplateName 规整并校验模板名（纯函数）。固定中文错误可直接回显。
func validateTemplateName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", errors.New("模板名不能为空")
	}
	if utf8.RuneCountInString(name) > 100 {
		return "", errors.New("模板名不能超过 100 个字符")
	}
	return name, nil
}

// parseTemplateSpec 解析 spec JSON 对象并做字段白名单过滤（纯函数）。
// DisallowUnknownFields 把未知键整体拒绝：spec 会原样成为建机 payload 的 cloud_init
// 并最终写进 seed ISO 文本，多余字段要么失效要么被带进渲染，白名单是唯一防线。
func parseTemplateSpec(raw json.RawMessage) (virt.CloudInitSpec, error) {
	var spec virt.CloudInitSpec
	if len(bytes.TrimSpace(raw)) == 0 {
		return spec, errors.New("spec 不能为空，须为 JSON 对象")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&spec); err != nil {
		return virt.CloudInitSpec{}, errors.New("spec 含不支持的字段，仅允许 hostname/user/password/ssh_key/net_mode/ip/gateway/dns")
	}
	return spec, nil
}

// validateTemplateSpec 校验并规整 spec 各字段（纯函数，就地修改）。
// 固定中文错误可直接回显给前端。
func validateTemplateSpec(spec *virt.CloudInitSpec) error {
	spec.Hostname = strings.TrimSpace(spec.Hostname)
	if utf8.RuneCountInString(spec.Hostname) > 100 {
		return errors.New("hostname 不能超过 100 个字符")
	}
	if spec.Hostname != "" && !hostnameRe.MatchString(spec.Hostname) {
		return errors.New("hostname 仅允许字母、数字开头结尾，中间可含点、连字符、下划线")
	}

	spec.User = strings.TrimSpace(spec.User)
	if utf8.RuneCountInString(spec.User) > 64 {
		return errors.New("user 不能超过 64 个字符")
	}
	if spec.User != "" && !linuxUserRe.MatchString(spec.User) {
		return errors.New("user 仅允许字母、数字开头，中间可含下划线、连字符、点")
	}

	// 密码与 SSH 公钥会按原文写进 user-data 文本（见 virt.GenerateSeedISO），
	// 换行/控制字符会破坏 YAML 结构甚至注入任意 cloud-config 键，整体拒绝
	if err := rejectControlText(spec.Password, "password", 128); err != nil {
		return err
	}
	if err := rejectControlText(spec.SSHKey, "ssh_key", 2000); err != nil {
		return err
	}

	switch spec.NetMode {
	case "", ciNetModeDHCP, ciNetModeStatic:
	default:
		return errors.New("net_mode 仅支持 dhcp 或 static")
	}
	if spec.NetMode == "" {
		spec.NetMode = ciNetModeDHCP // 归一化：空即 DHCP（GenerateSeedISO 非 static 分支的语义）
	}

	spec.IP = strings.TrimSpace(spec.IP)
	spec.Gateway = strings.TrimSpace(spec.Gateway)
	if spec.NetMode == ciNetModeStatic && (spec.IP == "" || spec.Gateway == "") {
		return errors.New("静态网络模式下 ip 与 gateway 必填")
	}
	if spec.IP != "" && !validIPv4(spec.IP) {
		return errors.New("ip 必须为合法 IPv4 地址")
	}
	if spec.Gateway != "" && !validIPv4(spec.Gateway) {
		return errors.New("gateway 必须为合法 IPv4 地址")
	}

	if len(spec.DNS) > 8 {
		return errors.New("dns 最多 8 个")
	}
	dns := make([]string, 0, len(spec.DNS))
	seen := make(map[string]bool, len(spec.DNS))
	for _, d := range spec.DNS {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		if !validIPv4(d) {
			return fmt.Errorf("dns 必须为合法 IPv4 地址：%s", d)
		}
		if !seen[d] {
			seen[d] = true
			dns = append(dns, d)
		}
	}
	spec.DNS = dns
	return nil
}

// rejectControlText 文本字段通用校验：禁换行与控制字符（写进 cloud-config 文本会破坏结构），限长。
func rejectControlText(val, field string, maxRunes int) error {
	if val == "" {
		return nil
	}
	if strings.ContainsAny(val, "\n\r") || strings.ContainsFunc(val, unicode.IsControl) {
		return fmt.Errorf("%s 不能包含换行或控制字符", field)
	}
	if utf8.RuneCountInString(val) > maxRunes {
		return fmt.Errorf("%s 不能超过 %d 个字符", field, maxRunes)
	}
	return nil
}

// newTemplateItem 模型行 + 已解析 spec → 对外 DTO。
func newTemplateItem(row model.CloudInitTemplate, spec virt.CloudInitSpec) cloudInitTemplateItem {
	return cloudInitTemplateItem{
		ID:          row.ID,
		Name:        row.Name,
		Spec:        spec,
		Description: row.Description,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
	}
}
