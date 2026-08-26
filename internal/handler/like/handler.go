package like

import (
	"LikeBili/internal/middleware"
	modelsLike "LikeBili/internal/models/like"
	svclike "LikeBili/internal/service/like"
	codeErrors "LikeBili/pkg/errors"
	"LikeBili/pkg/response"
	"strconv"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	svc *svclike.Service
}

func NewHandler(svc *svclike.Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) Create(c *gin.Context) {
	operation := "Create"
	userID := middleware.GetUserID(c)
	if userID == 0 {
		response.ErrorFrom(c, operation, codeErrors.ErrorUnauthorized)
		return
	}

	videoID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.ErrorFrom(c, operation, codeErrors.Wrap(err, codeErrors.BadRequest, "视频ID错误"))
		return
	}

	resp, err := h.svc.Create(c.Request.Context(), userID, uint(videoID))
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	response.Success(c, resp)
}

func (h *Handler) GetCount(c *gin.Context) {
	operation := "GetCount"

	videoID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.ErrorFrom(c, operation, codeErrors.Wrap(err, codeErrors.BadRequest, "视频ID错误"))
		return
	}
	total, err := h.svc.GetVideoLikes(c.Request.Context(), uint(videoID))
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	response.Success(c, modelsLike.LikeResp{Count: total})
}
