package comment

import (
	"LikeBili/internal/middleware"
	repocomment "LikeBili/internal/repository/comment"
	svccomment "LikeBili/internal/service/comment"
	"LikeBili/internal/service/message"
	"LikeBili/internal/service/rank"
	"LikeBili/pkg/jwt"
	"LikeBili/pkg/toresp"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// RegisterRoutes 注册评论模块路由，统一挂在 /videos/:id/comments 前缀下。
// 读接口（公开，游客可访问）：根评论列表、子评论分页；
// 写接口（AuthRequired）：发表评论、评论点赞、删除评论。
func RegisterRoutes(r *gin.RouterGroup, db *gorm.DB, rdb *redis.Client, notifier *message.Service, toresp *toresp.UserBriefRespBuilder, rank *rank.Service, jwt *jwt.JWT) {
	repo := repocomment.NewRepository(db)
	svc := svccomment.NewService(repo, rdb, notifier, toresp, rank)
	h := NewHandler(svc)

	// 写操作统一挂登录鉴权中间件；读接口游客可访问，不挂
	middle := middleware.AuthRequired(jwt, rdb)

	comm := r.Group("/videos/:id/comments")
	{
		// 公开读：根评论分页列表（每条内嵌前 5 条回复预览 + 回复总数）
		comm.GET("", h.GetComments)
		// 公开读：某根评论下的子评论分页（楼中楼"加载更多"）
		comm.GET("/:root_id/replies", h.GetReplies)

		// 登录写：发表评论/回复（ParentID=0 为根评论）
		comm.POST("", middle, h.Create)
		// 登录写：评论点赞/取消 toggle
		comm.POST("/:comment_id/like", middle, h.LikeComment)
		// 登录写：删除评论（评论作者本人或视频作者）
		comm.DELETE("/:comment_id", middle, h.DeleteComments)
	}
}
