package video

import (
	"LikeBili/internal/middleware"
	repo "LikeBili/internal/repository/video"
	"LikeBili/internal/service/rank"
	svc "LikeBili/internal/service/video"
	"LikeBili/internal/transcode"
	jwtlib "LikeBili/pkg/jwt"
	"LikeBili/pkg/storage"
	"LikeBili/pkg/toresp"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// RegisterRoutes 注册视频模块全部路由，并在此一次性完成依赖装配。
// 转码链路：publishFn 非 nil 走 MQ 发布，发布失败/为 nil 时由 WithTranscodeRunner
// 注入的闭包降级为本地 ProcessVideo（真实 ffmpeg HLS 转码）。
// reviewProvider：审核记录查询器（admin 模块 Repository 实现），
// 用于作者查看驳回原因；不注入则作者端不展示驳回原因，不影响其它功能。
// 需登录的写操作（上传/更新/删除）统一挂 AuthRequired 中间件。
func RegisterRoutes(r *gin.RouterGroup, db *gorm.DB, rdb *redis.Client, toresp *toresp.VideoRespBuilder, rank *rank.Service, storage *storage.MinIO, broker *transcode.ProgressBroker, jwtSvc *jwtlib.JWT, publishFn func(videoID uint) error, reviewProvider svc.ReviewProvider) {
	// ① 依赖装配：repo → service（含转码降级 + 审核查询注入）→ handler
	repo := repo.NewRepository(db)
	svc := svc.NewService(repo, rdb, storage, toresp, rank,
		svc.WithTranscodePublisher(publishFn),
		svc.WithTranscodeRunner(func(videoID uint) {
			transcode.ProcessVideo(videoID, db, broker, storage)
		}),
		svc.WithReviewProvider(reviewProvider),
	)
	handler := NewHandler(svc)

	// ② 鉴权中间件：写操作强制登录；作品列表用可选鉴权（区分"本人看全部/他人看公开"）
	auth := middleware.AuthRequired(jwtSvc, rdb)
	optional := middleware.OptionalAuth(jwtSvc, rdb)

	// ③ 路由注册
	// 分类字典：公开接口，挂在 /categories（不属于 /videos 前缀；上传选分类、筛选、搜索共用）
	r.GET("/categories", handler.ListCategories)

	// 注意：/hot、/hot-rank 等静态路径必须注册在 /:id 之前，否则会被 :id 吞掉
	v := r.Group("/videos")
	{
		v.GET("", handler.ListVideo)                                 // 视频列表（?category_id= 分类筛选，游客可访问）
		v.GET("/hot", handler.HotVideos)                             // 热门视频（游客可访问）
		v.GET("/hot-rank", handler.HotRank)                          // 排行榜（?window=day/week/month，游客可访问）
		v.GET("/:id", optional, handler.GetVideo)                    // 视频详情（游客可访问；作者本人可见自己的未公开视频）
		v.GET("/:id/play-url", optional, handler.GetPlayURL)         // 播放地址（预签名 URL；作者本人可获取未公开视频的播放地址）
		v.GET("/users/:id/videos", optional, handler.ListUserVideos) // 用户作品列表（本人看全部，他人只看公开）

		v.POST("/upload", auth, handler.Upload)     // 上传视频（SSE 进度，需登录）
		v.PUT("/:id", auth, handler.UpdateVideo)    // 更新视频信息（需登录）
		v.DELETE("/:id", auth, handler.DeleteVideo) // 删除视频（需登录）
	}
}
