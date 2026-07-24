package main

import (
	"LikeBili/config"

	"github.com/gin-gonic/gin"
)

func main() {
	config.InitConfig()
	r := gin.Default()

	r.Run(config.AppConfig.App.Port)
}
