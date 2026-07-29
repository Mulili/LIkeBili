package favorites

import "time"

// Favorites 收藏夹数据模型。
// 对应数据库 favorites 表，存储用户的收藏夹（类似歌单/专辑）。
type Favorites struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`      // 收藏夹唯一标识
	UserID    uint      `gorm:"index;not null" json:"user_id"`           // 所属用户 ID，用于关联用户和其创建的收藏夹
	Name      string    `gorm:"type:varchar(64);not null" json:"name"`   // 收藏夹名称，最长 64 个字符
	IsPublic  uint8     `gorm:"type:tinyint;default:1" json:"is_public"` // 是否公开：1=公开，0=私密，用于控制他人是否可见
	CreatedAt time.Time `gorm:"not null" json:"created_at"`              // 收藏夹创建时间
}

func (Favorites) TableName() string {
	return "favorites"
}

// FavoritesItem 收藏夹-视频关联记录。
// 对应数据库 favorite_items 表，记录某个收藏夹中收藏了哪些视频。
// 使用联合唯一索引 (favorites_id, video_id)，防止同一个视频被重复收藏到同一收藏夹。
// 例如：favorites_id=1 的收藏夹不能有两条 video_id=100 的记录。
// 如果只用代码 if exists 判断，存在并发问题（两次请求同时通过检查），
// 用数据库层的唯一索引来保证是最可靠的方案。
type FavoritesItem struct {
	ID          uint      `gorm:"primaryKey;autoIncrement" json:"id"`                    // 关联记录唯一标识
	FavoritesID uint      `gorm:"uniqueIndex:uk_fav_video;not null" json:"favorites_id"` // 所属收藏夹 ID（联合唯一索引的一部分）
	VideoID     uint      `gorm:"uniqueIndex:uk_fav_video;not null" json:"video_id"`     // 被收藏的视频 ID（联合唯一索引的一部分）
	CreatedAt   time.Time `gorm:"not null" json:"created_at"`                            // 收藏时间
}

func (FavoritesItem) TableName() string {
	return "favorite_items"
}
