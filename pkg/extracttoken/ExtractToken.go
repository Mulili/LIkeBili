package extracttoken

import (
	"strings"

	"github.com/gin-gonic/gin"
)

func ExtractToken(c *gin.Context) string {
	if token, err := c.Cookie("token"); err == nil && token != "" {
		return token
	}
	authHeader := c.GetHeader("Authorization")
	if authHeader != "" {
		part := strings.SplitN(authHeader, " ", 2)
		if len(part) == 2 && part[0] == "Bearer" {
			return part[1]
		}
	}

	return ""
}
