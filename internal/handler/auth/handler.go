// Package auth 提供认证相关的 HTTP 处理层（Handler）。
// 职责：解析请求参数 → 校验 → 调用 Service 层 → 统一响应。
// 约定：Handler 不写业务逻辑，只做"请求→响应"的翻译；错误统一走 response 包。
package auth

import (
	"LikeBili/internal/middleware"
	modelsUser "LikeBili/internal/models/user"
	authService "LikeBili/internal/service/auth"
	codeErrors "LikeBili/pkg/errors"
	extracttoken "LikeBili/pkg/extracttoken"
	"LikeBili/pkg/logger"
	"LikeBili/pkg/response"
	vt "LikeBili/pkg/validator"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Handler 认证模块的 Handler，持有 Service 依赖与 token 有效期。
// tokenTTL 用于计算 Cookie 的 maxAge，保证 Cookie 生命周期与 token 有效期一致。
type Handler struct {
	svc      *authService.Service
	tokenTTL time.Duration
}

// NewHandler 构造 Handler，注入 Service 与 token 有效期。
func NewHandler(svc *authService.Service, tokenTTL time.Duration) *Handler {
	return &Handler{svc: svc, tokenTTL: tokenTTL}
}

// Register 处理注册请求。
// 流程：绑定 JSON → 结构体校验（validate tag）→ Service 注册（含并发竞态兜底）→ 种 token Cookie（注册即登录）→ 201。
func (h *Handler) Register(c *gin.Context) {
	var req modelsUser.RegisterReq
	// 第 1 步：把请求体 JSON 反序列化进 DTO；失败说明不是合法 JSON
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, codeErrors.BadRequest, "请求参数格式错误")
		return
	}
	// 第 2 步：按 DTO 的 validate tag 做业务校验（如 username/password 正则）
	if err := vt.Struct(&req); err != nil {
		response.Error(c, http.StatusBadRequest, codeErrors.CodeInvalid, vt.TranslateError(err))
		return
	}
	// 第 3 步：调 Service 注册；成功返回用户信息与 token
	resp, token, err := h.svc.Register(c.Request.Context(), &req)
	if err != nil {
		operation := "Register"
		response.ErrorFrom(c, operation, err) // 统一错误出口：自动翻译业务码 + 日志
		return
	}
	h.setCookie(c, token) // 注册即登录：把 token 写进 HttpOnly Cookie
	response.Created(c, resp)
}

// Login 处理登录请求。
// 流程：绑定 JSON → 简单判空 → Service 登录（校验密码、写入 Redis）→ 种 Cookie → 200。
func (h *Handler) Login(c *gin.Context) {
	var req modelsUser.LoginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, codeErrors.BadRequest, "请求参数格式错误")
		return
	}
	// 登录参数只做非空判断：账号兼容用户名/邮箱两种格式，正则校验放注册处
	if req.Account == "" || req.Password == "" {
		response.Error(c, http.StatusBadRequest, codeErrors.BadRequest, "账号或密码不能为空")
		return
	}
	resp, token, err := h.svc.Login(c.Request.Context(), req.Account, req.Password)
	if err != nil {
		operation := "Login"
		response.ErrorFrom(c, operation, err)
		return
	}
	h.setCookie(c, token)
	response.Success(c, resp)
}

// Logout 处理登出请求。
// 依赖鉴权中间件注入的 userId（c.Set("userId", ...)）；登出即删除 Redis 中的 token 并清除 Cookie。
func (h *Handler) Logout(c *gin.Context) {
	userID, exist := c.Get("userId") // 从中间件注入的上下文取当前用户
	operation := "Logout"
	if !exist {
		// 没有 userId 说明未登录，直接拒绝（防御性兜底）
		response.Error(c, http.StatusUnauthorized, codeErrors.Unauthorized, codeErrors.ErrorUnauthorized.Message)
		return
	}
	if err := h.svc.Logout(c.Request.Context(), userID.(uint)); err != nil {
		// 登出失败属于异常：记日志 + 返回 500
		logger.Error("operation failed", zap.String("operation", operation), zap.Error(err))
		response.Error(c, http.StatusInternalServerError, codeErrors.Internal, codeErrors.ErrorInternal.Message)
		return
	}
	h.setCookie(c, "") // 传入空 token = 清除 Cookie
	response.Success(c, "")
}

// FindMe 查询当前登录用户的信息。
// 与 Logout 一样依赖中间件注入 userId。
func (h *Handler) FindMe(c *gin.Context) {
	userID, exist := c.Get("userId")
	operation := "FindMe"
	if !exist {
		response.Error(c, http.StatusUnauthorized, codeErrors.Unauthorized, codeErrors.ErrorUnauthorized.Message)
		return
	}
	resp, err := h.svc.FindMe(c.Request.Context(), userID.(uint))
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}
	response.Success(c, resp)
}

// Refresh 刷新 token 有效期。
// 不走鉴权中间件（允许已过期的 token 换新）；提取 token 后交给 Service 在 Redis 中续期。
func (h *Handler) Refresh(c *gin.Context) {
	token := extracttoken.ExtractToken(c) // 从 Cookie 或 Authorization header 提取 token 字符串
	operation := "Refresh"
	if token == "" {
		response.Error(c, http.StatusUnauthorized, codeErrors.Unauthorized, codeErrors.ErrorUnauthorized.Message)
		return
	}
	newToken, err := h.svc.Refresh(c.Request.Context(), token)
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}
	h.setCookie(c, newToken) // 用新 token 覆盖 Cookie
	response.Success(c, "")
}

// CSRF 生成 CSRF token 并种入 Cookie，返回给前端。
// 前端读取返回值后，在写请求的 X-CSRF-Token 头中携带，由 CSRF 中间件校验。
func (h *Handler) CSRF(c *gin.Context) {
	csrfToken, err := middleware.SetCSRFCookie(c)
	operation := "CSRF"
	if err != nil {
		logger.Error("operation failed", zap.String("operation", operation), zap.Error(err))
		response.Error(c, http.StatusInternalServerError, codeErrors.Internal, codeErrors.ErrorInternal.Message)
		return
	}
	response.Success(c, csrfToken)
}

// ==================辅助函数================

// setCookie 将 token 写入 HttpOnly Cookie。
// token 为空时 maxAge=-1，即删除 Cookie（登出场景）；maxAge 与 tokenTTL 保持一致。
func (h *Handler) setCookie(c *gin.Context, token string) {
	maxAge := int(h.tokenTTL.Seconds()) // Cookie 存活秒数 = token 有效期
	if token == "" {
		maxAge = -1 // -1 表示删除 Cookie
	}
	c.SetSameSite(http.SameSiteLaxMode)                       // 防 CSRF：跨站请求不携带 Cookie
	c.SetCookie("token", token, maxAge, "/", "", false, true) // httpOnly=true：JS 无法读取，防 XSS 窃取
}
