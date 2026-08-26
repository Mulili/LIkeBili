// 路由注册文件：本文件与 handler.go 同属 package like，直接复用其 Handler/NewHandler。
package like

import (
	"LikeBili/internal/middleware"
	repolike "LikeBili/internal/repository/like"
	svclike "LikeBili/internal/service/like"
	"LikeBili/internal/service/message"
	"LikeBili/internal/service/rank"
	"LikeBili/pkg/jwt"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// RegisterRoutes 注册点赞模块的全部路由。
//
// 参数说明（依赖注入：main 启动时组装好传进来，模块内不自己创建）：
//   - r:        父路由组（main 里通常是 /api/v1），在其下挂点赞模块的子路由
//   - db:       数据库连接，用来创建 Repository
//   - rdb:      Redis 客户端，注入点赞计数缓存与鉴权中间件
//   - notifier: 通知发送器（由 message 模块的 Service 实现 Notifier 接口），
//     点赞后通知视频作者；不注入则跳过通知，不影响点赞主流程
//   - rank:     热度排行榜服务，新增点赞时做热度埋点
//   - jwtsvc:   JWT 工具，注入鉴权中间件（解析签名 token）
func RegisterRoutes(r *gin.RouterGroup, db *gorm.DB, rdb *redis.Client, notifier *message.Service, rank *rank.Service, jwtsvc *jwt.JWT) {
	// 依赖组装：Repository → Service → Handler，层层注入（依赖倒置，便于测试替换）
	repo := repolike.NewRepository(db)
	svc := svclike.NewService(repo, rdb, notifier, rank)
	h := NewHandler(svc)

	// 点赞模块路由组：最终路径 = /api/v1/videos/:id/likes
	l := r.Group("/videos/:id/likes")
	{
		// 需登录：点赞/取消（toggle，重复点击取消点赞），返回 {liked, count}
		l.POST("", middleware.AuthRequired(jwtsvc, rdb), h.Create)

		// 公开：查询视频点赞总数（游客可访问）
		l.GET("", h.GetCount)
	}
}
