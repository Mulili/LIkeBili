package follow

import (
	modelsFollow "LikeBili/internal/models/follow"
	modelsMessage "LikeBili/internal/models/message"
	modelsUser "LikeBili/internal/models/user"
	repofollow "LikeBili/internal/repository/follow"
	codeErrors "LikeBili/pkg/errors"
	"LikeBili/pkg/logger"
	"LikeBili/pkg/toresp"
	"context"
	"fmt"

	"go.uber.org/zap"
)

// deletedUserNickname 已注销用户的官方统一展示昵称。
// 行内对端用户已注销（软删）时以此占位，与评论模块"官方删除占位"同思路。
const deletedUserNickname = "用户已注销"

// Notifier 关注通知抽象（预留扩展点）。
// 复用 message 模块的通用通知方法，不新增专用通知函数——
// 接口形状与 like/comment 模块完全一致，由 message.Service 的 SendNotification 实现并注入。
// 通知类型用 MsgTypeFollow=3（关注通知，常量定义在 message 模块，防枚举错位）。
// notifier 为 nil 时跳过通知，不影响关注主流程（fail-open）。
type Notifier interface {
	// SendNotification 写一条通知：userID=接收者，fromUserID=触发者，msgType=消息类型，
	// targetID=关联对象 ID，content=展示文案。
	SendNotification(c context.Context, userID, fromUserID uint, msgType uint8, targetID uint, content string) error
}

type Service struct {
	repo     *repofollow.Repository       // 关注关系访问层
	toresp   *toresp.UserBriefRespBuilder // 用户 DTO 转换器：复用 ToUserBriefResp 统一拼头像公开 URL
	notifier Notifier                     // 关注通知器（预留，nil 时静默跳过）
}

// NewService 构造关注服务，注入关系仓储、用户 DTO 转换器与通知器。
func NewService(repo *repofollow.Repository, toresp *toresp.UserBriefRespBuilder, notifier Notifier) *Service {
	return &Service{repo: repo, toresp: toresp, notifier: notifier}
}

// Follow 关注某人（关注/回关共用同一方法，动作语义相同，由调用方决定触发场景）。
func (s *Service) Follow(c context.Context, followerID, followeeID uint) error {
	// ① 自关注防御：关注自己无意义，直接拒绝
	if followerID == followeeID {
		return codeErrors.Wrap(codeErrors.ErrorBadRequest, codeErrors.BadRequest, "不能关注自己")
	}

	// ② 幂等插入：isNew=true 表示本次首次新增（重复点击/已关注过则 false，不再触发通知）
	isNew, err := s.repo.Follow(c, followerID, followeeID)
	if err != nil {
		return fmt.Errorf("follow.service.Follow: %w", err)
	}

	// ③ 首次关注成功 → 通知被关注者（MsgTypeFollow=3 关注通知，复用 message 通用方法）
	//    文案带关注者展示名（昵称优先，回退"用户"）；通知失败仅记日志，不阻塞关注成功
	if isNew && s.notifier != nil {
		nickname := "用户"
		if name, err := s.repo.FindNickname(c, followerID); err == nil && name != "" {
			nickname = name
		}
		content := fmt.Sprintf("%s 关注了你", nickname)
		if err := s.notifier.SendNotification(c, followeeID, followerID, modelsMessage.MsgTypeFollow, followerID, content); err != nil {
			logger.Warn("关注通知发送失败", zap.String("operation", "follow.service.Follow"),
				zap.Uint64("follower_id", uint64(followerID)),
				zap.Uint64("followee_id", uint64(followeeID)), zap.Error(err))
		}
	}
	return nil
}

// Unfollow 解除关注（取关）。不存在该关系时仓库层幂等返回，不报错。
func (s *Service) Unfollow(c context.Context, followerID, followeeID uint) error {
	// 与关注对称的自关注/无效目标防御可复用同一校验
	if followerID == followeeID {
		return codeErrors.Wrap(codeErrors.ErrorBadRequest, codeErrors.BadRequest, "不能取消关注自己")
	}
	if err := s.repo.Unfollow(c, followerID, followeeID); err != nil {
		return fmt.Errorf("follow.service.Unfollow: %w", err)
	}
	return nil
}

// ListFollowing 分页展示某用户(targetID)的关注列表（公共可看）。
// viewerID 为当前登录用户（未登录传 0），用于填充行内"我关注他/他关注我"状态。
func (s *Service) ListFollowing(c context.Context, viewerID, targetID uint, page, pageSize int) (*modelsFollow.FollowListResp, error) {
	// ① 分页防御：page 最小 1；pageSize 越界（<1 或 >50）回退默认 16
	page, pageSize = normalizePage(page, pageSize)

	// ② 查关注关系行（仓库层 Preload 对端用户）
	rows, total, err := s.repo.ListFollowing(c, targetID, page, pageSize)
	if err != nil {
		return nil, fmt.Errorf("follow.service.ListFollowing: %w", err)
	}

	// ③ 组装行（含注销占位与 viewer 关系状态）
	items, err := s.buildItems(c, viewerID, rows, false)
	if err != nil {
		return nil, fmt.Errorf("follow.service.ListFollowing: %w", err)
	}

	return &modelsFollow.FollowListResp{
		Items:    items,
		Total:    total,
		Page:     uint16(page),
		PageSize: uint16(pageSize),
	}, nil
}

// ListFollowers 分页展示某用户(targetID)的粉丝列表（公共可看）。
// viewerID 语义同 ListFollowing：粉丝行的 IsFollowing 即"回关"按钮状态。
func (s *Service) ListFollowers(c context.Context, viewerID, targetID uint, page, pageSize int) (*modelsFollow.FollowListResp, error) {
	// ① 分页防御
	page, pageSize = normalizePage(page, pageSize)

	// ② 查粉丝关系行
	rows, total, err := s.repo.ListFollowers(c, targetID, page, pageSize)
	if err != nil {
		return nil, fmt.Errorf("follow.service.ListFollowers: %w", err)
	}

	// ③ 组装行
	items, err := s.buildItems(c, viewerID, rows, true)
	if err != nil {
		return nil, fmt.Errorf("follow.service.ListFollowers: %w", err)
	}

	return &modelsFollow.FollowListResp{
		Items:    items,
		Total:    total,
		Page:     uint16(page),
		PageSize: uint16(pageSize),
	}, nil
}

// buildItems 把一页关系行组装成列表项 DTO：
//   - 行内对端用户已注销（软删 → Preload 过滤后 User.ID==0）→ 用关系行保留的原 ID 构造官方"已注销"占位；
//     未注销 → 复用 toresp.ToUserBriefResp 转换（头像拼公开 URL），避免各模块 URL 规则分叉
//   - 行状态以 viewerID 为准：一次 IN 批量取两方向关系集合，避免 N+1
func (s *Service) buildItems(c context.Context, viewerID uint, rows []modelsFollow.Follow, isFanList bool) ([]modelsFollow.FollowUserItemResp, error) {
	items := make([]modelsFollow.FollowUserItemResp, 0, len(rows))
	if len(rows) == 0 {
		return items, nil
	}

	// ① 收集本页对端用户 ID：粉丝列表取 FollowerID，关注列表取 FolloweeID
	targetIDs := make([]uint, len(rows))
	for i := range rows {
		if isFanList {
			targetIDs[i] = rows[i].FollowerID
		} else {
			targetIDs[i] = rows[i].FolloweeID
		}
	}

	// ② 批量取 viewer 与这些用户的两方向关系集合（游客 viewerID==0 跳过查询，状态保持 false）
	var followingSet, followedSet map[uint]struct{}
	if viewerID != 0 {
		ids, err := s.repo.FindFollowingIDs(c, viewerID, targetIDs)
		if err != nil {
			return nil, err
		}
		followingSet = toSet(ids)
		ids, err = s.repo.FindFollowerIDs(c, viewerID, targetIDs)
		if err != nil {
			return nil, err
		}
		followedSet = toSet(ids)
	}

	// ③ 逐行组装
	for i := range rows {
		var user modelsUser.User // 对端用户模型（关系行内嵌的关联）
		var relID uint           // 对端用户真实 ID（行字段，注销时兜底构造占位）
		if isFanList {
			user, relID = rows[i].Follower, rows[i].FollowerID
		} else {
			user, relID = rows[i].Followee, rows[i].FolloweeID
		}

		// 已注销占位：保留原 ID（前端锚定/去重用），昵称统一替换为官方文案，头像留空走前端默认
		var brief *modelsUser.UserBrief
		if user.ID == 0 {
			brief = &modelsUser.UserBrief{ID: relID, Nickname: deletedUserNickname}
		} else {
			brief = s.toresp.ToUserBriefResp(&user)
		}

		_, isFollowing := followingSet[relID] // viewer 是否已关注对端
		_, isFollowed := followedSet[relID]   // 对端是否已关注 viewer
		items = append(items, modelsFollow.FollowUserItemResp{
			User:        brief,
			FollowedAt:  rows[i].CreatedAt,
			IsFollowing: isFollowing,
			IsFollowed:  isFollowed,
		})
	}
	return items, nil
}

// normalizePage 分页参数防御，与项目其它模块口径一致。
func normalizePage(page, pageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 50 {
		pageSize = 16
	}
	return page, pageSize
}

// toSet ID 切片转集合，供 O(1) 判断。
func toSet(ids []uint) map[uint]struct{} {
	set := make(map[uint]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
}
