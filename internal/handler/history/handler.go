package history

import (
	"LikeBili/internal/middleware"
	modelsHistory "LikeBili/internal/models/history"
	svchistory "LikeBili/internal/service/history"
	codeErrors "LikeBili/pkg/errors"
	"LikeBili/pkg/response"
	vt "LikeBili/pkg/validator"
	"strconv"

	"github.com/gin-gonic/gin"
)

// Handler 观看历史模块的 HTTP 薄壳：解析鉴权/请求体 → 调 Service → 包装响应。
type Handler struct {
	svc *svchistory.Service
}

// NewHandler 构造历史处理器，注入历史服务。
func NewHandler(svc *svchistory.Service) *Handler {
	return &Handler{svc: svc}
}

// RecordHistory 上报一次观看进度（需登录）。
// 对应 POST /history；同一用户对同一视频会更新为最新进度与观看时间（service 内 upsert）。
func (h *Handler) RecordHistory(c *gin.Context) {
	operation := "RecordHistory"
	// ① 鉴权：观看历史是当前用户的私有数据，未登录拒绝
	userID := middleware.GetUserID(c)
	if userID == 0 {
		response.ErrorFrom(c, operation, codeErrors.ErrUnauthorized)
		return
	}

	// ② 绑定 JSON 请求体（video_id + progress + duration）
	var req modelsHistory.CreateHistoryReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.ErrorFrom(c, operation, codeErrors.Wrap(err, codeErrors.BadRequest, "请求格式错误"))
		return
	}

	// ③ 结构体校验：video_id 必填
	if err := vt.Struct(&req); err != nil {
		response.ErrorFrom(c, operation, codeErrors.Wrap(codeErrors.ErrorBadRequest, codeErrors.BadRequest, vt.TranslateError(err)))
		return
	}

	// ④ 调 Service：创建/更新历史 + 附带回填视频时长（若未解析过）
	if err := h.svc.CreateOrUpdateHistory(c.Request.Context(), userID, req); err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	response.Success(c, nil)
}

// ListHistory 分页查询当前用户的观看历史（需登录）。
// 对应 GET /history?page=1&page_size=16；倒序返回（最近观看在前），每条内嵌完整视频信息。
func (h *Handler) ListHistory(c *gin.Context) {
	operation := "ListHistory"
	// ① 鉴权：历史列表是当前用户的私有数据，未登录拒绝
	userID := middleware.GetUserID(c)
	if userID == 0 {
		response.ErrorFrom(c, operation, codeErrors.ErrUnauthorized)
		return
	}

	// ② 解析分页 query：page/page_size 缺省用 1/16（越界值交由 service 防御回退）
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "16"))

	// ③ 调 Service：分页查询并组装 HistoryListResp
	resp, err := h.svc.List(c.Request.Context(), userID, page, pageSize)
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	response.Success(c, resp)
}
