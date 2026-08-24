package message

import (
	modelsUser "LikeBili/internal/models/user"
	"time"
)

// 消息类型枚举（Type 字段取值）：关注 / 点赞 / 评论 / 系统通知
const (
	MsgTypeLike    uint8 = 1 // 点赞
	MsgTypeComment uint8 = 2 // 评论
	MsgTypeFollow  uint8 = 3 // 关注
	MsgTypeSystem  uint8 = 4 // 系统通知
)

// 已读状态枚举（IsRead 字段取值）
const (
	ReadStatusUnread uint8 = 1 // 未读
	ReadStatusRead   uint8 = 2 // 已读
)

// Message 站内通知：关注 / 点赞 / 评论等行为发生时，向被操作者（接收者）写入一条消息。
// 由行为触发方（like/comment/follow 等模块）通过 Notifier 接口写入。
type Message struct {
	ID         uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID     uint      `gorm:"index;not null" json:"user_id"`         // 接收者用户 ID（被通知的人）
	FromUserID uint      `gorm:"not null" json:"from_user_id"`          // 发送者用户 ID（触发行为的人）
	Type       uint8     `gorm:"type:tinyint;not null" json:"type"`     // 消息类型，见 MsgType* 常量
	TargetID   uint      `gorm:"default:0" json:"target_id"`            // 关联对象 ID（视频 / 评论 / 用户等）
	Content    string    `gorm:"type:varchar(512)" json:"content"`      // 消息内容（如"xxx 点赞了你的视频"）
	IsRead     uint8     `gorm:"type:tinyint;default:1" json:"is_read"` // 已读状态，见 ReadStatus* 常量，默认未读
	CreatedAt  time.Time `gorm:"not null" json:"created_at"`

	FromUser modelsUser.User `gorm:"foreignKey:FromUserID" json:"-"` // 发送者信息（Preload 加载，仅内部使用）
}

func (Message) TableName() string {
	return "messages"
}
