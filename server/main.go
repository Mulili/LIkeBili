package main

import (
	adminhandler "LikeBili/internal/handler/admin"
	authhandler "LikeBili/internal/handler/auth"
	coinhandler "LikeBili/internal/handler/coin"
	commenthandler "LikeBili/internal/handler/comment"
	favoriteshandler "LikeBili/internal/handler/favorites"
	followhandler "LikeBili/internal/handler/follow"
	historyhandler "LikeBili/internal/handler/history"
	likehandler "LikeBili/internal/handler/like"
	messagehandler "LikeBili/internal/handler/message"
	profilehandler "LikeBili/internal/handler/profile"
	rankhandler "LikeBili/internal/handler/rank"
	searchhandler "LikeBili/internal/handler/search"
	userhandler "LikeBili/internal/handler/user"
	videohandler "LikeBili/internal/handler/video"
	"LikeBili/internal/middleware"
	modelsCoins "LikeBili/internal/models/coin"
	modelsComments "LikeBili/internal/models/comments"
	modelsFavorites "LikeBili/internal/models/favorites"
	modelsFollow "LikeBili/internal/models/follow"
	modelsHistory "LikeBili/internal/models/history"
	modelsMeta "LikeBili/internal/models/meta"
	modelsQuality "LikeBili/internal/models/quality"
	modelsReview "LikeBili/internal/models/review"
	modelsSearch "LikeBili/internal/models/search"
	modelsTrans "LikeBili/internal/models/transcode"
	modelsUser "LikeBili/internal/models/user"
	modelsVideo "LikeBili/internal/models/video"
	adminRepo "LikeBili/internal/repository/admin"
	repocoin "LikeBili/internal/repository/coin"
	favRepo "LikeBili/internal/repository/favorites"
	rpmessage "LikeBili/internal/repository/message"
	rpsearch "LikeBili/internal/repository/search"
	rpvideo "LikeBili/internal/repository/video"
	svccoin "LikeBili/internal/service/coin"
	svcmessage "LikeBili/internal/service/message"
	"LikeBili/internal/service/rank"
	svcsearch "LikeBili/internal/service/search"
	"LikeBili/internal/transcode"
	"LikeBili/pkg/config"
	"LikeBili/pkg/database"
	jwtlib "LikeBili/pkg/jwt"
	"LikeBili/pkg/logger"
	"LikeBili/pkg/rabbitmq"
	"LikeBili/pkg/storage"
	"LikeBili/pkg/toresp"
	"LikeBili/pkg/validator"
	"context"
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
	// 热度排行榜服务
	rankSvc := rank.NewService(rdb)
	tokenTTL := time.Duration(cfg.TokenTTLDays) * 24 * time.Hour
	jwtSvc := jwtlib.New(cfg.JWTSecret, tokenTTL)
	favrepo := favRepo.NewRepository(db)
	// 币模块装配：注册/登录时签到发币（auth 依赖 coin，需先构造）
	coinRepo := repocoin.NewRepository(db)
	coinSvc := svccoin.NewService(coinRepo, rankSvc)
	// DTO 转换器：跨模块统一出口（提前构造，搜索等模块装配时也要用到）
	userBriefBuider := toresp.NewToRespBuilder(minio)
	toVideoResp := toresp.NewVideoRespBuilder(minio, userBriefBuider)
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
		&modelsFavorites.FavoritesItem{},
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
		&modelsFollow.Follow{},
		&modelsHistory.UserHistory{},
		&modelsSearch.VideoSearch{},
	)
	// 分类字典 seed（幂等）：上传时选分类、按分类筛选、搜索按分类名检索都依赖这份字典
	if err := rpvideo.NewRepository(db).EnsureCategories(context.Background()); err != nil {
		logger.Warn("分类字典初始化失败", zap.String("operation", "EnsureCategories"), zap.Error(err))
	}
	// 搜索检索表初始化：
	//  ① 幂等创建 ngram 全文索引（不用 AutoMigrate，避免生成不带分词器的同名索引导致中文检索失效）
	//  ② 检索表为空时全量回填既有视频（首次部署或重建索引时执行一次，之后靠写路径同步）
	searchRepo := rpsearch.NewRepository(db)
	if err := searchRepo.EnsureFulltextIndex(context.Background()); err != nil {
		logger.Warn("搜索全文索引初始化失败", zap.String("operation", "EnsureFulltextIndex"), zap.Error(err))
	} else if empty, err := searchRepo.IsEmpty(context.Background()); err != nil {
		logger.Warn("搜索检索表状态检查失败", zap.String("operation", "IsEmpty"), zap.Error(err))
	} else if empty {
		if err := searchRepo.RebuildSearchIndex(context.Background()); err != nil {
			logger.Warn("搜索索引全量回填失败", zap.String("operation", "RebuildSearchIndex"), zap.Error(err))
		}
	}
	// 搜索服务：同一实例既处理搜索请求，也注入给 video/user/admin 做检索表同步
	searchSvc := svcsearch.NewService(searchRepo, rpvideo.NewRepository(db), toVideoResp)
	authhandler.RegisterRoutes(api, rdb, db, jwtSvc, tokenTTL, minio, favrepo, coinSvc)
	userhandler.RegisterRoutes(api, db, rdb, minio, jwtSvc, searchSvc)
	// --- 视频模块装配 ---
	broker := transcode.NewProgressBroker() // 转码进度广播器（前端 SSE 订阅用）

	adminRepo := adminRepo.NewRepository(db) // 审核记录查询器（作者端驳回原因展示）
	//--- 点赞模块装配：notifier 复用 message 服务，rank 复用热度服务 ---
	msgRepo := rpmessage.NewRepository(db)
	msgSvc := svcmessage.NewService(msgRepo, rdb, userBriefBuider)
	likehandler.RegisterRoutes(api, db, rdb, msgSvc, rankSvc, jwtSvc)
	// 币模块路由：/video/:id/coin*（投币）+ /coin/balance（个人中心余额）
	coinhandler.RegisterRoutes(api, db, jwtSvc, rdb, rankSvc)
	// 热门榜路由：GET /rank/hot?window=day|week|month&top=20（公开）
	// 冷启动兜底：Redis 无埋点数据时按播放量取 TopN（DB 只有 Views 计数，过渡方案）
	rankhandler.RegisterRoutes(api, rankSvc, func(ctx context.Context, top int) ([]uint, error) {
		return rpvideo.NewRepository(db).TopByViews(ctx, top)
	})
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
	videohandler.RegisterRoutes(api, db, rdb, toVideoResp, rankSvc, minio, broker, jwtSvc, publishFn, adminRepo, searchSvc)
	//通知模块装配
	commenthandler.RegisterRoutes(api, db, rdb, msgSvc, userBriefBuider, rankSvc, jwtSvc)
	// --- 观看历史模块装配（登录用户私有数据） ---
	// 路由：POST /history 上报进度、GET /history 分页列表；
	// 依赖 toVideoResp 统一转换内嵌的视频信息（封面/头像 URL 规则一致）
	historyhandler.RegisterRoutes(api, db, rdb, jwtSvc, toVideoResp)
	// --- 关注模块装配 ---
	// 路由：POST/DELETE /users/:id/follow（关注、取关/回关，需登录）、
	//       GET /users/:id/followings|followers（关注/粉丝列表，公开可看）；
	// notifier 复用 message 服务（通用 SendNotification + MsgTypeFollow=3），
	// userBriefBuider 负责列表行内用户信息转换（与全站头像 URL 规则一致）
	followhandler.RegisterRoutes(api, db, rdb, msgSvc, userBriefBuider, jwtSvc)
	// --- 收藏夹模块装配 ---
	// 路由：POST/GET /favorites（创建、我的收藏夹）、GET/POST /favorites/:id/items（详情、收藏/取消）、
	//       GET /users/:id/favorites（他人公开收藏夹）；
	// minio 用于收藏夹封面预签名 URL，toVideoResp 统一转换夹内视频条目（URL 规则全站一致）
	favoriteshandler.RegisterRoutes(api, db, rdb, minio, toVideoResp, jwtSvc)
	// --- 个人中心模块装配（聚合接口） ---
	// 路由：GET /users/:id/profile（公开 + 可选鉴权）——
	// 一次返回资料 + 关注数/粉丝数/收藏夹数/投稿数；本人访问额外带硬币余额与历史条数
	profilehandler.RegisterRoutes(api, db, rdb, minio, jwtSvc)
	// --- 消息（通知）模块装配 ---
	// 路由：GET /messages（列表 + 未读数）、POST /messages/read-all（全部已读）、
	//       POST /messages/:id/read（单条已读）；通知只属于接收者本人，整组需登录
	messagehandler.RegisterRoutes(api, db, rdb, userBriefBuider, jwtSvc)
	// --- 搜索模块装配 ---
	// 路由：GET /search/videos（公开）——关键词 ≥2 字走 video_search 全文检索，1 字降级为分类名兜底
	searchhandler.RegisterRoutes(api, searchSvc)
	// --- 管理员审核模块装配（仅审核管理员 role=2 可访问） ---
	videoRepo := rpvideo.NewRepository(db)
	adminhandler.RegisterRoutes(api, db, rdb, videoRepo, minio, toVideoResp, jwtSvc, searchSvc)

	r.Run(cfg.ServerPort)
}
