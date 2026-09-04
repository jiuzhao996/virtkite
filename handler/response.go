package handler

import (
	"log"
	"net/http"
	"strings"
	"unicode"

	"github.com/gin-gonic/gin"
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
