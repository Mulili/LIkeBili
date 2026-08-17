package middleware

import (
	usermodel "LikeBili/internal/models/user"
	codeErrors "LikeBili/pkg/errors"
	jwtlib "LikeBili/pkg/jwt"
	"LikeBili/pkg/response"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// AdminRequired 审核管理员专用鉴权中间件：先走通用 AuthRequired（token 三重校验），
// 再校验角色必须为审核管理员（role=2）。
// 使用场景：审核视频的接口（待审核列表 / 审核详情 / 审核观看 / 审核操作）。
// 超管（role=3）与普通用户（role=1）都会被拒绝 → 403，保证审核权限严格隔离。
func AdminRequired(jwtSvc *jwtlib.JWT, rdbClient *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		// ① 先执行通用鉴权：token 存在 → 签名/有效期合法 → 与 Redis 一致；
		//    未通过时 AuthRequired 已写出响应并 Abort，这里直接返回
		AuthRequired(jwtSvc, rdbClient)(c)
		if c.IsAborted() {
			return
		}
		// ② 角色校验：仅审核管理员（role=2）可继续
		if GetRole(c) != usermodel.RoleAdmin {
			response.ErrorFrom(c, "AdminRequired", codeErrors.ErrCodeForbidden)
			c.Abort()
			return
		}
		c.Next()
	}
}
