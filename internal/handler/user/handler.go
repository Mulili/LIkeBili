package user

import (
	"LikeBili/internal/middleware"
	modelsUser "LikeBili/internal/models/user"
	userservice "LikeBili/internal/service/user"
	codeErrors "LikeBili/pkg/errors"
	"LikeBili/pkg/param"
	"LikeBili/pkg/response"
	"LikeBili/pkg/storage"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	svc     *userservice.Service
	storage *storage.MinIO
}

func NewHandler(svc *userservice.Service, storage *storage.MinIO) *Handler {
	return &Handler{svc: svc, storage: storage}
}

func (h *Handler) GetUser(c *gin.Context) {
	id, err := param.Parse[uint](c, "id")
	operation := "GetUser"
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}
	resp, err := h.svc.GetUser(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}
	response.Success(c, resp)
}
func (h *Handler) UpdateUser(c *gin.Context) {
	id, err := param.Parse[uint](c, "id")
	operation := "UpdateUser"
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	userid := middleware.GetUserID(c)
	if id != userid {
		response.ErrorFrom(c, operation, codeErrors.ErrCodeForbidden)
		return
	}

	var req modelsUser.UpdateProfileReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	resp, err := h.svc.UpdateUser(c.Request.Context(), id, &req)
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}
	response.Success(c, resp)
}
func (h *Handler) UploadAvatar(c *gin.Context) {
	operation := "UploadAvatar"
	id, err := param.Parse[uint](c, "id")
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	userid := middleware.GetUserID(c)
	if id != userid {
		response.ErrorFrom(c, operation, codeErrors.ErrCodeForbidden)
	}
}
