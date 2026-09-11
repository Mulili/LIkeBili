package favorites

import (
	"LikeBili/internal/middleware"
	modelsFavorites "LikeBili/internal/models/favorites"
	svcfavorites "LikeBili/internal/service/favorites"
	codeErrors "LikeBili/pkg/errors"
	"LikeBili/pkg/response"
	vt "LikeBili/pkg/validator"
	"strconv"

	"github.com/gin-gonic/gin"
)

// Handler 收藏夹模块的 HTTP 薄壳：解析登录态/路径参数/请求体 → 调 Service → 包装响应。
type Handler struct {
	svc *svcfavorites.Service
}

// NewHandler 构造收藏夹处理器，注入收藏夹服务。
func NewHandler(svc *svcfavorites.Service) *Handler {
	return &Handler{svc: svc}
}

// CreateFavorite 创建收藏夹（需登录）。
// 对应 POST /favorites，body: {name, is_public?}；名称重复/占用保留名由 service 返回业务错误。
func (h *Handler) CreateFavorite(c *gin.Context) {
	operation := "CreateFavorite"
	// ① 鉴权：收藏夹归属于当前用户，未登录拒绝
	userID := middleware.GetUserID(c)
	if userID == 0 {
		response.ErrorFrom(c, operation, codeErrors.ErrUnauthorized)
		return
	}

	// ② 绑定并校验请求体（name 必填且 ≤16 字符；is_public 可选 0/1）
	var req modelsFavorites.FavoritesReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.ErrorFrom(c, operation, codeErrors.Wrap(err, codeErrors.BadRequest, "请求格式错误"))
		return
	}
	if err := vt.Struct(&req); err != nil {
		response.ErrorFrom(c, operation, codeErrors.Wrap(codeErrors.ErrorBadRequest, codeErrors.BadRequest, vt.TranslateError(err)))
		return
	}

	// ③ 调 Service：保留名校验 + 同名查重 + 落库
	resp, err := h.svc.CreateFavorite(c.Request.Context(), userID, &req)
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	response.Created(c, resp)
}

// ListMyFavorites 查询当前登录用户的全部收藏夹（需登录，含私密）。
// 对应 GET /favorites；若无收藏夹，service 会自动创建"默认收藏夹"后返回。
func (h *Handler) ListMyFavorites(c *gin.Context) {
	operation := "ListMyFavorites"
	// ① 鉴权：这是"我的收藏夹"，含私密收藏夹，必须登录
	userID := middleware.GetUserID(c)
	if userID == 0 {
		response.ErrorFrom(c, operation, codeErrors.ErrUnauthorized)
		return
	}

	// ② 调 Service：查询本人全部收藏夹（含私密，带条目数与封面）
	resp, err := h.svc.FindAllFavorites(c.Request.Context(), userID)
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	response.Success(c, resp)
}

// ListUserFavorites 查询某用户的公开收藏夹（公开读接口，游客可访问）。
// 对应 GET /users/:id/favorites；私密收藏夹由 service 过滤，不会出现在他人视角。
func (h *Handler) ListUserFavorites(c *gin.Context) {
	operation := "ListUserFavorites"
	// ① 解析目标用户 ID（路径参数）
	targetID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || targetID == 0 {
		response.ErrorFrom(c, operation, codeErrors.Wrap(codeErrors.ErrorBadRequest, codeErrors.BadRequest, "用户ID错误"))
		return
	}

	// ② 调 Service：只返回该用户的公开收藏夹
	resp, err := h.svc.FindUserPublicFavorite(c.Request.Context(), uint(targetID))
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	response.Success(c, resp)
}

// GetFavoriteItems 分页查询收藏夹详情（公开读 + 可选鉴权）。
// 对应 GET /favorites/:id/items?page=1&page_size=16；
// 私密收藏夹仅本人可访问（service 内按 IsPublic + 归属校验），游客/他人访问返回 403。
// 已删除/未过审/非公开的视频不会消失，而是以 invalid 占位返回。
func (h *Handler) GetFavoriteItems(c *gin.Context) {
	operation := "GetFavoriteItems"
	// ① 取登录态：读接口不强制登录，游客为 0（仅影响私密收藏夹的归属校验）
	viewerID := middleware.GetUserID(c)

	// ② 解析收藏夹 ID（路径参数）
	favoriteID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || favoriteID == 0 {
		response.ErrorFrom(c, operation, codeErrors.Wrap(codeErrors.ErrorBadRequest, codeErrors.BadRequest, "收藏夹ID错误"))
		return
	}

	// ③ 解析分页 query：缺省 1/16，越界值交由 service 防御回退
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "16"))

	// ④ 调 Service：可见性校验 + 条目组装（含失效占位）
	resp, err := h.svc.GetFavoriteDetail(c.Request.Context(), viewerID, uint(favoriteID), page, pageSize)
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	response.Success(c, resp)
}

// ToggleFavoriteItem 收藏/取消收藏视频（需登录，同一接口 toggle）。
// 对应 POST /favorites/:id/items，:id 为目标收藏夹，body: {video_id}；
// 返回操作后的收藏状态 {favorited}，供前端即时更新按钮。
func (h *Handler) ToggleFavoriteItem(c *gin.Context) {
	operation := "ToggleFavoriteItem"
	// ① 鉴权：写操作必须登录
	userID := middleware.GetUserID(c)
	if userID == 0 {
		response.ErrorFrom(c, operation, codeErrors.ErrUnauthorized)
		return
	}

	// ② 解析收藏夹 ID（路径参数）
	favoriteID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || favoriteID == 0 {
		response.ErrorFrom(c, operation, codeErrors.Wrap(codeErrors.ErrorBadRequest, codeErrors.BadRequest, "收藏夹ID错误"))
		return
	}

	// ③ 绑定并校验请求体：video_id 必填
	var req modelsFavorites.FavoritesItemReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.ErrorFrom(c, operation, codeErrors.Wrap(err, codeErrors.BadRequest, "请求格式错误"))
		return
	}
	if err := vt.Struct(&req); err != nil {
		response.ErrorFrom(c, operation, codeErrors.Wrap(codeErrors.ErrorBadRequest, codeErrors.BadRequest, vt.TranslateError(err)))
		return
	}

	// ④ 调 Service：归属校验 + 已收藏则取消、未收藏则收藏（幂等）
	resp, err := h.svc.ToggleVideoFavorite(c.Request.Context(), uint(favoriteID), userID, req.VideoID)
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	response.Success(c, resp)
}
