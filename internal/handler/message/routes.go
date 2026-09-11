package message

import (
	"LikeBili/internal/middleware"
	repomessage "LikeBili/internal/repository/message"
	svcmessage "LikeBili/internal/service/message"
	"LikeBili/pkg/jwt"
	"LikeBili/pkg/toresp"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// RegisterRoutes 注册消息（通知）模块路由，统一挂在 /messages 下，全部需登录。
//   - GET  /messages            分页拉取通知列表（含未读数，供红点）
//   - POST /messages/read-all   一键全部已读
//   - POST /messages/:id/read   单条已读（幂等）
//
// 说明：通知只属于接收者本人，因此本模块没有公开接口，整组挂 AuthRequired。
// userBriefBuilder 用于把消息发送者（点赞/评论/关注的人）转成带完整头像 URL 的简要信息。
func RegisterRoutes(r *gin.RouterGroup, db *gorm.DB, rdb *redis.Client, userBriefBuilder *toresp.UserBriefRespBuilder, jwt *jwt.JWT) {
	repo := repomessage.NewRepository(db)
	svc := svcmessage.NewService(repo, rdb, userBriefBuilder)
	h := NewHandler(svc)

	msg := r.Group("/messages")
	msg.Use(middleware.AuthRequired(jwt, rdb))
	{
		// 通知列表：分页 + 未读数
		msg.GET("", h.ListNotifications)
		// 一键全部已读
		msg.POST("/read-all", h.MarkAllRead)
		// 单条已读：路径参数 :id 为通知主键
		msg.POST("/:id/read", h.MarkOneRead)
	}
}
