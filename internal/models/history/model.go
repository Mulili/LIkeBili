package history

import (
	modelsVideo "LikeBili/internal/models/video"
	"time"
)

type UserHistory struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID    uint      `gorm:"uniqueIndex:uk_user_video;index:idx_user_watched,priority:1;not null" json:"user_id"` // 用户 ID
	VideoID   uint      `gorm:"uniqueIndex:uk_user_video;not null" json:"video_id"`                                  // 视频 ID
	Progress  uint      `gorm:"default:0" json:"progress"`                                                           // 观看进度（秒）
	WatchedAt time.Time `gorm:"index:idx_user_watched,priority:2;not null" json:"watched_at"`                        // 最近观看时间

	Video modelsVideo.Video `gorm:"foreignKey:VideoID" json:"-"`
}

func (UserHistory) TableName() string {
	return "user_user_history"
}
