package auth

import (
	modelsUser "LikeBili/internal/models/user"
	authService "LikeBili/internal/service/auth"
	extracttoken "LikeBili/pkg/ExtractToken"
	codeErrors "LikeBili/pkg/errors"
	"LikeBili/pkg/logger"
	"LikeBili/pkg/response"
	vt "LikeBili/pkg/validator"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type Handler struct {
	svc      *authService.Service
	tokenTTL time.Duration
}

func NewHandler(svc *authService.Service, tokenTTL time.Duration) *Handler {
	return &Handler{svc: svc, tokenTTL: tokenTTL}
}

func (h *Handler) Register(c *gin.Context) {
	var req modelsUser.RegisterReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, codeErrors.BadRequest, "请求参数格式错误")
		return
	}
	//结构体校验，看是否符合username或password的要求
	if err := vt.Struct(&req); err != nil {
		response.Error(c, http.StatusBadRequest, codeErrors.CodeInvalid, vt.TranslateError(err))
		return
	}
	resp, token, err := h.svc.Register(c.Request.Context(), &req)
	if err != nil {
		operation := "Register"
		response.ErrorFrom(c, operation, err)
		return
	}
	h.setCookie(c, token)
	response.Created(c, resp)
}

func (h *Handler) Login(c *gin.Context) {
	var req modelsUser.LoginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, codeErrors.BadRequest, "请求参数格式错误")
		return
	}
	if req.Account == "" || req.Password == "" {
		response.Error(c, http.StatusBadRequest, codeErrors.BadRequest, "账号或密码不能为空")
		return
	}
	resp, token, err := h.svc.Login(c.Request.Context(), req.Account, req.Password)
	if err != nil {
		opration := "Login"
		response.ErrorFrom(c, opration, err)
		return
	}
	h.setCookie(c, token)
	response.Success(c, resp)
}

// 登出
func (h *Handler) Logout(c *gin.Context) {
	userID, exist := c.Get("userId")
	operation := "Logout"
	if !exist {
		response.Error(c, http.StatusUnauthorized, codeErrors.Unauthorized, codeErrors.ErrorUnauthorized.Message)
		return
	}
	if err := h.svc.Logout(c.Request.Context(), userID.(uint)); err != nil {
		logger.Error("operation failed", zap.String("operation", operation), zap.Error(err))
		response.Error(c, http.StatusInternalServerError, codeErrors.Internal, codeErrors.ErrorInternal.Message)
		return
	}
	h.setCookie(c, "")
	response.Success(c, "")
}

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

func (h *Handler) Refresh(c *gin.Context) {
	token := extracttoken.ExtractToken(c)
	operation := "Refresh"
	if token == "" {
		response.Error(c, http.StatusUnauthorized, codeErrors.Unauthorized, codeErrors.ErrorUnauthorized.Message)
		return
	}
	newToken, err := h.svc.Refresh(c, token)
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}
	h.setCookie(c, newToken)
	response.Success(c, "")
}

// ==================辅助函数================
func (h *Handler) setCookie(c *gin.Context, token string) {
	maxAge := int(h.tokenTTL.Seconds())
	if token == "" {
		maxAge = -1 // 清除 Cookie
	}
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("token", token, maxAge, "/", "", false, true)
}
