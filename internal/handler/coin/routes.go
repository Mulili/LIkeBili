package coin

import (
	"LikeBili/internal/middleware"
	repocoins "LikeBili/internal/repository/coin"
	svccoins "LikeBili/internal/service/coin"
	"LikeBili/pkg/jwt"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// RegisterRoutes 注册币模块路由。
// 分两个维度：
//   - 视频维度 /video/:id/coin*：公开的投币总数 + 登录用户的投币状态/投币动作（挂视频子资源下）
//   - 用户维度 /coin/balance：当前登录用户的余额（与视频无关，供个人中心调用）
//
// 写操作（投币）与私有查询（投币状态、余额）统一挂 AuthRequired；投币总数游客可看。
func RegisterRoutes(r *gin.RouterGroup, db *gorm.DB, jwt *jwt.JWT, rdb *redis.Client) {
	rp := repocoins.NewRepository(db)
	svc := svccoins.NewService(rp)
	h := NewHandler(svc)

	middle := middleware.AuthRequired(jwt, rdb)

	coin := r.Group("")
	{
		// 视频投币总数：游客可看视频页"已投 X 币"，不鉴权
		coin.GET("/video/:id/coin", h.CountVideoCoins)
		// 当前用户对该视频的投币状态（可投/剩余可投数），控制按钮置灰，需登录
		coin.GET("/video/:id/coin/status", middle, h.FindUserDrop)
		// 投币：扣余额 + 作者收币 + 流水（同一事务），需登录
		coin.POST("/video/:id/coin/drop", middle, h.DropCoins)
		// 我的硬币余额：个人中心展示，与视频无关，需登录
		coin.GET("/coin/balance", middle, h.FindBalance)
	}
}
