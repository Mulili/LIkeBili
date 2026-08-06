// Package response 提供统一的 HTTP 响应工具。
// 约定：所有 Handler 只通过本包向外输出响应，保证接口返回结构一致。
package response

import (
	codeErrors "LikeBili/pkg/errors"
	"LikeBili/pkg/logger"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Response 统一响应体。
// Code/Message 是业务层语义（业务码与提示语）；HTTP 状态码由各函数调用时显式指定。
// Data 为业务数据；RequestID 用于链路追踪，方便日志排查。
type Response struct {
	Code      int    `json:"code"`                 // 业务状态码：200 表示成功，其余为业务错误码（见 pkg/errors）
	Message   string `json:"message"`              // 面向用户/前端的提示信息
	Data      any    `json:"data,omitempty"`       // 业务数据载荷，为空时省略该字段
	RequestID string `json:"request_id,omitempty"` // 请求追踪 ID，由中间件注入 gin.Context 后取出
}

// Success 以 HTTP 200 返回成功响应。
// 参数：c 为当前请求上下文，data 为要返回的业务数据（可为 nil）。
// 说明：调用后 Handler 应直接 return，响应已写出，不应再写第二次。
func Success(c *gin.Context, data any) {
	requestID, _ := c.Get("requestId")
	var rid string
	if requestID != nil {
		rid = requestID.(string)
	}
	c.JSON(http.StatusOK, Response{
		Code:      200,
		Message:   "成功",
		Data:      data,
		RequestID: rid,
	})
}

// Created 以 HTTP 201 返回"创建成功"响应。
// 适用场景：注册、创建资源等产生新实体的接口。
func Created(c *gin.Context, data any) {
	requestID, _ := c.Get("requestId")
	var rid string
	if requestID != nil {
		rid = requestID.(string)
	}
	c.JSON(http.StatusCreated, Response{
		Code:      201,
		Message:   "创建成功",
		Data:      data,
		RequestID: rid,
	})
}

// Error 以指定的 HTTP 状态码返回失败响应（不带业务数据）。
// 参数：httpstatus 为 HTTP 状态码（如 400/401/500），code 为业务错误码，message 为提示信息。
// 配合 pkg/errors 使用：Handler 用 codeErrors.From 取出 *Error 后，将 Code/Message 传入。
func Error(c *gin.Context, httpstatus, code int, message string) {
	requestID, _ := c.Get("requestId")
	var rid string
	if requestID != nil {
		rid = requestID.(string)
	}
	c.JSON(httpstatus, Response{
		Code:      code,
		Message:   message,
		RequestID: rid,
	})
}

// ErrorWithRequestID 与 Error 相同，但允许调用方显式指定 RequestID。
// 适用场景：错误发生在 RequestID 中间件之前、无法从 gin.Context 取到 requestId 时。
func ErrorWithRequestID(c *gin.Context, httpstatus, code int, message, requestID string) {
	c.JSON(httpstatus, Response{
		Code:      code,
		Message:   message,
		RequestID: requestID,
	})
}

// ErrorFrom 将任意 error 统一翻译为错误响应并写出。
// 内部用 codeErrors.From 从错误链中提取业务错误；提取不到（如 GORM/Redis 原始错误）时
// 兜底为"服务器内部错误"（50000 + HTTP 500）。
// 用法：Handler 遇到错误时一行调用即可，无需再写 errors.As 分支。
func ErrorFrom(c *gin.Context, operation string, err error) {
	if bizErr, ok := codeErrors.From(err); ok {
		Error(c, httpStatusForCode(bizErr.Code), bizErr.Code, bizErr.Message)
		return
	}
	//结构化
	logger.Error("operation failed", zap.String("operation", operation), zap.Error(err))
	Error(c, http.StatusInternalServerError, codeErrors.CodeInternal, "服务器内部错误")
}

// httpStatusForCode 将业务错误码映射为 HTTP 状态码。
// 采用逐条枚举：每个错误码都由其业务语义决定对应的 HTTP 状态，
// 因此以后新增错误码时，记得在这里补一条 case，否则会落入 default（500）。
func httpStatusForCode(code int) int {
	switch code {
	// ---- 注册/登录类（1xxxx）----
	// 冲突：数据已存在（用户名/邮箱重复，含并发注册竞态兜底）
	case codeErrors.CodeUsernameExists, codeErrors.CodeEmailExists, codeErrors.CodeUsernameOrEmailExists:
		return http.StatusConflict
	// 请求的内容无法通过校验：密码错误 / 两次密码不一致 / 参数校验失败
	case codeErrors.CodeWrongPassword, codeErrors.CodePasswordsNotMatch, codeErrors.CodeInvalid:
		return http.StatusBadRequest
	case codeErrors.CodeUserNotFound:
		return http.StatusNotFound
	case codeErrors.CodeUserIsBan, codeErrors.CodeForbidden:
		return http.StatusForbidden
	// ---- JWT 类（2xxxx）：未认证 ----
	case codeErrors.CodeTokenInvalid, codeErrors.CodeTokenExpired, codeErrors.CodeUnauthorized:
		return http.StatusUnauthorized
	// ---- 文件字节流类（3xxxx）----
	// 文件过大 → 413 Payload Too Large（语义最贴切）
	case codeErrors.CodeFileTooLarge:
		return http.StatusRequestEntityTooLarge
	// 格式不支持 / 文件为空 / 数量超限 → 请求本身不合法
	case codeErrors.CodeFileFormatInvalid, codeErrors.CodeFileEmpty, codeErrors.CodeFileTooMany:
		return http.StatusBadRequest
	// 写入对象存储失败 → 基础设施故障
	case codeErrors.CodeFileUploadFailed:
		return http.StatusInternalServerError
	// ---- 服务器内部错误（5xxxx）----
	case codeErrors.CodeInternal:
		return http.StatusInternalServerError
	// ---- 收藏夹类（6xxxx）----
	case codeErrors.CodeFavoriteNotFound:
		return http.StatusNotFound
	case codeErrors.CodeFavoriteForbidden:
		return http.StatusForbidden
	// ---- 未知错误码：保守按服务器错误处理 ----
	default:
		return http.StatusInternalServerError
	}
}
