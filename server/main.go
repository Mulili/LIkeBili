package main

import (
	authhandler "LikeBili/internal/handler/auth"
	userhandler "LikeBili/internal/handler/user"
	"LikeBili/internal/middleware"
	modelsFavorites "LikeBili/internal/models/favorites"
	modelsMeta "LikeBili/internal/models/meta"
	modelsQuality "LikeBili/internal/models/quality"
	modelsTrans "LikeBili/internal/models/transcode"
	modelsUser "LikeBili/internal/models/user"
	favRepo "LikeBili/internal/repository/favorites"
	"LikeBili/pkg/config"
	"LikeBili/pkg/database"
	jwtlib "LikeBili/pkg/jwt"
	"LikeBili/pkg/storage"
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
		&modelsTrans.TranscodeTask{},
		&modelsMeta.VideoMeta{},
		&modelsQuality.VideoQuality{},
	)
	authhandler.RegisterRoutes(api, rdb, db, jwtSvc, tokenTTL, minio, favrepo)
	userhandler.RegisterRoutes(api, db, rdb, minio, jwtSvc)

	r.Run(cfg.ServerPort)
}
