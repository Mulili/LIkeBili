package message

import (
	"LikeBili/internal/middleware"
	svcmessage "LikeBili/internal/service/message"
	codeErrors "LikeBili/pkg/errors"
	"LikeBili/pkg/response"
	"strconv"

	"github.com/gin-gonic/gin"
)

// Handler 消息（通知）模块的 HTTP 薄壳：解析登录态/分页/路径参数 → 调 Service → 包装响应。
type Handler struct {
	svc *svcmessage.Service
}

// NewHandler 构造消息处理器，注入消息服务。
func NewHandler(svc *svcmessage.Service) *Handler {
	return &Handler{svc: svc}
}

// ListNotifications 分页拉取当前登录用户的通知列表（需登录）。
// 对应 GET /messages?page=1&page_size=16；响应含未读数（Unread），供前端红点角标展示。
func (h *Handler) ListNotifications(c *gin.Context) {
	operation := "ListNotifications"
	// ① 鉴权：通知是当前用户的私有数据，未登录拒绝
	userID := middleware.GetUserID(c)
	if userID == 0 {
		response.ErrorFrom(c, operation, codeErrors.ErrUnauthorized)
		return
	}

	// ② 解析分页 query：缺省 1/16，越界值由 service 防御回退（上限 64）
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "16"))

	// ③ 调 Service：列表 + 未读数（缓存与 DB 取较大值）
	resp, err := h.svc.GetAllNotifications(c.Request.Context(), page, pageSize, userID)
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	response.Success(c, resp)
}

// MarkAllRead 一键把当前用户的所有通知置为已读（需登录）。
// 对应 POST /messages/read-all；DB 置读后同步把 Redis 未读缓存清零（尽力而为）。
func (h *Handler) MarkAllRead(c *gin.Context) {
	operation := "MarkAllRead"
	// ① 鉴权
	userID := middleware.GetUserID(c)
	if userID == 0 {
		response.ErrorFrom(c, operation, codeErrors.ErrUnauthorized)
		return
	}

	// ② 调 Service：全部置已读（幂等，重复调用无副作用）
	if err := h.svc.UpdateAllIsRead(c.Request.Context(), userID); err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	response.Success(c, nil)
}

// MarkOneRead 把指定通知置为已读（需登录，幂等）。
// 对应 POST /messages/:id/read；仅当该条确实由未读变为已读时才递减未读缓存。
func (h *Handler) MarkOneRead(c *gin.Context) {
	operation := "MarkOneRead"
	// ① 鉴权
	userID := middleware.GetUserID(c)
	if userID == 0 {
		response.ErrorFrom(c, operation, codeErrors.ErrUnauthorized)
		return
	}

	// ② 解析通知 ID（路径参数）
	msgID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || msgID == 0 {
		response.ErrorFrom(c, operation, codeErrors.Wrap(codeErrors.ErrorBadRequest, codeErrors.BadRequest, "消息ID错误"))
		return
	}

	// ③ 调 Service：单条置已读（repository 内按 user_id 限定，天然防越权）
	if err := h.svc.UpdateTheIsRead(c.Request.Context(), userID, uint(msgID)); err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	response.Success(c, nil)
}
