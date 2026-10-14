// ansible_run.go：运维自动化——批量执行入口（adhoc 模块 / playbook 二选一）。
// 执行走 ansible_run 异步任务，输出随任务 Result 实时刷新（前端轮询任务详情）。
package handler

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/ansible"
	"github.com/jiuzhao/vmops/service/secretbox"
)

// Run POST /api/ansible/run —— 批量执行（operator+，路由组已挡）。
// adhoc：body {targets*[vm_id], module*(ping|command|shell), args?}
// playbook：body {targets*[vm_id], playbook*(id)}（S2）
func (h *AnsibleHandler) Run(c *gin.Context) {
	var req struct {
		Targets   []uint                 `json:"targets"`
		GroupID   uint                   `json:"group_id"`
		Module    string                 `json:"module"`
		Args      string                 `json:"args"`
		Playbook  string                 `json:"playbook"`
		ExtraVars map[string]interface{} `json:"extra_vars"` // survey 答案（值可为字符串或数字）
		Check     bool                   `json:"check"`      // 预演（--check，仅 playbook）
		Diff      bool                   `json:"diff"`       // 差异（--diff，仅 playbook）
		Tags      string                 `json:"tags"`       // 只跑指定标签（-t，仅 playbook）
		SkipTags  string                 `json:"skip_tags"`  // 跳过指定标签（仅 playbook）
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, "参数格式非法")
		return
	}
	// 目标来源：显式 targets 或主机组 group_id（至少其一；executor 侧再合并去重）
	if len(req.Targets) == 0 && req.GroupID == 0 {
		Fail(c, http.StatusBadRequest, "请选择目标虚拟机或主机组")
		return
	}
	if len(req.Targets) > 50 {
		Fail(c, http.StatusBadRequest, "单次执行最多 50 台")
		return
	}
	// 轻校验目标存在性（权威校验在 executor：运行态/IP/凭据逐台复核）
	if len(req.Targets) > 0 {
		var cnt int64
		if err := h.DB.Model(&model.VM{}).Where("id IN ?", req.Targets).Count(&cnt).Error; err != nil || cnt != int64(len(req.Targets)) {
			Fail(c, http.StatusBadRequest, "部分目标虚拟机不存在")
			return
		}
	}
	if req.GroupID > 0 {
		var g model.HostGroup
		if err := h.DB.First(&g, req.GroupID).Error; err != nil {
			Fail(c, http.StatusBadRequest, "主机组不存在")
			return
		}
	}

	targetDesc := strconv.Itoa(len(req.Targets)) + " 台"
	if req.GroupID > 0 && len(req.Targets) == 0 {
		targetDesc = "主机组"
	}
	payload := map[string]interface{}{}
	if len(req.Targets) > 0 {
		payload["targets"] = req.Targets
	}
	if req.GroupID > 0 {
		payload["group_id"] = req.GroupID
	}
	var title string
	if req.Playbook != "" {
		// playbook 分支：id 合法性 + 文件存在（executor 再按 path 执行）
		if !playbookIDRe.MatchString(req.Playbook) {
			Fail(c, http.StatusBadRequest, "playbook ID 非法")
			return
		}
		pbPath, _, ok := ansible.ResolvePlaybook(playbookDir(), req.Playbook)
		if !ok {
			Fail(c, http.StatusNotFound, "playbook 不存在")
			return
		}
		payload["playbook"] = req.Playbook
		// survey 参数处理：强校验答案 + secret 加密分流。
		//   - 非 secret → payload.extra_vars（executor 落盘为 --extra-vars @file）
		//   - secret    → 经 secretbox 加密后进 payload.secret_vars，明文不落库、不进命令行，
		//     执行时解密走环境变量（见 ansible.ApplySurveyAnswers）
		var surveyVars []ansible.SurveyVar
		if raw, rerr := os.ReadFile(pbPath); rerr == nil {
			if sv, present, perr := ansible.ParseSurvey(raw); perr == nil && present {
				surveyVars = sv
			}
		}
		answers := map[string]string{}
		for k, v := range req.ExtraVars {
			answers[k] = fmt.Sprintf("%v", v)
		}
		if len(surveyVars) > 0 {
			if verr := ansible.ValidateAnswers(surveyVars, answers); verr != nil {
				Fail(c, http.StatusBadRequest, verr.Error())
				return
			}
		}
		byName := ansible.SurveyByName(surveyVars)
		extraOut := map[string]string{}
		secretOut := map[string]map[string]string{}
		for name, val := range answers {
			if v, declared := byName[name]; declared && v.Type == ansible.SurveyTypeSecret {
				if h.MasterSecret == "" {
					Fail(c, http.StatusServiceUnavailable, "凭据加密未初始化，无法安全保存 secret 参数")
					return
				}
				enc, salt, eerr := secretbox.SealWithMaster(h.MasterSecret, val)
				if eerr != nil {
					ErrorWithMessage(c, http.StatusInternalServerError, "加密执行参数失败", eerr)
					return
				}
				secretOut[name] = map[string]string{"enc": enc, "salt": salt}
				continue
			}
			extraOut[name] = val
		}
		if len(extraOut) > 0 {
			payload["extra_vars"] = extraOut
		}
		if len(secretOut) > 0 {
			payload["secret_vars"] = secretOut
		}
		if req.Check {
			payload["check"] = true
		}
		if req.Diff {
			payload["diff"] = true
		}
		if strings.TrimSpace(req.Tags) != "" {
			payload["tags"] = strings.TrimSpace(req.Tags)
		}
		if strings.TrimSpace(req.SkipTags) != "" {
			payload["skip_tags"] = strings.TrimSpace(req.SkipTags)
		}
		title = "Ansible playbook " + req.Playbook + " → " + targetDesc
	} else {
		req.Module = strings.TrimSpace(req.Module)
		if !ansibleModules[req.Module] {
			Fail(c, http.StatusBadRequest, "模块仅支持 "+ansibleModuleList)
			return
		}
		if req.Module != "ping" && strings.TrimSpace(req.Args) == "" {
			Fail(c, http.StatusBadRequest, "模块 "+req.Module+" 需要填写执行参数")
			return
		}
		payload["module"] = req.Module
		payload["args"] = req.Args
		title = "Ansible " + req.Module + " → " + targetDesc
	}
	userID, username := taskUserFromContext(c)
	task, err := h.Tasks.Submit("ansible_run", title, payload, userID, username, "", nil)
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "提交任务失败", err)
		return
	}
	// 留痕：谁、从哪、对几台机器跑了什么（shell 参数/playbook 内容不进日志——可能含敏感内容）
	log.Printf("[ansible] 提交批量执行 playbook=%q module=%q targets=%d group=%d user=%s from=%s",
		req.Playbook, req.Module, len(req.Targets), req.GroupID, username, c.ClientIP())
	Accepted(c, "批量执行任务已提交", gin.H{"task_id": task.ID})
}
