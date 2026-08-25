package message

import (
	modelsMessage "LikeBili/internal/models/message"
	"context"
	"fmt"

	"gorm.io/gorm"
)

type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// Create 写入一条通知消息（由 like/comment/follow 等模块经 Notifier 接口调用）。
func (r *Repository) Create(c context.Context, msg *modelsMessage.Message) error {
	if err := r.db.WithContext(c).Create(msg).Error; err != nil {
		return fmt.Errorf("Method:message.repository.Create: %w", err)
	}
	return nil
}

// FindUserAllMsg 分页查询某用户（接收者）的全部消息，按创建时间倒序，并预加载发送者信息。
// 返回：当前页列表、消息总数（total 供分页器使用）。
func (r *Repository) FindUserAllMsg(c context.Context, page, pageSize int, userID uint) ([]modelsMessage.Message, int64, error) {
	var total int64
	// ① 先统计总数（分页需要），与列表查询复用同一条件构建
	query := r.db.WithContext(c).Model(&modelsMessage.Message{}).Where("user_id = ?", userID)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("Method:message.repository.FindUserAllMsg: %w", err)
	}

	offset := (page - 1) * pageSize

	// ② 再查当前页：倒序 + 分页 + 预加载发送者（FromUser）
	var list []modelsMessage.Message
	if err := query.Preload("FromUser").Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&list).Error; err != nil {
		return nil, 0, fmt.Errorf("Method:message.repository.FindUserAllMsg: %w", err)
	}

	return list, total, nil
}

// UpdateAllIsRead 一键已读：将该用户所有未读消息置为已读（幂等，已读的不在条件内，不会重复更新）。
func (r *Repository) UpdateAllIsRead(c context.Context, userID uint) error {
	err := r.db.WithContext(c).Model(&modelsMessage.Message{}).
		Where("user_id = ? AND is_read = ?", userID, modelsMessage.ReadStatusUnread).
		Update("is_read", modelsMessage.ReadStatusRead).Error
	if err != nil {
		return fmt.Errorf("Method:message.repository.UpdateAllIsRead: %w", err)
	}
	return nil
}

// UpdateTheIsRead 将单条消息置为已读（用于用户点击某条消息后更新状态）。
// 带 is_read = 未读 条件，重复点击不产生额外写操作（幂等）。
func (r *Repository) UpdateTheIsRead(c context.Context, userID, msgID uint) (bool, error) {
	result := r.db.WithContext(c).Model(&modelsMessage.Message{}).
		Where("user_id = ? AND id = ? AND is_read = ?", userID, msgID, modelsMessage.ReadStatusUnread).
		Update("is_read", modelsMessage.ReadStatusRead)
	if result.Error != nil {
		return false, fmt.Errorf("Method:message.repository.UpdateTheIsRead: %w", result.Error)
	}
	return result.RowsAffected > 0, nil
}

// UnreadCount 统计用户未读消息数（供列表响应的 Unread 字段 / 前端红点角标）。
func (r *Repository) UnreadCount(c context.Context, userID uint) (int64, error) {
	var total int64
	query := r.db.WithContext(c).Model(&modelsMessage.Message{}).Where("user_id = ? AND is_read = ?", userID, modelsMessage.ReadStatusUnread)
	if err := query.Count(&total).Error; err != nil {
		return 0, fmt.Errorf("Method:message.repository.UnreadCount: %w", err)
	}
	return total, nil
}
