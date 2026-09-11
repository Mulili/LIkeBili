package favorites

import (
	"LikeBili/internal/middleware"
	repofavorites "LikeBili/internal/repository/favorites"
	svcfavorites "LikeBili/internal/service/favorites"
	"LikeBili/pkg/jwt"
	"LikeBili/pkg/storage"
	"LikeBili/pkg/toresp"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// RegisterRoutes 注册收藏夹模块路由。
// 分两个维度：
//   - 收藏夹维度 /favorites*：创建、我的收藏夹、收藏夹详情、收藏/取消收藏
//   - 用户维度  /users/:id/favorites：某用户对外的公开收藏夹（路径参数名 :id 与 user/follow 模块保持一致）
//
// 鉴权约定：
//   - 写操作与"我的"视角（创建 / 我的收藏夹 / 收藏动作）→ AuthRequired
//   - 收藏夹详情 → OptionalAuth（公开可看；私密夹靠 service 校验归属，带 token 才能看自己的私密夹）
//   - 他人主页的公开收藏夹列表 → 完全公开，无鉴权
//
// 依赖说明：
//   - minio：收藏夹封面走 1 小时预签名 URL（与 toresp 的永久公开 URL 语义不同）
//   - toVideoResp：收藏夹内视频条目统一走全站转换器，字段与 URL 规则不分叉
func RegisterRoutes(r *gin.RouterGroup, db *gorm.DB, rdb *redis.Client, minio *storage.MinIO, toVideoResp *toresp.VideoRespBuilder, jwt *jwt.JWT) {
	repo := repofavorites.NewRepository(db)
	svc := svcfavorites.NewService(repo, minio, toVideoResp)
	h := NewHandler(svc)

	middle := middleware.AuthRequired(jwt, rdb)
	optional := middleware.OptionalAuth(jwt, rdb)

	fav := r.Group("/favorites")
	{
		// 创建收藏夹：需登录（名称重复/占用保留名返回业务错误）
		fav.POST("", middle, h.CreateFavorite)
		// 我的收藏夹：需登录，含私密收藏夹；无收藏夹时自动创建默认收藏夹
		fav.GET("", middle, h.ListMyFavorites)
		// 收藏夹详情：公开 + 可选鉴权；私密夹仅本人可看，失效视频以 invalid 占位返回
		fav.GET("/:id/items", optional, h.GetFavoriteItems)
		// 收藏/取消收藏视频：需登录，同一接口 toggle，需为该收藏夹的归属者
		fav.POST("/:id/items", middle, h.ToggleFavoriteItem)
	}

	// 某用户的公开收藏夹：完全公开（他人主页展示），私密夹由 service 过滤
	r.GET("/users/:id/favorites", h.ListUserFavorites)
}
