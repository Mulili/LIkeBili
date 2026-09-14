package search

import (
	reposearch "LikeBili/internal/repository/search"
	repovideo "LikeBili/internal/repository/video"
	svcsearch "LikeBili/internal/service/search"
	"LikeBili/pkg/toresp"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RegisterRoutes 注册搜索模块路由。
// GET /search/videos：公开接口（游客可搜）——
// 关键词 ≥2 字走 video_search 的 ngram 全文检索，1 字降级为分类名模糊匹配。
func RegisterRoutes(r *gin.RouterGroup, db *gorm.DB, toVideoResp *toresp.VideoRespBuilder) {
	searchRepo := reposearch.NewRepository(db)
	videoRepo := repovideo.NewRepository(db)
	svc := svcsearch.NewService(searchRepo, videoRepo, toVideoResp)
	h := NewHandler(svc)

	r.GET("/search/videos", h.SearchVideos)
}
