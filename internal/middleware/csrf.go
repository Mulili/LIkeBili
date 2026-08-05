package middleware

import (
	codeErrors "LikeBili/pkg/errors"
	"LikeBili/pkg/response"
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"github.com/gin-gonic/gin"
)

type CSRFConfig struct {
	PublicPaths []string
}

func CSRF(cfg CSRFConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		method := c.Request.Method
		if method == "GET" || method == "OPTIONS" || method == "HEAD" {
			c.Next()
			return
		}
		path := c.Request.URL.Path
		for _, p := range cfg.PublicPaths {
			if path == p {
				c.Next()
				return
			}
		}
		csrfHeader := c.GetHeader("X-CSRF-Token")
		if csrfHeader == "" {
			response.Error(c, http.StatusForbidden, codeErrors.Forbidden, "CSRF token 缺失")
			c.Abort()
			return
		}
		csrfCookie, err := c.Cookie("csrf_token")
		if csrfCookie == "" || err != nil {
			response.Error(c, http.StatusForbidden, codeErrors.Forbidden, "CSRF cookie 缺失")
			c.Abort()
			return
		}
		if csrfHeader != csrfCookie {
			response.Error(c, http.StatusForbidden, codeErrors.Forbidden, "CSRF token 不匹配")
			c.Abort()
			return
		}
		c.Next()
	}
}

func GenerateCSRFToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
func SetCSRFCookie(c *gin.Context) (string, error) {
	token, err := GenerateCSRFToken()
	if err != nil {
		//失败时直接报错，事关安全
		return "", err
	}
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("csrf_token", token, 86400, "/", "", false, false)
	return token, nil
}
