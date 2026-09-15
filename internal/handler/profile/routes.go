package profile

import (
	"LikeBili/internal/middleware"
	repocoin "LikeBili/internal/repository/coin"
	repofavorites "LikeBili/internal/repository/favorites"
	repofollow "LikeBili/internal/repository/follow"
	repohistory "LikeBili/internal/repository/history"
	repomessage "LikeBili/internal/repository/message"
	rpuser "LikeBili/internal/repository/user"
	repovideo "LikeBili/internal/repository/video"
	svcmessage "LikeBili/internal/service/message"
	svcprofile "LikeBili/internal/service/profile"
	svcuser "LikeBili/internal/service/user"
	"LikeBili/pkg/jwt"
	"LikeBili/pkg/storage"
	"LikeBili/pkg/toresp"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// RegisterRoutes 注册个人中心聚合路由。
//
// GET /users/:id/profile（公开 + 可选鉴权）：
//   - 游客/他人视角：只返回公开统计（关注数、粉丝数、公开收藏夹数、公开投稿数）
//   - 本人视角（token 有效且 :id == 自己）：额外返回硬币余额与观看历史条数
//
// 路径参数名 :id 与 user / follow / favorites 模块保持一致，避免 gin 路由树参数名冲突。
// 依赖说明：聚合 reads 各模块仓储，故在此组装 UserService 与各 Repository。
func RegisterRoutes(r *gin.RouterGroup, db *gorm.DB, rdb *redis.Client, minio *storage.MinIO, jwt *jwt.JWT) {
	// ① 用户资料服务：复用其头像 URL 拼接与"用户不存在"错误语义
	// indexer 传 nil：个人中心只读资料，不涉及改昵称后的搜索索引同步
	userSvc := svcuser.NewService(rpuser.NewRepository(db), minio, nil)

	// ② 消息服务：借用其"DB/Redis 取较大值"的未读数口径，保证与 /messages 的红点一致
	msgSvc := svcmessage.NewService(repomessage.NewRepository(db), rdb, toresp.NewToRespBuilder(minio))

	svc := svcprofile.NewService(
		userSvc,
		msgSvc,
		repofollow.NewRepository(db),
		repofavorites.NewRepository(db),
		repovideo.NewRepository(db),
		repohistory.NewRepository(db),
		repocoin.NewRepository(db),
	)
	h := NewHandler(svc)

	// 公开可看 + 可选鉴权：带 token 且查看的是自己时，响应才包含私有数据
	r.GET("/users/:id/profile", middleware.OptionalAuth(jwt, rdb), h.GetProfile)
}
