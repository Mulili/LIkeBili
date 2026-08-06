package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin == "" {
			c.Next()
			return
		}
		// 回显请求源：配合 Allow-Credentials 时不能用 *，只能精确到源
		c.Header("Access-Control-Allow-Origin", origin)
		c.Header("Access-Control-Allow-Credentials", "true")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		// 前端用到的自定义头必须都列出来，否则预检失败
		c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type, X-CSRF-Token")
		c.Header("Access-Control-Max-Age", "86400") // 缓存预检结果 24 小时

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
