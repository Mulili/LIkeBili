package rank

import (
	"LikeBili/internal/service/rank"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes 注册热门榜路由（公开接口，游客可查看榜单）。
// 路由：GET /rank/hot?window=day|week|month&top=20，返回 TopN 视频 ID 列表。
// fallback 为 Redis 冷启动时的 DB 兜底查询（可为 nil：空榜时直接返回空列表）。
func RegisterRoutes(r *gin.RouterGroup, rankSvc *rank.Service, fallback FallbackFunc) {
	h := NewHandler(rankSvc, fallback)
	r.GET("/rank/hot", h.HotRank)
}
