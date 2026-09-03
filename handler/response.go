package handler

import (
	"net/http"

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
