package follow

import (
	"LikeBili/internal/middleware"
	repofollow "LikeBili/internal/repository/follow"
	svcfollow "LikeBili/internal/service/follow"
	"LikeBili/internal/service/message"
	"LikeBili/pkg/jwt"
	"LikeBili/pkg/toresp"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// RegisterRoutes 注册关注模块路由，统一挂在 /users/:id 下（用户维度子资源，
// 路径参数名 :id 与 user 模块保持一致，避免 gin 路由树参数名冲突）。
//   - POST   /users/:id/follow      关注（回关复用同一接口），需登录
//   - DELETE /users/:id/follow      取关，需登录
//   - GET    /users/:id/followings  某人的关注列表（公开 + 可选鉴权，填充互关状态）
//   - GET    /users/:id/followers   某人的粉丝列表（公开 + 可选鉴权，填充回关状态）
//
// notifier 传 message.Service：复用其通用 SendNotification + MsgTypeFollow(=3)，
// 为 nil 时关注成功不发通知、不影响主流程（fail-open）。
func RegisterRoutes(r *gin.RouterGroup, db *gorm.DB, rdb *redis.Client, notifier *message.Service, userBriefBuilder *toresp.UserBriefRespBuilder, jwt *jwt.JWT) {
	repo := repofollow.NewRepository(db)
	svc := svcfollow.NewService(repo, userBriefBuilder, notifier)
	h := NewHandler(svc)

	// 写操作挂强制鉴权；读接口游客可访问，挂可选鉴权：
	// 带有效 token 时注入 viewerID 供行内关系状态填充，无 token/失效则按游客（viewerID=0）放行
	middle := middleware.AuthRequired(jwt, rdb)
	optional := middleware.OptionalAuth(jwt, rdb)

	users := r.Group("/users")
	{
		// 关注：首次关注会通知被关注者；重复点击幂等
		users.POST("/:id/follow", middle, h.FollowUser)
		// 取关：幂等，未关注过也返回成功
		users.DELETE("/:id/follow", middle, h.UnfollowUser)
		// 某用户的关注列表：分页 + 行内互关状态（注销用户走官方占位）
		users.GET("/:id/followings", optional, h.ListFollowing)
		// 某用户的粉丝列表：分页 + 行内"回关"状态
		users.GET("/:id/followers", optional, h.ListFollowers)
	}
}
