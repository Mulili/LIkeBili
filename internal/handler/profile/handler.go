package profile

import (
	"LikeBili/internal/middleware"
	svcprofile "LikeBili/internal/service/profile"
	codeErrors "LikeBili/pkg/errors"
	"LikeBili/pkg/response"
	"strconv"

	"github.com/gin-gonic/gin"
)

// Handler 个人中心模块的 HTTP 薄壳：解析登录态/路径参数 → 调 Service → 包装响应。
type Handler struct {
	svc *svcprofile.Service
}

// NewHandler 构造个人中心处理器，注入聚合服务。
func NewHandler(svc *svcprofile.Service) *Handler {
	return &Handler{svc: svc}
}

// GetProfile 查看某用户的个人中心聚合信息（公开读 + 可选鉴权）。
// 对应 GET /users/:id/profile，一次返回资料 + 关注数/粉丝数/收藏夹数/投稿数；
// 本人访问（token 有效且 :id 为自己）时额外返回硬币余额与观看历史条数。
func (h *Handler) GetProfile(c *gin.Context) {
	operation := "GetProfile"
	// ① 取登录态：游客为 0（读接口不强制登录，仅决定是否返回私有数据与统计口径）
	viewerID := middleware.GetUserID(c)

	// ② 解析被查看用户 ID（路径参数）
	targetID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || targetID == 0 {
		response.ErrorFrom(c, operation, codeErrors.Wrap(codeErrors.ErrorBadRequest, codeErrors.BadRequest, "用户ID错误"))
		return
	}

	// ③ 调 Service：聚合并按本人/他人分权
	resp, err := h.svc.GetProfile(c.Request.Context(), viewerID, uint(targetID))
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	response.Success(c, resp)
}
