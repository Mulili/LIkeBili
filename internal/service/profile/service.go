package profile

import (
	modelsProfile "LikeBili/internal/models/profile"
	repocoin "LikeBili/internal/repository/coin"
	repofavorites "LikeBili/internal/repository/favorites"
	repofollow "LikeBili/internal/repository/follow"
	repohistory "LikeBili/internal/repository/history"
	repovideo "LikeBili/internal/repository/video"
	svcmessage "LikeBili/internal/service/message"
	svcuser "LikeBili/internal/service/user"
	"context"
	"fmt"
)

// Service 个人中心聚合服务。
// 职责：把"用户资料 + 各模块统计"聚合成一个响应，避免前端首屏并发调用 5-6 个接口。
// 依赖各模块仓储（只读统计）+ 用户/消息服务（需要它们的既有业务口径）。
type Service struct {
	userSvc   *svcuser.Service          // 用户资料（复用其头像 URL 拼接与"用户不存在"语义）
	msgSvc    *svcmessage.Service       // 未读消息数（复用其 DB/Redis 取较大值的口径）
	follow    *repofollow.Repository    // 关注数 / 粉丝数
	favorites *repofavorites.Repository // 收藏夹数量
	video     *repovideo.Repository     // 投稿数
	history   *repohistory.Repository   // 观看历史条数（仅本人）
	coin      *repocoin.Repository      // 硬币余额（仅本人）
}

// NewService 构造个人中心聚合服务，注入资料服务、消息服务与各模块仓储。
func NewService(userSvc *svcuser.Service, msgSvc *svcmessage.Service, follow *repofollow.Repository,
	favorites *repofavorites.Repository, video *repovideo.Repository,
	history *repohistory.Repository, coin *repocoin.Repository) *Service {
	return &Service{
		userSvc:   userSvc,
		msgSvc:    msgSvc,
		follow:    follow,
		favorites: favorites,
		video:     video,
		history:   history,
		coin:      coin,
	}
}

// GetProfile 聚合个人中心数据。
// viewerID 为当前登录用户（游客为 0），targetID 为被查看用户（路径 :id）。
//
// 分权规则：
//   - 私有数据（硬币余额、观看历史条数）仅在 viewerID == targetID 时返回，他人视角为 nil
//   - 统计口径：本人看全部（含待审核/私密收藏夹），他人只看公开部分（审核通过且公开的投稿、公开收藏夹）
func (s *Service) GetProfile(c context.Context, viewerID, targetID uint) (*modelsProfile.ProfileResp, error) {
	// ① 用户资料：用户不存在时由 user service 返回 ErrUserNotFound（HTTP 404）
	user, err := s.userSvc.GetUser(c, targetID)
	if err != nil {
		return nil, fmt.Errorf("profile.service.GetProfile: %w", err)
	}

	// ② 是否本人视角：游客（0）与任何 id 都不相等，天然按他人处理
	isSelf := viewerID != 0 && viewerID == targetID

	// ③ 关注数 / 粉丝数：公开统计，任何人可见
	followingCount, err := s.follow.CountFollowing(c, targetID)
	if err != nil {
		return nil, fmt.Errorf("profile.service.GetProfile: %w", err)
	}
	followerCount, err := s.follow.CountFollowers(c, targetID)
	if err != nil {
		return nil, fmt.Errorf("profile.service.GetProfile: %w", err)
	}

	// ④ 收藏夹数 / 投稿数：他人视角只统计公开部分（!isSelf）
	favoriteCount, err := s.favorites.CountFavorites(c, targetID, !isSelf)
	if err != nil {
		return nil, fmt.Errorf("profile.service.GetProfile: %w", err)
	}
	videoCount, err := s.video.CountUserVideos(c, targetID, !isSelf)
	if err != nil {
		return nil, fmt.Errorf("profile.service.GetProfile: %w", err)
	}

	resp := &modelsProfile.ProfileResp{
		User:           *user,
		FollowingCount: followingCount,
		FollowerCount:  followerCount,
		FavoriteCount:  favoriteCount,
		VideoCount:     videoCount,
		IsSelf:         isSelf,
	}

	// ⑤ 私有数据：仅本人返回（他人视角保持 nil，JSON 中省略这两个字段）
	if isSelf {
		// 余额：钱包不存在时创建（GetOrCreateWallet），保证本人总能查到自己的余额
		wallet, err := s.coin.GetOrCreateWallet(c, viewerID)
		if err != nil {
			return nil, fmt.Errorf("profile.service.GetProfile: %w", err)
		}
		balance, err := s.coin.FindBalance(c, wallet.ID)
		if err != nil {
			return nil, fmt.Errorf("profile.service.GetProfile: %w", err)
		}

		historyCount, err := s.history.CountHistory(c, viewerID)
		if err != nil {
			return nil, fmt.Errorf("profile.service.GetProfile: %w", err)
		}

		unreadCount, err := s.msgSvc.UnreadCount(c, viewerID)
		if err != nil {
			return nil, fmt.Errorf("profile.service.GetProfile: %w", err)
		}

		resp.CoinBalance = &balance
		resp.HistoryCount = &historyCount
		resp.UnreadCount = &unreadCount
	}

	return resp, nil
}
