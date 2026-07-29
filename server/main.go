package main

import (
	"LikeBili/pkg/config"
	"LikeBili/pkg/database"

	"github.com/gin-gonic/gin"
)

func main() {
	cfg := config.InitConfig()
	db := database.InitMySQL(cfg)
	// rdb := database.InitRedis(cfg)
	r := gin.Default()
	db.AutoMigrate()
	r.Run(cfg.ServerPort)
}
