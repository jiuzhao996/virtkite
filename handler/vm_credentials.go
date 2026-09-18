package handler

import (
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/secretbox"
	"gorm.io/gorm"
)

// maxSavedPasswordLen 保存口令的字节上限。PasswordEnc 列 size:500，base64 后长度
// = 4*⌈(明文+12 nonce+16 认证标签)/3⌉，256 字节明文 → 密文 384 字符，留足余量；
// 同时防止异常长入参白占存储。SSH 口令现实中远短于此。
const maxSavedPasswordLen = 256

// VMCredentialHandler SSH 凭据托管处理器（v3 批次 I）。
//
// 信任模型：明文凭据只在两处出现——① HTTP 边界进入（Save 表单，验完即加密）；
// ② 服务端内存解密（Get 只回掩码；ResolveFor 导出给文件管理/应用商店 use_saved 通道，
// 明文直接组装 vmssh.Options，不进日志、不进响应体、不落盘）。
// MasterSecret 未注入时全部端点 503 关闭（fail-closed：绝不落明文也不假解密）。
type VMCredentialHandler struct {
	DB           *gorm.DB
	MasterSecret string // 主密钥，见 NewVMCredentialHandler 的注入建议
}

// NewVMCredentialHandler 创建 SSH 凭据托管处理器。
//
// MasterSecret 建议注入 config.GlobalConfig.JWTSecretKey（main.go 挂载时传入）：
// 同源信任（都是「平台级、env 注入、泄露即全局失守」的密钥，信任边界完全一致），
// 且不新增一个一旦遗漏就回退成空值的敏感配置项。代价是轮换 JWT 密钥会使已存凭据
// 不可解密（ResolveFor 报错引导用户重存，fail-closed 不产出错误明文），属可接受取舍。
func NewVMCredentialHandler(db *gorm.DB, masterSecret string) *VMCredentialHandler {
	return &VMCredentialHandler{DB: db, MasterSecret: masterSecret}
}

// ready 功能闸：主密钥未注入（如旧配置热升级到本版本）时一切读写一律 503。
func (h *VMCredentialHandler) ready(c *gin.Context) bool {
	if h.MasterSecret == "" {
		Fail(c, http.StatusServiceUnavailable, "凭据加密未初始化，请联系管理员配置主密钥后重启服务")
		return false
	}
	return true
}

// findVMFor 公共前置：解析 :id → 取 VM → 授权可见性（对齐 vm_files.resolve 的
// 「查无此项」语义：非 admin 未持有效授权与不存在同响应，不泄露资产存在性）。
func (h *VMCredentialHandler) findVMFor(c *gin.Context) (*model.VM, bool) {
	id, ok := paramID(c, "id")
	if !ok {
		return nil, false
	}
	var vm model.VM
	if err := h.DB.First(&vm, id).Error; err != nil {
		Fail(c, http.StatusNotFound, "虚拟机不存在")
		return nil, false
	}
	if !vmVisible(c, h.DB, vm.ID) {
		Fail(c, http.StatusNotFound, "虚拟机不存在")
		return nil, false
	}
	return &vm, true
}

// saveReq POST /api/vms/:id/credentials 请求体。
type saveReq struct {
	User     string `json:"user"`
	Password string `json:"password"`
	Port     int    `json:"port"`
}

// Save 保存（或覆盖）VM 的 SSH 凭据。POST /api/vms/:id/credentials
// body: {user*, password*, port?}。盐每次随机重新生成——覆盖保存同一口令，密文也整体
// 换血（数据库层面无法对比出新旧口令是否相同）；vm_id 唯一索引，已存在即覆盖更新。
func (h *VMCredentialHandler) Save(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	vm, ok := h.findVMFor(c)
	if !ok {
		return
	}
	var req saveReq
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误（需提供 user 和 password）", err)
		return
	}
	if req.User == "" || req.Password == "" {
		Fail(c, http.StatusBadRequest, "请填写用户名和密码")
		return
	}
	if len(req.User) > 50 {
		Fail(c, http.StatusBadRequest, "用户名过长（最多 50 字符）")
		return
	}
	if len(req.Password) > maxSavedPasswordLen {
		Fail(c, http.StatusBadRequest, "密码过长（最多 256 字符）")
		return
	}
	port := req.Port
	if port == 0 {
		port = 22
	}
	if port < 1 || port > 65535 {
		Fail(c, http.StatusBadRequest, "端口须在 1-65535 之间")
		return
	}

	cipherB64, saltHex, err := secretbox.SealWithMaster(h.MasterSecret, req.Password)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	var rec model.VMCredential
	err = h.DB.Where("vm_id = ?", vm.ID).First(&rec).Error
	switch {
	case err == nil:
		// 已有凭据 → 覆盖更新（vm_id 唯一索引保证至多一条）
		if err := h.DB.Model(&rec).Updates(map[string]interface{}{
			"user":         req.User,
			"port":         port,
			"password_enc": cipherB64,
			"salt":         saltHex,
		}).Error; err != nil {
			ErrorResponse(c, http.StatusInternalServerError, err)
			return
		}
	case errors.Is(err, gorm.ErrRecordNotFound):
		rec = model.VMCredential{VMID: vm.ID, User: req.User, Port: port, PasswordEnc: cipherB64, Salt: saltHex}
		if err := h.DB.Create(&rec).Error; err != nil {
			ErrorResponse(c, http.StatusInternalServerError, err)
			return
		}
	default:
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	// 落库事件留痕（谁、从哪、给哪台机器存了凭据）；口令与掩码都不进日志
	log.Printf("[vm-cred] 保存凭据 vm=%s(%d) user=%s port=%d from=%s", vm.Name, vm.ID, req.User, port, c.ClientIP())
	Success(c, gin.H{"message": "已保存凭据", "configured": true, "user": req.User, "port": port})
}

// Get 查看 VM 已保存凭据的元信息。GET /api/vms/:id/credentials
// 响应 {configured, user, port, password_masked, updated_at}——⚠️ 绝不含明文，
// 掩码仅保留前 1 后 1 字符（rune 级，中文口令不乱码）。
func (h *VMCredentialHandler) Get(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	vm, ok := h.findVMFor(c)
	if !ok {
		return
	}
	var rec model.VMCredential
	err := h.DB.Where("vm_id = ?", vm.ID).First(&rec).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// 未配置属正常状态（非错误）：给前端表单预填默认值
		Success(c, gin.H{"configured": false, "user": "", "port": 22, "password_masked": ""})
		return
	}
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	// 掩码需要明文才能取前 1 后 1 字符；解密失败（如主密钥已轮换）不拖垮查询，
	// 退回定长 **** 并留痕——configured=true 仍成立，真正暴露问题的是后续 use_saved 解密
	masked := "****"
	if plain, derr := secretbox.OpenWithMaster(h.MasterSecret, rec.Salt, rec.PasswordEnc); derr == nil {
		masked = maskPassword(string(plain))
	} else {
		log.Printf("[vm-cred] 掩码解密失败（主密钥可能已轮换）vm=%s(%d) err=%v", vm.Name, vm.ID, derr)
	}
	Success(c, gin.H{
		"configured":      true,
		"user":            rec.User,
		"port":            rec.Port,
		"password_masked": masked,
		"updated_at":      rec.UpdatedAt,
	})
}

// Delete 删除 VM 已保存的凭据。DELETE /api/vms/:id/credentials
func (h *VMCredentialHandler) Delete(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	vm, ok := h.findVMFor(c)
	if !ok {
		return
	}
	res := h.DB.Where("vm_id = ?", vm.ID).Delete(&model.VMCredential{})
	if res.Error != nil {
		ErrorResponse(c, http.StatusInternalServerError, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		Fail(c, http.StatusNotFound, "该虚拟机尚未配置凭据")
		return
	}
	log.Printf("[vm-cred] 删除凭据 vm=%s(%d) from=%s", vm.Name, vm.ID, c.ClientIP())
	Success(c, gin.H{"message": "已删除保存的凭据"})
}

// ResolveFor 取回 VM 已保存的 SSH 凭据明文——导出给其他 handler 内部使用：
// 文件管理 / 应用商店的 use_saved=true 通道由后端解密直接组装 vmssh.Options，
// 前端无需重输口令。⚠️ 明文只在服务端内存流转：不写日志、不进响应体、不落盘。
//
// 约定（与 vm_files.resolve 一致）：ok=false 时错误响应已写好，调用方直接 return——
//
//	user, port, password, ok := vmCredHandler.ResolveFor(c, vm.ID)
//	if !ok {
//	    return
//	}
//	opts := vmssh.Options{Host: ip, Port: port, User: user, Password: password}
func (h *VMCredentialHandler) ResolveFor(c *gin.Context, vmID uint) (user string, port int, password string, ok bool) {
	if !h.ready(c) {
		return "", 0, "", false
	}
	var vm model.VM
	// db.First 自动过滤软删 VM；vmID 来自调用方已解析的 uint（非路径原串），无注入面
	if err := h.DB.First(&vm, vmID).Error; err != nil {
		Fail(c, http.StatusNotFound, "虚拟机不存在")
		return "", 0, "", false
	}
	if !vmVisible(c, h.DB, vm.ID) {
		Fail(c, http.StatusNotFound, "虚拟机不存在")
		return "", 0, "", false
	}
	var rec model.VMCredential
	if err := h.DB.Where("vm_id = ?", vm.ID).First(&rec).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			Fail(c, http.StatusBadRequest, "该虚拟机尚未配置保存的凭据，请先在凭据面板保存或手动输入")
			return "", 0, "", false
		}
		ErrorResponse(c, http.StatusInternalServerError, err)
		return "", 0, "", false
	}
	plain, err := secretbox.OpenWithMaster(h.MasterSecret, rec.Salt, rec.PasswordEnc)
	if err != nil {
		// 主密钥轮换 / 记录损坏时 fail-closed：绝不返回半截或错误明文，引导用户重存
		ErrorWithMessage(c, http.StatusInternalServerError, "凭据解密失败，请重新保存该虚拟机的凭据后再使用", err)
		return "", 0, "", false
	}
	return rec.User, rec.Port, string(plain), true
}

// maskPassword 生成展示用掩码：≤2 字符一律定长 ****（防长度泄露），>2 保留前 1 后 1 字符。
// 纯函数，供单测。
func maskPassword(p string) string {
	r := []rune(p)
	if len(r) <= 2 {
		return "****"
	}
	return string(r[0]) + "****" + string(r[len(r)-1])
}
