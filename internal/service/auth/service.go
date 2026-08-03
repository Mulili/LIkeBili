// Package auth 提供认证模块的业务逻辑层（Service）实现。
// 职责：注册、登录、获取当前用户信息、刷新 Token，并管理 Token 在 Redis 中的生命周期。
// 调用链：Handler → Service → Repository →（GORM / Redis / MinIO）。
package auth

import (
	modelsFavorites "LikeBili/internal/models/favorites"
	usermodel "LikeBili/internal/models/user"
	"LikeBili/internal/repository/auth"
	"LikeBili/internal/repository/favorites"
	codeErrors "LikeBili/pkg/errors"
	jwtlib "LikeBili/pkg/jwt"
	"LikeBili/pkg/storage"
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
)

// Service 封装认证相关的业务逻辑。
// 包含注册、登录、获取当前用户信息、刷新 Token，以及 Token 在 Redis 中的生命周期管理。
// 注意：当前没有 Logout 方法，"登出即失效 Token"（删除 auth:token:{userID}）的逻辑待补充。
type Service struct {
	repo     *auth.Repository      // 用户数据访问层：负责 users 表的查询与写入
	rdb      *redis.Client         // Redis 客户端：缓存每个用户当前有效的 Token
	favorite *favorites.Repository // 收藏夹数据访问层：注册时为新用户创建默认收藏夹
	jwt      *jwtlib.JWT           // JWT 工具：生成与解析 Token
	tokenTTL time.Duration         // Token 统一有效期（JWT 与 Redis 的 TTL 共用）
	storage  *storage.MinIO        // 对象存储：把存储的对象名转换为可公开访问的 URL
}

// NewService 创建认证服务实例。
// 通过构造函数注入全部依赖（依赖倒置），便于在单元测试中替换为 mock 实现。
// 参数依次为：用户数据仓库、数据库连接（预留事务）、Redis 客户端、JWT 工具、Token 过期时长、对象存储、收藏夹数据仓库。
func NewService(repo *auth.Repository, rdb *redis.Client, jwt *jwtlib.JWT, tokenTTL time.Duration, storage *storage.MinIO, favorite *favorites.Repository) *Service {
	return &Service{
		repo:     repo,
		rdb:      rdb,
		jwt:      jwt,
		tokenTTL: tokenTTL,
		storage:  storage,
		favorite: favorite,
	}
}

// Register 处理用户注册业务。
// 流程：检查用户名唯一性 → 检查邮箱唯一性 → 密码哈希 → 写入数据库 → 生成 Token → 缓存到 Redis → 创建默认收藏夹。
// 注册成功后直接返回 Token，实现"注册即登录"，前端无需再走一次登录流程。
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
	if err != nil {
		// 数据库查询异常，包裹原始错误以便日志追踪
		return nil, "", fmt.Errorf("Method:auth.service.Register: %w", err)
	}
	if existing != nil {
		// 用户名已被占用，返回预定义的业务错误
		return nil, "", fmt.Errorf("Method:auth.service.Register: %w", codeErrors.ErrUsernameExists)
	}

	// ---------- 第二步：校验邮箱唯一性 ----------
	existing, err = s.repo.FindByEmail(c, request.Email)
	if err != nil {
		return nil, "", fmt.Errorf("Method:auth.service.Register: %w", err)
	}
	if existing != nil {
		return nil, "", fmt.Errorf("Method:auth.service.Register: %w", codeErrors.ErrEmailExists)
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
		return nil, "", fmt.Errorf("Method:auth.service.Register: %w", codeErrors.Wrap(err, codeErrors.CodeInternal, "服务器繁忙,账号可能已被注册,请移步登录"))
	}
	// ---------- 第七步：为新用户创建默认收藏夹 ----------
	// 通过收藏夹仓库（而非直接 SQL）操作，保持分层结构。
	// 先查询是否已存在默认收藏夹（防止重复创建），不存在则插入一条公开记录。
	var defaultFvrts *modelsFavorites.Favorites
	defaultFvrts, _ = s.favorite.FindDefaultFavorite(c, user.ID)
	if defaultFvrts == nil {
		// 不存在则插入一条默认收藏夹记录（is_public=1 公开，所有用户可见）
		s.favorite.Create(c, &modelsFavorites.Favorites{
			UserID:   user.ID,
			Name:     "默认收藏夹",
			IsPublic: 1,
		})
	}

	userAvatar := ""
	if user.Avatar != "" {
		userAvatar = s.storage.GetObjectURL(user.Avatar)
	}

	// ---------- 第八步：组装响应返回 ----------
	resp := &usermodel.LoginResp{
		ID:       user.ID,
		Username: user.Username,
		Nickname: user.Nickname,
		Avatar:   userAvatar,
	}
	return resp, token, nil
}

// Login 处理用户登录业务。
// 流程：按用户名或邮箱定位用户 → 校验账号状态（封禁拦截）→ 比对密码 → 生成 Token → 缓存到 Redis → 返回用户信息。
// 参数：
//   - c: 请求上下文
//   - account: 登录账号（用户名或邮箱均可）
//   - password: 明文密码（在 Service 层与哈希比对，密码本身不落库、不返回）
//
// 返回：
//   - *usermodel.LoginResp: 登录响应
//   - string: JWT Token
//   - error: 业务错误（用户不存在 / 密码错误 / 账号被封禁等）
func (s *Service) Login(c context.Context, account, password string) (*usermodel.LoginResp, string, error) {
	user, err := s.repo.FindByUsernameOrEmail(c, account)
	if err != nil {
		return nil, "", fmt.Errorf("Method:auth.service.Login: %w", err)
	}
	if user == nil {
		return nil, "", fmt.Errorf("Method:auth.service.Login: %w", codeErrors.ErrUserNotFound)
	}
	if user.Status == -1 {
		// 账号处于封禁状态（Status=-1），拒绝登录并返回封禁业务错误
		return nil, "", fmt.Errorf("Method:auth.service.Login: %w", codeErrors.ErrCodeUserIsBan)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, "", fmt.Errorf("Method:auth.service.Login: %w", codeErrors.ErrWrongPassword)
	}
	token, err := s.jwt.GenerateToken(user.ID, user.Username, user.Role)
	if err != nil {
		return nil, "", fmt.Errorf("Method:auth.service.Login: %w", err)
	}

	rdbkey := fmt.Sprintf("auth:token:%d", user.ID)
	if err := s.rdb.Set(c, rdbkey, token, s.tokenTTL).Err(); err != nil {
		return nil, "", fmt.Errorf("Method:auth.service.Login: %w", codeErrors.Wrap(err, codeErrors.CodeInternal, "服务器繁忙,请稍后重试"))
	}
	userAvatar := ""
	if user.Avatar != "" {
		userAvatar = s.storage.GetObjectURL(user.Avatar)
	}
	resp := &usermodel.LoginResp{
		ID:       user.ID,
		Username: user.Username,
		Nickname: user.Nickname,
		Avatar:   userAvatar,
	}
	return resp, token, nil
}

// FindMe 获取当前登录用户的详细信息。
// userid 由 JWT 鉴权中间件从 Token 中解析后传入，返回用户主页所需的信息。
// 若用户设置了头像（存的是对象名），会通过对象存储转换为可访问的 URL 后返回。
func (s *Service) FindMe(c context.Context, userid uint) (*usermodel.UserInfoResp, error) {
	user, err := s.repo.FindByID(c, userid)
	if err != nil {
		return nil, fmt.Errorf("Method:auth.service.FindMe: %w", err)
	}
	if user == nil {
		return nil, fmt.Errorf("Method:auth.service.FindMe: %w", codeErrors.ErrUserNotFound)
	}
	userAvatar := ""
	if user.Avatar != "" {
		userAvatar = s.storage.GetObjectURL(user.Avatar)
	}
	resp := &usermodel.UserInfoResp{
		ID:        user.ID,
		Username:  user.Username,
		Nickname:  user.Nickname,
		Avatar:    userAvatar,
		Bio:       user.Bio,
		CreatedAt: user.CreatedAt,
	}
	return resp, nil
}

// Refresh 刷新令牌，对用户透明。
// 设计说明：
//   - 使用 ParseTokenUnverified 仅解析 Token（不验签、不检查过期），
//     因为本接口允许 Token 过期后仍能换取新 Token。
//   - 安全性由 Redis 兜底：只有 Redis 中缓存的值与传入 Token 完全一致时才允许刷新，
//     因此伪造、被顶替或已被登出的 Token 都无法通过校验。
//   - 刷新成功后生成新 Token 并覆盖 Redis 中的旧值，旧 Token 立即失效。
//
// 参数 oldToken: 当前持有的 Token。
// 返回：新 Token 或错误（Token 无效 / 未授权）。
func (s *Service) Refresh(c context.Context, oldToken string) (string, error) {
	claims, err := s.jwt.ParseTokenUnverified(oldToken)
	if err != nil {
		return "", fmt.Errorf("Method:auth.service.Refresh: %w", codeErrors.ErrTokenInvalid)
	}
	rdbkey := fmt.Sprintf("auth:token:%d", claims.UserID)
	storedToken, err := s.rdb.Get(c, rdbkey).Result()
	if err != nil || storedToken != oldToken {
		return "", fmt.Errorf("Method:auth.service.Refresh: %w", codeErrors.ErrUnauthorized)
	}

	newToken, err := s.jwt.GenerateToken(claims.UserID, claims.Username, claims.Role)
	if err != nil {
		return "", fmt.Errorf("Method:auth.service.Refresh: %w", err)
	}
	if err := s.rdb.Set(c, rdbkey, newToken, s.tokenTTL).Err(); err != nil {
		return "", fmt.Errorf("Method:auth.service.Refresh: %w", err)
	}
	return newToken, nil
}
