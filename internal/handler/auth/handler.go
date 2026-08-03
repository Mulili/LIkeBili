package auth

import (
	modelsUser "LikeBili/internal/models/user"
	authService "LikeBili/internal/service/auth"
	codeErrors "LikeBili/pkg/errors"
	"LikeBili/pkg/response"
	"net/http"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	svc *authService.Service
}

func NewHandler(svc *authService.Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) Register(c *gin.Context) {
	var req modelsUser.RegisterReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, codeErrors.BadRequest, "请求参数格式错误")
		return
	}

}
