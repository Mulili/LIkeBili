// Package admin 提供管理员审核视频的 HTTP 处理器（Handler）。
// 角色边界：仅审核管理员（role=2）可访问本模块接口，鉴权由 middleware.AdminRequired 完成。
// 调用链：路由 → Handler（解析参数/输出响应）→ Service（业务逻辑）→ Repository（数据访问）。
package admin

import (
	"LikeBili/internal/middleware"
	modelsReview "LikeBili/internal/models/review"
	svc "LikeBili/internal/service/admin"
	"LikeBili/pkg/param"
	"LikeBili/pkg/response"
	"strconv"

	"github.com/gin-gonic/gin"
)

// Handler 审核模块的 HTTP 处理器，只做参数解析与响应输出，业务逻辑下沉到 Service。
type Handler struct {
	svc *svc.Service
}

// NewHandler 创建审核模块处理器。
func NewHandler(svc *svc.Service) *Handler {
	return &Handler{svc: svc}
}

// ListPending 待审核视频列表（GET /admin/videos/pending，仅 role=2）。
// 分页参数：page（默认 1）、page_size（默认 10）。
func (h *Handler) ListPending(c *gin.Context) {
	operation := "ListPending"
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))

	resp, err := h.svc.ListPendingVideos(c.Request.Context(), page, pageSize)
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}
	response.Success(c, resp)
}

// GetVideoDetail 审核详情（GET /admin/videos/:id，仅 role=2）。
// 返回视频完整信息 + 全部审核历史（倒序），供审核后台展示。
func (h *Handler) GetVideoDetail(c *gin.Context) {
	operation := "GetVideoDetail"
	videoID, err := param.Parse[uint](c, "id")
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	resp, err := h.svc.GetVideoDetail(c.Request.Context(), videoID)
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}
	response.Success(c, resp)
}

// GetReviewPlayURL 审核观看（GET /admin/videos/:id/play-url，仅 role=2）。
// 返回源文件 1 小时有效的预签名 URL（不过滤审核状态，待审核/驳回都能看）。
func (h *Handler) GetReviewPlayURL(c *gin.Context) {
	operation := "GetReviewPlayURL"
	videoID, err := param.Parse[uint](c, "id")
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	url, err := h.svc.GetReviewPlayURL(c.Request.Context(), videoID)
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}
	response.Success(c, url)
}

// Review 审核操作（POST /admin/videos/:id/review，仅 role=2）。
// 请求体：{result: 2=通过|3=驳回, reason: 驳回原因（驳回时必填）}。
// 审核管理员 ID 从登录 token 取（AuthRequired 注入），写入审核流水留痕。
func (h *Handler) Review(c *gin.Context) {
	operation := "Review"
	adminID := middleware.GetUserID(c)

	videoID, err := param.Parse[uint](c, "id")
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}
	var req modelsReview.ReviewReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	if err := h.svc.Review(c.Request.Context(), adminID, videoID, req.Result, req.Reason); err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}
	response.Success(c, nil)
}

// Cleanup 硬删除软删超过指定天数的视频（POST /admin/videos/cleanup，仅 role=2）。
// 请求体：{days: 30}；days<1 时服务端钳制为 30（防误传 0/负数清空回收站）。
// 返回清理数量 {deleted: n}；流程含 DB 事务硬删 + MinIO 对象清理。
func (h *Handler) Cleanup(c *gin.Context) {
	operation := "Admin.Cleanup"
	adminID := middleware.GetUserID(c)

	var req modelsReview.CleanupReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	count, err := h.svc.CleanupExpired(c.Request.Context(), adminID, req.Days)
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}
	response.Success(c, gin.H{"deleted": count})
}
