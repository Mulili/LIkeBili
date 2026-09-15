package search

import (
	svcsearch "LikeBili/internal/service/search"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes 注册搜索模块路由。
// GET /search/videos：公开接口（游客可搜）——
// 关键词 ≥2 字走 video_search 的 ngram 全文检索，1 字降级为分类名模糊匹配。
//
// 注：service 由 main 构造并传入，因为同一个实例还要注入给 video/user/admin 模块做索引同步。
func RegisterRoutes(r *gin.RouterGroup, svc *svcsearch.Service) {
	h := NewHandler(svc)

	r.GET("/search/videos", h.SearchVideos)
}
