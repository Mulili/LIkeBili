package main

import (
	adminhandler "LikeBili/internal/handler/admin"
	authhandler "LikeBili/internal/handler/auth"
	userhandler "LikeBili/internal/handler/user"
	videohandler "LikeBili/internal/handler/video"
	"LikeBili/internal/middleware"
	modelsFavorites "LikeBili/internal/models/favorites"
	modelsMeta "LikeBili/internal/models/meta"
	modelsQuality "LikeBili/internal/models/quality"
	modelsReview "LikeBili/internal/models/review"
	modelsTrans "LikeBili/internal/models/transcode"
	modelsUser "LikeBili/internal/models/user"
	modelsVideo "LikeBili/internal/models/video"
	adminRepo "LikeBili/internal/repository/admin"
	favRepo "LikeBili/internal/repository/favorites"
	rpvideo "LikeBili/internal/repository/video"
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
	)
	authhandler.RegisterRoutes(api, rdb, db, jwtSvc, tokenTTL, minio, favrepo)
	userhandler.RegisterRoutes(api, db, rdb, minio, jwtSvc)
	// --- 视频模块装配 ---
	broker := transcode.NewProgressBroker()     // 转码进度广播器（前端 SSE 订阅用）
	toresp := toresp.NewVideoRespBuilder(minio) // 视频 DTO 转换器
	rankSvc := rank.NewService(rdb)             // 热度排行榜服务
	adminRepo := adminRepo.NewRepository(db)    // 审核记录查询器（作者端驳回原因展示）

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
	videohandler.RegisterRoutes(api, db, rdb, toresp, rankSvc, minio, broker, jwtSvc, publishFn, adminRepo)
	// --- 管理员审核模块装配（仅审核管理员 role=2 可访问） ---
	videoRepo := rpvideo.NewRepository(db)
	adminhandler.RegisterRoutes(api, db, rdb, videoRepo, minio, toresp, jwtSvc)

	r.Run(cfg.ServerPort)
}
