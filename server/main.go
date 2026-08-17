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
	"LikeBili/pkg/storage"
	"LikeBili/pkg/toresp"
	"LikeBili/pkg/validator"
	"log"
	"time"

	"github.com/gin-gonic/gin"
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
	// publishFn 暂不接 MQ → 传 nil，Service 自动降级为本地转码（WithTranscodeRunner）
	videohandler.RegisterRoutes(api, db, rdb, toresp, rankSvc, minio, broker, jwtSvc, nil, adminRepo)
	// --- 管理员审核模块装配（仅审核管理员 role=2 可访问） ---
	videoRepo := rpvideo.NewRepository(db)
	adminhandler.RegisterRoutes(api, db, rdb, videoRepo, minio, toresp, jwtSvc)

	r.Run(cfg.ServerPort)
}
