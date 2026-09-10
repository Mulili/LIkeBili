package follow

import (
	"LikeBili/internal/middleware"
	svcfollow "LikeBili/internal/service/follow"
	codeErrors "LikeBili/pkg/errors"
	"LikeBili/pkg/response"
	"strconv"

	"github.com/gin-gonic/gin"
)

// Handler 关注模块的 HTTP 薄壳：解析登录态/路径参数/分页 → 调 Service → 包装响应。
type Handler struct {
	svc *svcfollow.Service
}

// NewHandler 构造关注处理器，注入关注服务。
func NewHandler(svc *svcfollow.Service) *Handler {
	return &Handler{svc: svc}
}

// FollowUser 关注某人（需登录）；"回关"复用同一接口，数据层动作完全相同。
// 对应 POST /users/:id/follow，:id 为被关注者；关注者取自登录态，不接受前端传入。
func (h *Handler) FollowUser(c *gin.Context) {
	operation := "FollowUser"
	// ① 鉴权：写操作必须登录
	userID := middleware.GetUserID(c)
	if userID == 0 {
		response.ErrorFrom(c, operation, codeErrors.ErrUnauthorized)
		return
	}

	// ② 解析被关注者 ID（路径参数）
	targetID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || targetID == 0 {
		response.ErrorFrom(c, operation, codeErrors.Wrap(codeErrors.ErrorBadRequest, codeErrors.BadRequest, "用户ID错误"))
		return
	}

	// ③ 调 Service：自关注拦截、重复关注幂等、首次关注通知被关注者均在 service 内完成
	if err := h.svc.Follow(c.Request.Context(), userID, uint(targetID)); err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	response.Success(c, nil)
}

// UnfollowUser 取关某人（需登录）。
// 对应 DELETE /users/:id/follow；未关注过也视为成功（幂等）。
func (h *Handler) UnfollowUser(c *gin.Context) {
	operation := "UnfollowUser"
	// ① 鉴权：写操作必须登录
	userID := middleware.GetUserID(c)
	if userID == 0 {
		response.ErrorFrom(c, operation, codeErrors.ErrUnauthorized)
		return
	}

	// ② 解析被取关者 ID（路径参数）
	targetID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || targetID == 0 {
		response.ErrorFrom(c, operation, codeErrors.Wrap(codeErrors.ErrorBadRequest, codeErrors.BadRequest, "用户ID错误"))
		return
	}

	// ③ 调 Service：解除关注关系
	if err := h.svc.Unfollow(c.Request.Context(), userID, uint(targetID)); err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	response.Success(c, nil)
}

// ListFollowing 分页查看某用户的关注列表（公开读接口，游客可访问）。
// 对应 GET /users/:id/followings?page=1&page_size=16，按关注时间倒序。
// viewerID 取当前登录态（游客为 0），由 service 填充行内"互关/已关注"状态；已注销用户由 service 替换占位。
func (h *Handler) ListFollowing(c *gin.Context) {
	operation := "ListFollowing"
	// ① 取登录态：读接口不强制登录，仅影响行内关系状态填充（游客恒为 false）
	viewerID := middleware.GetUserID(c)

	// ② 解析被查看用户 ID（路径参数）
	targetID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || targetID == 0 {
		response.ErrorFrom(c, operation, codeErrors.Wrap(codeErrors.ErrorBadRequest, codeErrors.BadRequest, "用户ID错误"))
		return
	}

	// ③ 解析分页 query：缺省 1/16，越界值交由 service 防御回退
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "16"))

	// ④ 调 Service：查询关系行 + 组装列表 DTO
	resp, err := h.svc.ListFollowing(c.Request.Context(), viewerID, uint(targetID), page, pageSize)
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	response.Success(c, resp)
}

// ListFollowers 分页查看某用户的粉丝列表（公开读接口，游客可访问）。
// 对应 GET /users/:id/followers?page=1&page_size=16，按关注时间倒序。
// 粉丝行的 IsFollowing 即"回关"按钮状态（viewer 是否已关注该粉丝）。
func (h *Handler) ListFollowers(c *gin.Context) {
	operation := "ListFollowers"
	// ① 取登录态：游客为 0
	viewerID := middleware.GetUserID(c)

	// ② 解析被查看用户 ID（路径参数）
	targetID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || targetID == 0 {
		response.ErrorFrom(c, operation, codeErrors.Wrap(codeErrors.ErrorBadRequest, codeErrors.BadRequest, "用户ID错误"))
		return
	}

	// ③ 解析分页 query
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "16"))

	// ④ 调 Service：查询粉丝行 + 组装列表 DTO
	resp, err := h.svc.ListFollowers(c.Request.Context(), viewerID, uint(targetID), page, pageSize)
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	response.Success(c, resp)
}
