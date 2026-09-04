package history

import (
	"LikeBili/internal/middleware"
	repohistory "LikeBili/internal/repository/history"
	repovideo "LikeBili/internal/repository/video"
	svchistory "LikeBili/internal/service/history"
	"LikeBili/pkg/jwt"
	"LikeBili/pkg/toresp"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// RegisterRoutes 注册观看历史模块路由。
// 历史是当前登录用户的个人数据，两个接口（上报进度 / 查询列表）统一挂 AuthRequired。
//   - POST /history：上报观看进度（body 带 video_id/progress/duration），重复观看走更新
//   - GET  /history：分页查询我的观看历史（最近观看在前）
func RegisterRoutes(r *gin.RouterGroup, db *gorm.DB, rdb *redis.Client, jwtSvc *jwt.JWT, toVideoResp *toresp.VideoRespBuilder) {
	rp := repohistory.NewRepository(db)
	videoRepo := repovideo.NewRepository(db) // 观看时回填视频真实时长
	svc := svchistory.NewService(rp, videoRepo, toVideoResp)
	h := NewHandler(svc)

	middle := middleware.AuthRequired(jwtSvc, rdb)

	his := r.Group("/history")
	his.Use(middle)
	{
		// 上报观看进度：前端播放器节流上报，同一 (user, video) 只保留最新一条
		his.POST("", h.RecordHistory)
		// 查询我的观看历史：分页 + 视频信息内嵌
		his.GET("", h.ListHistory)
	}
}
