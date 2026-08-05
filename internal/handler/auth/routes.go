package auth

import (
	"LikeBili/internal/middleware"
	authrepo "LikeBili/internal/repository/auth"
	favoritesRepo "LikeBili/internal/repository/favorites"
	authserver "LikeBili/internal/service/auth"
	jwtlib "LikeBili/pkg/jwt"
	"LikeBili/pkg/storage"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// RegisterRoutes 把认证模块的全部路由注册到 gin 路由组。
// 参数说明：
//   - rg：已挂载中间件（CSRF 等）的路由组，由 main 传入（通常为 api/v1 分组）
//   - rdb/db/jwt/tokenTTL/minio/favorite：Service 的全部依赖，由 main 构造后注入
//
// 依赖注入链：main 构造依赖 → RegisterRoutes 组装 repo/svc/handler → 注册路由。
func RegisterRoutes(rg *gin.RouterGroup, rdb *redis.Client, db *gorm.DB, jwt *jwtlib.JWT, tokenTTL time.Duration, minio *storage.MinIO, favorite *favoritesRepo.Repository) {
	repo := authrepo.NewRepository(db)                                      // 1. 用户数据仓库（依赖 db）
	svc := authserver.NewService(repo, rdb, jwt, tokenTTL, minio, favorite) // 2. 业务服务（6 个依赖）
	h := NewHandler(svc, tokenTTL)                                          // 3. HTTP 处理层

	// 注册路由：path 相对于路由组（如 /api/v1），注册、登录、刷新已在 CSRF 豁免列表中
	auth := rg.Group("auth")
	{
		//不登录也能使用的功能
		auth.POST("/register", h.Register)
		auth.POST("/login", h.Login)
		auth.POST("/refresh", h.Refresh)
		//登录才能使用的功能
		auth.POST("/logout", middleware.AuthRequired(jwt, rdb), h.Logout)
		auth.GET("/me", middleware.AuthRequired(jwt, rdb), h.FindMe)
	}
	auth.GET("/csrf", h.CSRF)
}
