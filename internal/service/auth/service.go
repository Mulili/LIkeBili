package auth

import (
	usermodel "LikeBili/internal/models/user"
	"LikeBili/internal/repository/auth"
	codeErrors "LikeBili/pkg/errors"
	jwtlib "LikeBili/pkg/jwt"
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// Service 封装认证相关的业务逻辑。
// 包含注册、登录、登出及 JWT 令牌管理。
type Service struct {
	repo     *auth.Repository // 用户数据访问层
	db       *gorm.DB         // 数据库连接（用于事务）
	rdb      *redis.Client    // Redis 客户端（用于缓存 Token）
	jwt      *jwtlib.JWT      // JWT 工具（生成和解析 Token）
	tokenTTL time.Duration    // Token 在 Redis 中的有效期
}

// NewService 创建认证服务实例。
// 参数依次为：数据仓库、数据库连接、Redis 客户端、JWT 工具、Token 过期时长。
func NewService(repo *auth.Repository, db *gorm.DB, rdb *redis.Client, jwt *jwtlib.JWT, tokenTTL time.Duration) *Service {
	return &Service{
		repo:     repo,
		db:       db,
		rdb:      rdb,
		jwt:      jwt,
		tokenTTL: tokenTTL,
	}
}

// Register 处理用户注册业务。
// 流程：检查用户名唯一性 → 检查邮箱唯一性 → 密码哈希 → 写入数据库 → 生成 Token → 缓存到 Redis → 创建默认收藏夹
// 参数：
//   - c: 请求上下文
//   - request: 注册请求体（用户名、邮箱、密码）
//
// 返回：
//   - *usermodel.LoginResp: 登录响应（用户 ID、用户名、昵称、头像）
//   - string: JWT Token
//   - error: 业务错误（用户名已存在、邮箱已注册等）
func (s *Service) Register(c context.Context, request *usermodel.RegisterReq) (*usermodel.LoginResp, string, error) {
	// ---------- 第一步：校验用户名唯一性 ----------
	existing, err := s.repo.FindByUsername(c, request.Username)
	if existing != nil {
		// 用户名已被占用，返回预定义的业务错误
		return nil, "", fmt.Errorf("Method:auth.service.Register: %w", codeErrors.ErrUsernameExists)
	}
	if err != nil {
		// 数据库查询异常，包裹原始错误以便日志追踪
		return nil, "", fmt.Errorf("Method:auth.service.Register: %w", err)
	}

	// ---------- 第二步：校验邮箱唯一性 ----------
	existing, err = s.repo.FindByEmail(c, request.Email)
	if existing != nil {
		return nil, "", fmt.Errorf("Method:auth.service.Register: %w", codeErrors.ErrEmailExists)
	}
	if err != nil {
		return nil, "", fmt.Errorf("Method:auth.service.Register: %w", err)
	}

	// ---------- 第三步：密码哈希处理 ----------
	// 使用 bcrypt 对明文密码进行哈希，cost=12（计算强度，越高越安全但越慢）
	hash, err := bcrypt.GenerateFromPassword([]byte(request.Password), 12)
	if err != nil {
		return nil, "", fmt.Errorf("Method:auth.service.Register: %w", err)
	}

	// ---------- 第四步：构建用户对象并写入数据库 ----------
	user := &usermodel.User{
		Username:     request.Username,
		Email:        request.Email,
		PasswordHash: string(hash),
		Nickname:     request.Username, // 默认昵称与用户名相同
	}
	if err := s.repo.Create(c, user); err != nil {
		return nil, "", fmt.Errorf("Method:auth.service.Register: %w", err)
	}

	// ---------- 第五步：生成 JWT Token ----------
	token, err := s.jwt.GenerateToken(user.ID, user.Username, user.Role)
	if err != nil {
		return nil, "", fmt.Errorf("Method:auth.service.Register: %w", err)
	}

	// ---------- 第六步：Token 缓存到 Redis ----------
	// Redis key 格式：auth:token:{userID}，用于登出时失效 Token
	rdbKey := fmt.Sprintf("auth:token:%d", user.ID)
	if err := s.rdb.Set(c, rdbKey, token, s.tokenTTL).Err(); err != nil {
		return nil, "", fmt.Errorf("Method:auth.service.Register: %w", err)
	}
	// ---------- 第七步：为新用户创建默认收藏夹 ----------
	// 查询是否已存在默认收藏夹（防止重复创建）
	var defaultFvrts int64
	s.db.WithContext(c).Table("favorites").
		Where("user_id = ? and name = ?", user.ID, "默认收藏夹").
		Count(&defaultFvrts)
	if defaultFvrts == 0 {
		// 不存在则插入一条默认收藏夹记录
		s.db.WithContext(c).Exec(
			"INSERT INTO favorites (user_id, name, is_public, created_at) VALUES (?, ?, 1, NOW())",
			user.ID, "默认收藏夹",
		)
	}

	// ---------- 第八步：组装响应返回 ----------
	resp := &usermodel.LoginResp{
		ID:       user.ID,
		Username: user.Username,
		Nickname: user.Nickname,
		Avatar:   user.Avatar,
	}
	return resp, token, nil
}
