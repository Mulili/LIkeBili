package message

import (
	modelsUser "LikeBili/internal/models/user"
	"time"
)

// 消息响应体，含发送者的简要信息
type MessageResp struct {
	ID        uint                  `json:"id"`                  // 消息主键
	Type      uint8                 `json:"type"`                // 消息类型：1点赞 2评论 3关注 4系统通知
	Content   string                `json:"content"`             // 消息内容
	TargetID  uint                  `json:"target_id"`           // 关联目标 ID（视频 / 评论 / 用户）
	IsRead    uint8                 `json:"is_read"`             // 已读状态：1未读 2已读
	CreatedAt time.Time             `json:"created_at"`          // 创建时间
	FromUser  *modelsUser.UserBrief `json:"from_user,omitempty"` // 发送者简要信息
}

// 通知列表响应体（含未读数，供红点角标展示）
type NotificationListResp struct {
	Items    []MessageResp `json:"items"`     // 通知列表
	Total    int64         `json:"total"`     // 通知总数
	Page     int           `json:"page"`      // 当前页码
	PageSize int           `json:"page_size"` // 每页数量
	Unread   int64         `json:"unread"`    // 未读通知数
}
