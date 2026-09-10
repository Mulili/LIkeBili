package middleware

import (
	"errors"
	"fmt"

	extracttoken "LikeBili/pkg/extracttoken"
	jwtlib "LikeBili/pkg/jwt"
	"LikeBili/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// OptionalAuth 可选鉴权中间件：尽力解析并注入登录身份，但绝不拦截请求。
// 使用场景：公开读接口，但响应需要按"当前登录用户"填充个性化字段
// （如评论的 IsLiked、关注/粉丝列表的回关与互关状态）。
//
// 与 AuthRequired 的区别（校验逻辑相同，失败处理相反）：
//   - AuthRequired：无 token / 非法 / 已登出 / Redis 故障 → 一律 401/503 并终止请求
//   - OptionalAuth：上述任一情况 → 一律放行，不写上下文，GetUserID 读到 0（游客语义）
//
// 注意：注入的 userId 是"尽力而为"的结果，Handler 不能把它当作已鉴权凭证，
// 只能用于填充展示字段；需要权限判断或写操作的接口仍必须挂 AuthRequired。
func OptionalAuth(jwtSvc *jwtlib.JWT, rdbClient *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		// ① 提取 token（Cookie → Authorization header）；没有 token 即游客，直接放行
		token := extracttoken.ExtractToken(c)
		if token == "" {
			c.Next()
			return
		}

		// ② 解析签名与有效期；解析失败（伪造/过期）等同游客，不报错
		claims, err := jwtSvc.ParseToken(token)
		if err != nil {
			c.Next()
			return
		}

		// ③ 校验 Redis 中的"当前有效 Token"（登出/顶号即失效），key 格式与 AuthRequired 一致
		ctx := c.Request.Context()
		rdbKey := fmt.Sprintf("auth:token:%d", claims.UserID)
		storedToken, err := rdbClient.Get(ctx, rdbKey).Result()
		if err != nil {
			// redis.Nil=已登出；其余=Redis 故障。两种情况都降级为游客放行而不返回 503，
			// 因为公开读接口不应因鉴权组件异常而不可用（AuthRequired 才需要严格失败）。
			if !errors.Is(err, redis.Nil) {
				logger.Warn("可选鉴权 Redis 不可用，按游客处理", zap.String("operation", "OptionalAuth"), zap.Error(err))
			}
			c.Next()
			return
		}
		if storedToken != token {
			// 顶号：该 token 已失效，按游客放行
			c.Next()
			return
		}

		// ④ 校验通过：注入登录身份（与 AuthRequired 写入相同的 key，Handler 读取方式一致）
		c.Set("userId", claims.UserID)
		c.Set("username", claims.Username)
		c.Set("role", claims.Role)
		c.Next()
	}
}
