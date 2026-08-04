package main

import (
	"LikeBili/internal/middleware"
	"LikeBili/pkg/config"
	"LikeBili/pkg/database"

	"github.com/gin-gonic/gin"
)

func main() {
	cfg := config.InitConfig()
	db := database.InitMySQL(cfg)
	// rdb := database.InitRedis(cfg)
	r := gin.Default()
	//显式传入CSRF
	api := r.Group("api/v1")
	//显式传入CSRF豁免路径
	api.Use(middleware.CSRF(middleware.CSRFConfig{
		PublicPaths: []string{
			"/api/v1/auth/register",
			"/api/v1/auth/login",
			"/api/v1/auth/refresh",
		},
	}))
	db.AutoMigrate()
	r.Run(cfg.ServerPort)
}
