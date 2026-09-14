package search

import (
	svcsearch "LikeBili/internal/service/search"
	"LikeBili/pkg/response"
	"strconv"

	"github.com/gin-gonic/gin"
)

// Handler 搜索模块的 HTTP 薄壳：解析查询参数 → 调 Service → 包装响应。
type Handler struct {
	svc *svcsearch.Service
}

// NewHandler 构造搜索处理器，注入搜索服务。
func NewHandler(svc *svcsearch.Service) *Handler {
	return &Handler{svc: svc}
}

// SearchVideos 搜索视频（GET /search/videos，游客可访问）。
//
// 查询参数：
//   - keyword：搜索词，1~50 字（≥2 字走全文检索，1 字按分类名兜底，空则返回空列表）
//   - category_id：可选，按分类精确过滤
//   - order：relevance（默认，相关度优先）/ latest（最新优先）
//   - page / page_size：分页（默认 1/16，上限 50）
func (h *Handler) SearchVideos(c *gin.Context) {
	operation := "SearchVideos"
	// ① 关键词：原样交给 service 做 trim/截断/分支，handler 不做业务判断
	keyword := c.Query("keyword")

	// ② 可选分类过滤（0 或非法值 = 不过滤）
	categoryID, _ := strconv.ParseUint(c.DefaultQuery("category_id", "0"), 10, 64)

	// ③ 排序方式（非法值由 service 归入默认相关度）
	order := c.DefaultQuery("order", "relevance")

	// ④ 分页参数
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "16"))

	// ⑤ 调 Service：检索 + 回表组装
	resp, err := h.svc.SearchVideos(c.Request.Context(), keyword, uint(categoryID), order, page, pageSize)
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	response.Success(c, resp)
}
