package handler

import (
	"errors"
	"log"
	"net/http"
	"strings"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/service/tasks"
)

// Success 返回成功响应，HTTP 200，统一格式 {"code":200,"message":"success","data":data}
func Success(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "success",
		"data":    data,
	})
}

// Fail 返回失败响应，使用给定 HTTP 状态码作为 code
func Fail(c *gin.Context, httpStatus int, message string) {
	c.JSON(httpStatus, gin.H{
		"code":    httpStatus,
		"message": message,
	})
}

// Created 返回成功响应，HTTP 200，但使用自定义 message
func Created(c *gin.Context, message string, data interface{}) {
	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": message,
		"data":    data,
	})
}

// Accepted 返回异步任务受理响应，HTTP 202，统一格式 {"code":202,"message":msg,"data":{task_id:...}}。
// 抽出目的：6 个异步任务提交 handler（创建/克隆/停止/删除虚拟机、克隆镜像、孤儿卷清理）
// 的 c.JSON 块逐字重复（冗余清理批次收敛），前端凭 task_id 轮询 GET /api/tasks/:id。
func Accepted(c *gin.Context, message string, data interface{}) {
	c.JSON(http.StatusAccepted, gin.H{
		"code":    http.StatusAccepted,
		"message": message,
		"data":    data,
	})
}

// ErrorResponse 统一处理操作失败：完整错误写入服务端日志，仅向前端返回友好消息。
// 内部细节（libvirt 原始错误、系统信息）不外泄。
func ErrorResponse(c *gin.Context, status int, err error) {
	LogError(c, err)
	Fail(c, status, friendlyMessage(err))
}

// ErrorWithMessage 统一处理操作失败：调用方已给出用户友好文案，完整错误写日志。
func ErrorWithMessage(c *gin.Context, status int, message string, err error) {
	LogError(c, err)
	Fail(c, status, message)
}

// SubmitTaskError 任务提交失败的统一响应。
// 队列繁忙（tasks.ErrQueueBusy）是「可稍后重试」的暂时状态，返回 503 + 明确文案；
// 其它错误仍按 500 + 中文兜底。此前各 handler 一律 ErrorWithMessage(..., "提交任务失败", err)，
// 把 manager 的「任务队列繁忙，请稍后重试」吞掉，用户只看到笼统失败。
func SubmitTaskError(c *gin.Context, err error) {
	if errors.Is(err, tasks.ErrQueueBusy) {
		Fail(c, http.StatusServiceUnavailable, "任务队列繁忙，请稍后重试")
		return
	}
	ErrorWithMessage(c, http.StatusInternalServerError, "提交任务失败", err)
}

// LogError 将完整错误写入服务端日志（含请求路径），供排查使用。
func LogError(c *gin.Context, err error) {
	if err == nil {
		return
	}
	log.Printf("[handler] 请求失败 path=%s err=%v", c.Request.URL.Path, err)
}

// friendlyMessage 从错误中提取用户友好消息。
// 约定 virt 层错误为 "中文描述: <内部细节>" 结构，取冒号前的中文段；
// 无中文描述时（如系统层错误）返回通用文案，避免内部细节外泄。
func friendlyMessage(err error) string {
	if err == nil {
		return "操作失败"
	}
	msg := err.Error()
	if i := strings.IndexAny(msg, "：:"); i > 0 {
		msg = msg[:i]
	}
	if !containsCJK(msg) {
		return "操作失败"
	}
	return msg
}

// containsCJK 判断字符串是否包含中日韩统一表意文字（用于识别友好中文消息）。
func containsCJK(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}
