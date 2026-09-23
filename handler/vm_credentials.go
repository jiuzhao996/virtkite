package handler

import (
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/config"
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
	MasterSecret string // 主密钥，来源见 CredentialMasterSecret（独立配置项，不与 JWT 密钥共用）
}

// NewVMCredentialHandler 创建 SSH 凭据托管处理器。
//
// MasterSecret 一律由 CredentialMasterSecret() 提供（routes.go 挂载时传入），不要就地传
// config.GlobalConfig.JWTSecretKey——那会把凭据生命周期绑死在 JWT 密钥上（见下方说明）。
func NewVMCredentialHandler(db *gorm.DB, masterSecret string) *VMCredentialHandler {
	return &VMCredentialHandler{DB: db, MasterSecret: masterSecret}
}

// CredentialMasterSecret 选取凭据加密主密钥（每次服务启动调用一次，注入 VMCredentialHandler）。
//
// 优先级：CREDENTIAL_MASTER_KEY（独立配置项，新部署必配） > JWT_SECRET_KEY（历史行为，兼容回落）。
//
// 为什么必须解耦：早期实现直接复用 JWT 主密钥，于是「按安全规范轮换 JWT_SECRET_KEY」
// 会连带废掉库里全部历史凭据——ResolveFor 全线解密失败，报错却表现为「凭据不存在或已损坏」，
// 极易被误判成数据损坏而触发重装/重建，造成真实损失。二者信任边界虽相同（平台级 env 密钥），
// 但生命周期必须各自独立。
//
// 回落路径绝不静默：只打 ⚠️ 日志不足以阻止事故，但至少让「本部署仍是绑定态」这件事
// 在每次启动的日志里可见，而不是等轮换那天才炸。
//
// 迁移（已从回落态切到独立密钥的存量部署）：历史凭据仍是旧密钥加密的，需跑一次重加密——
// go run ./scripts/credential-rekey --apply（默认 dry-run，详见该文件的用法说明）。
func CredentialMasterSecret() string {
	if key := config.GlobalConfig.CredentialMasterKey; key != "" {
		// 已独立配置：此后轮换 JWT_SECRET_KEY 不会动摇任何已存凭据
		return key
	}
	log.Printf("⚠️⚠️ [vm-cred] 未配置 CREDENTIAL_MASTER_KEY，VM 凭据仍复用 JWT_SECRET_KEY 加密：此时轮换 JWT_SECRET_KEY 会导致历史凭据全部解密失败（表现为「凭据不存在或已损坏」）。请尽快配置独立的 CREDENTIAL_MASTER_KEY（首次可直接复用当前 JWT_SECRET_KEY 的值，配置后二者即解耦）")
	return config.GlobalConfig.JWTSecretKey
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

// resolveVMForCred 凭据通道公共前置：取 VM → 授权可见性（对齐 vm_files.resolve 的
// 「查无此项」语义：非 admin 未持有效授权与不存在同响应，不泄露资产存在性）。
// vmID 来自调用方已解析的 uint（非路径原串），无注入面；db.First 自动过滤软删 VM。
func (h *VMCredentialHandler) resolveVMForCred(c *gin.Context, vmID uint) (*model.VM, bool) {
	var vm model.VM
	if err := h.DB.First(&vm, vmID).Error; err != nil {
		Fail(c, http.StatusNotFound, "虚拟机不存在")
		return nil, false
	}
	if !vmVisible(c, h.DB, vm.ID) {
		Fail(c, http.StatusNotFound, "虚拟机不存在")
		return nil, false
	}
	return &vm, true
}

// CredentialRefFor 取该 VM 已托管凭据的「引用」：只回记录 ID / 用户名 / 端口，绝不回明文。
//
// 用途：异步任务（app_install）把引用 ID 写进 task.payload，由 executor 在服务端解密取用，
// 明文口令因此既不进 HTTP 响应、也不落库（tasks.payload 随任务入库，是泄露面）。
// 与 ResolveFor 的分工：ResolveFor 给同步通道（当场 SSH 拨号），本函数给异步通道（延迟拨号）。
//
// 约定同 ResolveFor：ok=false 时错误响应已写好，调用方直接 return。
func (h *VMCredentialHandler) CredentialRefFor(c *gin.Context, vmID uint) (credID uint, user string, port int, ok bool) {
	if !h.ready(c) {
		return 0, "", 0, false
	}
	vm, ok := h.resolveVMForCred(c, vmID)
	if !ok {
		return 0, "", 0, false
	}
	var rec model.VMCredential
	if err := h.DB.Where("vm_id = ?", vm.ID).First(&rec).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			Fail(c, http.StatusBadRequest, "该虚拟机尚未配置保存的凭据，请先在凭据面板保存后再安装")
			return 0, "", 0, false
		}
		ErrorResponse(c, http.StatusInternalServerError, err)
		return 0, "", 0, false
	}
	return rec.ID, rec.User, rec.Port, true
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
	vm, ok := h.resolveVMForCred(c, vmID)
	if !ok {
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
