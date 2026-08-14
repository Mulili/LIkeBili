package middleware

import (
	"errors"
	"fmt"
	"net/http"

	codeErrors "LikeBili/pkg/errors"
	extracttoken "LikeBili/pkg/extracttoken"
	jwtlib "LikeBili/pkg/jwt"
	"LikeBili/pkg/logger"
	"LikeBili/pkg/response"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// AuthRequired 鉴权中间件：验证请求中的 JWT Token，并校验 Redis 中该用户的"当前有效 Token"。
// 三重校验：① token 存在 → ② 签名/有效期合法 → ③ 与 Redis 中存储的 token 一致（登出/顶号即失效）。
// 校验通过后把用户身份（userId/username/role）注入 gin.Context，供后续 Handler 用 GetUserID 等读取。
// 参数：jwtSvc 持有签名密钥（由 main 注入），rdbClient 用于查询当前有效 Token。
func AuthRequired(jwtSvc *jwtlib.JWT, rdbClient *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		// ① 提取 token：ExtractToken 内部按 Cookie → Authorization header 顺序查找
		token := extracttoken.ExtractToken(c)
		if token == "" {
			response.Error(c, http.StatusUnauthorized, codeErrors.Unauthorized, codeErrors.ErrorUnauthorized.Message)
			c.Abort()
			return
		}

		// ② 解析并验证签名与有效期（jwt/v5 自动校验过期时间）
		claims, err := jwtSvc.ParseToken(token)
		if err != nil {
			response.Error(c, http.StatusUnauthorized, codeErrors.Unauthorized, codeErrors.ErrorUnauthorized.Message)
			c.Abort()
			return
		}

		// ③ 校验 Redis 中的"当前有效 Token"。
		//    key 格式 auth:token:{userID} 必须与 Service 写入时完全一致（见 service.go）
		ctx := c.Request.Context()
		rdbKey := fmt.Sprintf("auth:token:%d", claims.UserID)
		storedToken, err := rdbClient.Get(ctx, rdbKey).Result()
		switch {
		case errors.Is(err, redis.Nil):
			// 未登录/已登出：业务错误 → ErrorFrom 提取 ErrUnauthorized，返回 401 + 20003
			response.ErrorFrom(c, "AuthRequired", codeErrors.ErrUnauthorized)
			c.Abort()
			return
		case err != nil:
			// Redis 故障：凭证本身有效，只是暂时无法验证 → 503（区别于 401"未授权"），
			// 避免前端误以为登录过期而误导用户退出重登。先记日志保留原始错误，再统一走错误码体系。
			logger.Error("鉴权中间件 Redis 不可用", zap.String("operation", "AuthRequired"), zap.Error(err))
			response.ErrorFrom(c, "AuthRequired", codeErrors.ErrorServiceUnavailable)
			c.Abort()
			return
		case storedToken != token:
			// 顶号：该账号在别处重新登录，当前 token 已失效 → 401
			response.ErrorFrom(c, "AuthRequired", codeErrors.ErrUnauthorized)
			c.Abort()
			return
		}

		// 校验通过：把用户身份注入上下文，后续 Handler 通过 GetUserID 等读取
		c.Set("userId", claims.UserID)
		c.Set("username", claims.Username)
		c.Set("role", claims.Role)
		c.Next()
	}
}

// GetUserID 从 gin.Context 读取当前登录用户 ID。
// 必须在 AuthRequired 之后的 Handler 中调用，否则返回 0。
func GetUserID(c *gin.Context) uint {
	value, exist := c.Get("userId")
	if !exist {
		return 0
	}
	return value.(uint)
}

// GetUsername 从 gin.Context 读取当前登录用户用户名。
func GetUsername(c *gin.Context) string {
	value, exist := c.Get("username")
	if !exist {
		return ""
	}
	return value.(string)
}

// GetRole 从 gin.Context 读取当前登录用户角色（签发 Token 时写入）。
func GetRole(c *gin.Context) uint8 {
	value, exist := c.Get("role")
	if !exist {
		return 0
	}
	return value.(uint8)
}
