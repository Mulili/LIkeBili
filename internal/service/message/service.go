package message

import (
	modelsMessage "LikeBili/internal/models/message"
	modelsUser "LikeBili/internal/models/user"
	repomessage "LikeBili/internal/repository/message"
	"LikeBili/pkg/toresp"
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

type Service struct {
	repo   *repomessage.Repository
	rdb    *redis.Client
	toresp *toresp.UserBriefRespBuilder
}

func NewService(repo *repomessage.Repository, rdb *redis.Client, toresp *toresp.UserBriefRespBuilder) *Service {
	return &Service{repo: repo, rdb: rdb, toresp: toresp}
}

// GetAllNotifications 分页拉取用户全部通知，并附带未读数。
// 分页参数钳制：page<1 归 1；pageSize 越界归默认 16（上限 64）。
// 未读数策略：以 DB 统计为准，Redis 缓存（unread:{userID}）更大时取缓存值
// （写路径 INCR 领先于 DB，缓存可能暂超 DB）。
func (s *Service) GetAllNotifications(c context.Context, page, pageSize int, userID uint) (*modelsMessage.NotificationListResp, error) {
	if page < 1 {
		page = 1
	}
	if pageSize > 64 || pageSize < 1 {
		pageSize = 16
	}
	list, total, err := s.repo.FindUserAllMsg(c, page, pageSize, userID)
	if err != nil {
		return nil, fmt.Errorf("Method:message.service.GetAllNotification: %w", err)
	}
	unreadCount, err := s.repo.UnreadCount(c, userID)
	if err != nil {
		return nil, fmt.Errorf("Method:message.service.GetAllNotification: %w", err)
	}
	//判断redis中未读数缓存是否比数据库中大，谁大拿谁
	if s.rdb != nil {
		rdbKey := fmt.Sprintf("unread:%d", userID)
		if count, err := s.rdb.Get(c, rdbKey).Int64(); err == nil && count > unreadCount {
			unreadCount = count
		}
	}

	listResp := make([]modelsMessage.MessageResp, len(list))
	for i, msg := range list {
		// FromUser 三分支：系统通知 / 正常用户 / 已注销（先判 type=4，再看 Preload 命中，default 兜底已注销）
		var fromUser *modelsUser.UserBrief
		switch {
		case msg.Type == modelsMessage.MsgTypeSystem:
			// ① 系统通知：type=4 → 系统虚拟发送者（FromUserID 约定为 0）
			fromUser = &modelsUser.UserBrief{ID: 0, Nickname: modelsMessage.SystemNickname}
		case msg.FromUser.ID != 0:
			// ② 正常用户：Preload 命中，走 toresp 转换（头像 objKey → 完整 URL）
			fromUser = s.toresp.ToUserBriefResp(&msg.FromUser)
		default:
			// ③ 发送者已注销：FromUserID 为正但 Preload 未命中（软删行被 gorm 自动过滤）
			fromUser = &modelsUser.UserBrief{ID: 0, Nickname: modelsMessage.DeletedUserText}
		}
		listResp[i] = modelsMessage.MessageResp{
			ID: msg.ID, Type: msg.Type,
			Content: msg.Content, TargetID: msg.TargetID,
			IsRead: msg.IsRead, CreatedAt: msg.CreatedAt,
			FromUser: fromUser,
		}
	}
	return &modelsMessage.NotificationListResp{
		Items:    listResp,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
		Unread:   unreadCount,
	}, nil
}

// UpdateAllIsRead 一键全部已读：DB 全部置已读后，将 Redis 未读缓存清零。
// rdb 尽力同步：Redis 故障时仅 DB 生效，后续读取缓存未命中会回源 DB，语义自愈。
func (s *Service) UpdateAllIsRead(c context.Context, userID uint) error {
	if err := s.repo.UpdateAllIsRead(c, userID); err != nil {
		return fmt.Errorf("Method:message.service.UpdateAllIsRead: %w", err)
	}
	if s.rdb != nil {
		rdbKey := fmt.Sprintf("unread:%d", userID)
		s.rdb.Set(c, rdbKey, 0, 0) // 缓存归零；忽略错误（尽力而为）
	}
	return nil
}

// UpdateTheIsRead 单条置为已读（幂等）：仅当该条确实由 未读→已读 转变（repository 返回 true）时，
// 才对 Redis 未读缓存递减，重复点击不产生多余写。
func (s *Service) UpdateTheIsRead(c context.Context, userID, msgID uint) error {
	ok, err := s.repo.UpdateTheIsRead(c, userID, msgID)
	if err != nil {
		return fmt.Errorf("Method:message.service.UpdateTheIsRead: %w", err)
	}
	if ok && s.rdb != nil {
		rdbKey := fmt.Sprintf("unread:%d", userID)
		s.rdb.Decr(c, rdbKey) // 未读 -1；忽略错误（尽力而为）
	}
	return nil
}

// SendNotification 写入一条通知（实现 like/comment/follow 模块依赖的 Notifier 接口）。
// 落库成功后对接收者未读缓存 INCR；Redis 故障时跳过（未读数以 DB 回源为准）。
func (s *Service) SendNotification(c context.Context, userID, fromUserID uint, msgType uint8, targetID uint, content string) error {
	msg := &modelsMessage.Message{
		UserID:     userID,     // 接收者（被通知的人）
		FromUserID: fromUserID, // 发送者（触发行为的人）；系统通知传 0
		Type:       msgType,    // 消息类型，见 MsgType* 常量
		TargetID:   targetID,   // 关联对象 ID（视频/评论/用户等）
		Content:    content,    // 展示文案
	}
	if err := s.repo.Create(c, msg); err != nil {
		return fmt.Errorf("Method:message.service.SendNotification: %w", err)
	}
	if s.rdb != nil {
		rdbKey := fmt.Sprintf("unread:%d", userID)
		s.rdb.Incr(c, rdbKey) // 未读 +1；忽略错误（尽力而为）
	}
	return nil
}
