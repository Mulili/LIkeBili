// 路由注册文件：本文件与 handler.go 同属 package user，直接复用其 Handler/NewHandler。
package user

import (
	"LikeBili/internal/middleware"
	rp "LikeBili/internal/repository/user"
	service "LikeBili/internal/service/user"
	"LikeBili/pkg/jwt"
	"LikeBili/pkg/storage"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// RegisterRoutes 注册用户模块的全部路由。
//
// 参数说明（依赖注入：main 启动时组装好传进来，模块内不自己创建）：
//   - r:       父路由组（main 里通常是 /api/v1），在其下挂用户模块的子路由
//   - db:      数据库连接，用来创建 Repository
//   - rdb:     Redis 客户端，注入鉴权中间件（校验 token 与 Redis 中的一致）
//   - storage: MinIO 客户端，注入 Service（上传头像 / 生成 URL）
//   - jwt:     JWT 工具，注入鉴权中间件（解析签名 token）
func RegisterRoutes(r *gin.RouterGroup, db *gorm.DB, rdb *redis.Client, storage *storage.MinIO, jwt *jwt.JWT) {
	// 依赖组装：Repository → Service → Handler，层层注入（依赖倒置，便于测试替换）
	repo := rp.NewRepository(db)
	svc := service.NewService(repo, storage)
	h := NewHandler(svc)

	// 用户模块路由组：最终路径 = /api/v1/users/...
	users := r.Group("/users")
	{
		// 公开接口：任何人可查用户主页（不需要登录）
		// 最终路径：GET /api/v1/users/:id
		users.GET("/:id", h.GetUser)

		// 需登录：修改自己的资料（PUT 是写操作）
		// AuthRequired 先做鉴权（JWT + Redis 一致校验），handler 内再校验"路径 id == 登录用户 id"
		// 最终路径：PUT /api/v1/users/:id
		users.PUT("/:id", middleware.AuthRequired(jwt, rdb), h.UpdateUser)

		// 需登录：上传/更换头像（multipart 文件上传，POST 才支持请求体）
		// 最终路径：POST /api/v1/users/:id/avatar
		users.POST("/:id/avatar", middleware.AuthRequired(jwt, rdb), h.UploadAvatar)
	}
}
