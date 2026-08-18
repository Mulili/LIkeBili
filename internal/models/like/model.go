package like

import "time"

// VideoLikes 视频点赞记录模型。
// 对应数据库 video_likes 表，记录"哪个用户点赞了哪个视频"的点赞关系。
// 使用联合唯一索引 (user_id, video_id)，保证同一用户对同一视频只能点赞一次，
// 由数据库层直接防重复点赞（比代码 if exists 判断更可靠，无并发竞态）。
// 例如：用户 1 不能有两条 video_id=100 的点赞记录。
// 注意：本表只存"谁点了赞"的关系，点赞总数由调用方 COUNT 统计（不冗余在 videos 表）。
type VideoLikes struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`                 // 点赞记录唯一标识
	UserID    uint      `gorm:"uniqueIndex:uk_user_video;not null" json:"user_id"`  // 点赞用户 ID（联合唯一索引的一部分）
	VideoID   uint      `gorm:"uniqueIndex:uk_user_video;not null" json:"video_id"` // 被点赞的视频 ID（联合唯一索引的一部分）
	CreatedAt time.Time `gorm:"not null" json:"created_at"`                         // 点赞时间
}

func (VideoLikes) TableName() string {
	return "video_likes"
}
