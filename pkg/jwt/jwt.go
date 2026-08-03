package jwt

import (
	"fmt"
	"time"

	jwtlib "github.com/golang-jwt/jwt/v5"
)

// UserClaims 自定义 JWT 载荷，包含用户基本信息和标准注册声明。
type UserClaims struct {
	jwtlib.RegisteredClaims
	UserID   uint   `json:"user_id"`
	Username string `json:"username"`
	Role     uint8  `json:"role"` //控制为用户或者是管理员
}

// JWT 封装签名密钥和过期配置，避免每次调用都传入 secret。
type JWT struct {
	secret []byte        // HMAC 签名密钥
	expire time.Duration // Token 过期时长
}

// New 创建 JWT 实例，注入一次配置即可复用。
func New(secret string, expire time.Duration) *JWT {
	return &JWT{
		secret: []byte(secret),
		expire: expire,
	}
}

// GenerateToken 为用户生成签名的 JWT Token 字符串。
// Token 包含用户 ID、用户名、角色，有效期由构造时传入的 expire 决定。
func (j *JWT) GenerateToken(userID uint, username string, role uint8) (string, error) {
	//构造载荷，生成标准字段表示由谁签发token，以及到期时间和起始时间
	claims := &UserClaims{
		UserID:   userID,
		Username: username,
		Role:     role,
		//标准字段
		RegisteredClaims: jwtlib.RegisteredClaims{
			Issuer:    "LikeBili",                                      //token由谁签发
			ExpiresAt: jwtlib.NewNumericDate(time.Now().Add(j.expire)), //token过期时间
			IssuedAt:  jwtlib.NewNumericDate(time.Now()),               //token何时签发
		},
	}
	//使用HS256
	token := jwtlib.NewWithClaims(jwtlib.SigningMethodHS256, claims)
	//返回字符串
	return token.SignedString(j.secret)
}

// ParseToken 解析并验证 JWT Token，返回 Claims。
// 会验证签名算法（仅接受 HMAC）和 Token 有效期。
func (j *JWT) ParseToken(tokenStr string) (*UserClaims, error) {
	token, err := jwtlib.ParseWithClaims(tokenStr, &UserClaims{}, func(token *jwtlib.Token) (any, error) {
		//判断是否为HMAC算法，防止使用其他算法伪造 token
		if _, ok := token.Method.(*jwtlib.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		//返回结构体中存储的密钥
		return j.secret, nil
	})
	if err != nil {
		return nil, fmt.Errorf("jwt.ParseToken: %w", err)
	}

	//类型断言，取出 Claims 并检查 Token 是否有效
	claims, ok := token.Claims.(*UserClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("jwt.ParseToken: invalid token")
	}

	return claims, nil
}

// ParseTokenUnverified 不验证签名和有效期，仅解析 Token 载荷。
// 用于刷新 Token 等场景（如 Token 已过期但仍需读取用户信息），调用方应自行验证。
func (j *JWT) ParseTokenUnverified(tokenStr string) (*UserClaims, error) {
	token, _, err := new(jwtlib.Parser).ParseUnverified(tokenStr, &UserClaims{})
	if err != nil {
		return nil, fmt.Errorf("jwt.ParseTokenUnverified: %w", err)
	}

	claims, ok := token.Claims.(*UserClaims)
	if !ok {
		return nil, fmt.Errorf("jwt.ParseTokenUnverified: invalid claims type")
	}

	return claims, nil
}
