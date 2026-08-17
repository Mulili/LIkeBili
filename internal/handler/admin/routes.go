package admin

import (
	"LikeBili/internal/middleware"
	repo "LikeBili/internal/repository/admin"
	rpvideo "LikeBili/internal/repository/video"
	svc "LikeBili/internal/service/admin"
	jwtlib "LikeBili/pkg/jwt"
	"LikeBili/pkg/storage"
	"LikeBili/pkg/toresp"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// RegisterRoutes 注册管理员审核模块路由，并在此一次性完成依赖装配。
// 全部审核接口挂 AdminRequired（仅审核管理员 role=2 可访问，role=1/3 均 403）。
// videoRepo：视频数据访问层，用于软删超期视频的硬删除清理（ListDeleteBefore/HardDeleteExpiredTx）。
func RegisterRoutes(r *gin.RouterGroup, db *gorm.DB, rdb *redis.Client, videoRepo *rpvideo.Repository, storage *storage.MinIO, toresp *toresp.VideoRespBuilder, jwtSvc *jwtlib.JWT) {
	// ① 依赖装配：repo → service → handler
	repo := repo.NewRepository(db)
	svc := svc.NewService(repo, videoRepo, storage, toresp)
	handler := NewHandler(svc)

	// ② 审核管理员专用路由组（统一挂 AdminRequired）
	admin := r.Group("/admin")
	admin.Use(middleware.AdminRequired(jwtSvc, rdb))
	{
		admin.GET("/videos/pending", handler.ListPending)           // 待审核列表
		admin.GET("/videos/:id", handler.GetVideoDetail)            // 审核详情（含审核历史）
		admin.GET("/videos/:id/play-url", handler.GetReviewPlayURL) // 审核观看（源文件预签名）
		admin.POST("/videos/:id/review", handler.Review)            // 审核操作：通过/驳回
		admin.POST("/videos/cleanup", handler.Cleanup)              // 硬删软删超期视频（回收站清理）
	}
}
