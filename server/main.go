package main

import (
	adminhandler "LikeBili/internal/handler/admin"
	authhandler "LikeBili/internal/handler/auth"
	coinhandler "LikeBili/internal/handler/coin"
	commenthandler "LikeBili/internal/handler/comment"
	likehandler "LikeBili/internal/handler/like"
	userhandler "LikeBili/internal/handler/user"
	videohandler "LikeBili/internal/handler/video"
	"LikeBili/internal/middleware"
	modelsCoins "LikeBili/internal/models/coin"
	modelsComments "LikeBili/internal/models/comments"
	modelsFavorites "LikeBili/internal/models/favorites"
	modelsMeta "LikeBili/internal/models/meta"
	modelsQuality "LikeBili/internal/models/quality"
	modelsReview "LikeBili/internal/models/review"
	modelsTrans "LikeBili/internal/models/transcode"
	modelsUser "LikeBili/internal/models/user"
	modelsVideo "LikeBili/internal/models/video"
	adminRepo "LikeBili/internal/repository/admin"
	repocoin "LikeBili/internal/repository/coin"
	favRepo "LikeBili/internal/repository/favorites"
	rpmessage "LikeBili/internal/repository/message"
	rpvideo "LikeBili/internal/repository/video"
	svccoin "LikeBili/internal/service/coin"
	svcmessage "LikeBili/internal/service/message"
	"LikeBili/internal/service/rank"
	"LikeBili/internal/transcode"
	"LikeBili/pkg/config"
	"LikeBili/pkg/database"
	jwtlib "LikeBili/pkg/jwt"
	"LikeBili/pkg/logger"
	"LikeBili/pkg/rabbitmq"
	"LikeBili/pkg/storage"
	"LikeBili/pkg/toresp"
	"LikeBili/pkg/validator"
	"encoding/json"
	"log"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func main() {
	cfg := config.InitConfig()
	if err := validator.Init(); err != nil {
		log.Fatalf("validator failed: %v", err)
	}
	db := database.InitMySQL(cfg)
	rdb := database.InitRedis(cfg)
	minio, err := storage.New(cfg)
	if err != nil {
		log.Fatalf("minio failed: %v", err)
	}
	tokenTTL := time.Duration(cfg.TokenTTLDays) * 24 * time.Hour
	jwtSvc := jwtlib.New(cfg.JWTSecret, tokenTTL)
	favrepo := favRepo.NewRepository(db)
	// 币模块装配：注册/登录时签到发币（auth 依赖 coin，需先构造）
	coinRepo := repocoin.NewRepository(db)
	coinSvc := svccoin.NewService(coinRepo)

	r := gin.Default()
	r.Use(middleware.CORS())
	api := r.Group("api/v1")
	api.Use(middleware.CSRF(middleware.CSRFConfig{
		PublicPaths: []string{
			"/api/v1/auth/register",
			"/api/v1/auth/login",
			"/api/v1/auth/refresh",
		},
	}))
	db.AutoMigrate(
		&modelsUser.User{},
		&modelsFavorites.Favorites{},
		&modelsVideo.Category{},
		&modelsVideo.Video{},
		&modelsReview.VideoReview{},
		&modelsTrans.TranscodeTask{},
		&modelsMeta.VideoMeta{},
		&modelsQuality.VideoQuality{},
		&modelsComments.VideoComments{},
		&modelsComments.CommentLikes{},
		&modelsCoins.Coin{},
		&modelsCoins.UserCoin{},
	)
	authhandler.RegisterRoutes(api, rdb, db, jwtSvc, tokenTTL, minio, favrepo, coinSvc)
	userhandler.RegisterRoutes(api, db, rdb, minio, jwtSvc)
	// 币模块路由：/video/:id/coin*（投币）+ /coin/balance（个人中心余额）
	coinhandler.RegisterRoutes(api, db, jwtSvc, rdb)
	// --- 视频模块装配 ---
	broker := transcode.NewProgressBroker()
	userBriefBuider := toresp.NewToRespBuilder(minio)                 // 转码进度广播器（前端 SSE 订阅用）
	toVideoResp := toresp.NewVideoRespBuilder(minio, userBriefBuider) // 视频 DTO 转换器
	// 热度排行榜服务
	rankSvc := rank.NewService(rdb)
	adminRepo := adminRepo.NewRepository(db) // 审核记录查询器（作者端驳回原因展示）
	//--- 点赞模块装配：notifier 复用 message 服务，rank 复用热度服务 ---
	msgRepo := rpmessage.NewRepository(db)
	msgSvc := svcmessage.NewService(msgRepo, rdb, userBriefBuider)
	likehandler.RegisterRoutes(api, db, rdb, msgSvc, rankSvc, jwtSvc)
	// --- RabbitMQ 转码异步化装配 ---
	// 链路：上传 → publishFn 投递任务到 MQ → 消费者 goroutine 拉取 → transcode.ProcessVideo 执行真实转码。
	// 降级策略：MQ 不可用时整体退化为本地转码（不启动消费者、publishFn 保持 nil，
	// video 服务内部自动回退 WithTranscodeRunner 本地转码），保证 MQ 挂了服务照常跑。
	mq, err := rabbitmq.Init(rabbitmq.Config{
		Host: cfg.RabbitMQHost, Port: cfg.RabbitMQPort,
		User: cfg.RabbitMQUser, Password: cfg.RabbitMQPassword,
	})
	if err != nil {
		// ① Init 失败：mq 为 nil，此处绝不能继续用 mq（nil 指针 panic）。
		// 降级为本地转码：跳过消费者 goroutine，publishFn 保持 nil。
		logger.Warn("服务降级为本地转码", zap.Error(err))
	} else {
		// ② Init 成功：启动消费者 goroutine。
		// 外层 for+sleep 是断线自愈：Consume 在连接断开时返回，循环重试恢复消费
		// （连接本身由 rabbitmq 包 reconnectLoop 自动重连）。
		go func() {
			for {
				err := mq.Consume(rabbitmq.QueueTranscode, func(body []byte) error {
					var msg rabbitmq.TranscodeMessage
					if err := json.Unmarshal(body, &msg); err != nil {
						return nil // 坏消息直接丢弃，避免死循环
					}
					// 执行真实转码：写 transcode_tasks 状态 + MinIO 产物 + broker 广播 SSE 进度
					transcode.ProcessVideo(msg.VideoID, db, broker, minio)
					return nil
				})
				logger.Warn("转码消费者退出，三秒后重启", zap.Error(err))
				time.Sleep(3 * time.Second)
			}
		}()
	}
	// publishFn 以函数类型声明：nil = 走本地转码（MQ 不可用），非 nil = 走 MQ 发布
	var publishFn func(videoID uint) error
	if mq != nil {
		// ③ 仅 MQ 可用时注入发布函数：投递转码任务到队列；发布失败时 service 内部自动降级本地转码
		publishFn = func(videoID uint) error {
			body, _ := json.Marshal(rabbitmq.TranscodeMessage{VideoID: videoID})
			return mq.Publish(rabbitmq.QueueTranscode, body)
		}
	}
	videohandler.RegisterRoutes(api, db, rdb, toVideoResp, rankSvc, minio, broker, jwtSvc, publishFn, adminRepo)
	//通知模块装配
	commenthandler.RegisterRoutes(api, db, rdb, msgSvc, userBriefBuider, rankSvc, jwtSvc)
	// --- 管理员审核模块装配（仅审核管理员 role=2 可访问） ---
	videoRepo := rpvideo.NewRepository(db)
	adminhandler.RegisterRoutes(api, db, rdb, videoRepo, minio, toVideoResp, jwtSvc)

	r.Run(cfg.ServerPort)
}
