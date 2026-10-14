// ansible_key.go：运维自动化——引擎状态与免密态势探测 + 平台公钥分发。
// Status 汇报 ansible 安装情况、平台密钥指纹与各 VM 的就绪/凭据态势；
// DeployKey 对存量 VM 补注入 authorized_key（幂等），成功即标记 ansible_ready。
package handler

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/ansible"
	"github.com/jiuzhao/vmops/service/secretbox"
	"github.com/jiuzhao/vmops/service/vmssh"
)

// Status GET /api/ansible/status —— 引擎探测 + 免密态势（P4-S4）。
func (h *AnsibleHandler) Status(c *gin.Context) {
	eng, err := ansible.Detect()
	if err != nil {
		Success(c, gin.H{
			"installed": false,
			"hint":      "宿主机未安装 ansible（pip install --user ansible 或系统包管理器安装后刷新）",
		})
		return
	}
	out := gin.H{
		"installed": true,
		"path":      eng.PlaybookPath,
		"version":   eng.Version,
	}
	// 免密态势：平台密钥指纹 + 已注入 VM 数 / 已托管凭据 VM 数（分发的候选池）
	if _, pub, kerr := ansible.EnsureKeyPair(config.DataPath("ansible")); kerr == nil {
		out["key_fingerprint"] = ansible.FingerprintPair(pub)
		out["pubkey"] = pub
	}
	var readyCnt, credCnt int64
	h.DB.Model(&model.VM{}).Where("ansible_ready = ?", true).Count(&readyCnt)
	h.DB.Model(&model.VMCredential{}).Count(&credCnt)
	out["ansible_ready_count"] = readyCnt
	out["cred_count"] = credCnt
	// per-VM 就绪标记（AU1）：目标选择处标注「无凭据」机器，选中即失败的坑前置暴露
	readyIDs := []uint{}
	h.DB.Model(&model.VM{}).Where("ansible_ready = ?", true).Pluck("id", &readyIDs)
	credIDs := []uint{}
	h.DB.Model(&model.VMCredential{}).Pluck("vm_id", &credIDs)
	out["ansible_ready_ids"] = readyIDs
	out["cred_vm_ids"] = credIDs
	Success(c, out)
}

// DeployKey POST /api/ansible/deploy-key —— 平台公钥分发（存量 VM 补注入）。
// targets 可省：默认全部「已托管凭据且未免密」的 VM。逐台经现有凭据通道 SSH
// 注入 authorized_key（grep -qxF 幂等，不重复写），成功即标记 ansible_ready。
func (h *AnsibleHandler) DeployKey(c *gin.Context) {
	var req struct {
		Targets []uint `json:"targets"`
	}
	_ = c.ShouldBindJSON(&req)
	if h.MasterSecret == "" {
		Fail(c, http.StatusServiceUnavailable, "凭据加密未初始化，请联系管理员配置主密钥后重启服务")
		return
	}
	_, pub, err := ansible.EnsureKeyPair(config.DataPath("ansible"))
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "平台密钥不可用", err)
		return
	}
	q := h.DB.Model(&model.VM{}).Where("ansible_ready = ?", false)
	if len(req.Targets) > 0 {
		q = q.Where("id IN ?", req.Targets)
	}
	var vms []model.VM
	if err := q.Find(&vms).Error; err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "读取虚拟机失败", err)
		return
	}
	if len(vms) == 0 {
		Success(c, gin.H{"ok": 0, "skipped": 0, "failed": []gin.H{}, "message": "没有需要分发的虚拟机（全部已免密或无目标）"})
		return
	}
	// 只处理有托管凭据的（无凭据的连口令通道都没有，无从注入）
	ids := make([]uint, 0, len(vms))
	for _, vm := range vms {
		ids = append(ids, vm.ID)
	}
	var creds []model.VMCredential
	h.DB.Where("vm_id IN ?", ids).Find(&creds)
	credByVM := map[uint]model.VMCredential{}
	for _, c := range creds {
		credByVM[c.VMID] = c
	}

	okCnt, skipCnt := 0, 0
	failed := []gin.H{}
	for _, vm := range vms {
		cred, has := credByVM[vm.ID]
		if !has {
			skipCnt++
			failed = append(failed, gin.H{"name": vm.Name, "reason": "未保存托管凭据"})
			continue
		}
		port := cred.Port
		if port <= 0 {
			port = 22
		}
		plain, derr := secretbox.OpenWithMaster(h.MasterSecret, cred.Salt, cred.PasswordEnc)
		if derr != nil {
			failed = append(failed, gin.H{"name": vm.Name, "reason": "凭据解密失败"})
			continue
		}
		opt, oerr := vmssh.NewOptions(vm.IP, vm.IP, port, cred.User, string(plain))
		if oerr != nil {
			failed = append(failed, gin.H{"name": vm.Name, "reason": "目标未通过安全校验"})
			continue
		}
		// 幂等注入：已存在同款公钥（整行精确匹配）不重复写
		cmd := fmt.Sprintf("mkdir -p ~/.ssh && chmod 700 ~/.ssh && touch ~/.ssh/authorized_keys && grep -qxF %q ~/.ssh/authorized_keys || echo %q >> ~/.ssh/authorized_keys; chmod 600 ~/.ssh/authorized_keys", pub, pub)
		if _, _, rerr := vmssh.Run(opt, cmd, 30*time.Second); rerr != nil {
			failed = append(failed, gin.H{"name": vm.Name, "reason": "SSH 注入失败"})
			continue
		}
		if uerr := h.DB.Model(&model.VM{}).Where("id = ?", vm.ID).
			Updates(map[string]interface{}{"ansible_ready": true, "ansible_user": cred.User}).Error; uerr != nil {
			log.Printf("[ansible] 标记 ansible_ready 失败 vm=%s: %v", vm.Name, uerr)
		}
		okCnt++
	}
	log.Printf("[ansible] 公钥分发完成 ok=%d skip=%d failed=%d from=%s", okCnt, skipCnt, len(failed), c.ClientIP())
	Success(c, gin.H{"ok": okCnt, "skipped": skipCnt, "failed": failed})
}
