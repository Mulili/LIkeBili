// Package quality 定义"视频清晰度档位"的数据库模型（实体）。
// 一张 video_qualities 表，记录视频转码后产出的每个清晰度档位（360p/480p/720p/1080p）
// 以及该档位 m3u8 在 MinIO 的存储路径和总大小。
//
// 写入时机：转码 worker 每完成一个档位就写一条（对应 internal/transcode 包的 saveQuality）。
// 用途：前端做"清晰度切换"菜单时，查这张表就知道视频有哪些档位、每档的 m3u8 在哪。
// 与 video_metas 的区别：本表一个视频对应多条（每档一条），所以 VideoID 用普通 index 而非唯一索引。
package quality

import "time"

// VideoQuality 清晰度档位实体，对应数据库表 video_qualities。
// 关系：一个视频多条记录（每个转码档位一条），靠 VideoID + Quality 组合定位。
type VideoQuality struct {
	ID         uint      `gorm:"primaryKey;autoIncrement" json:"id"`            // 记录主键，自增
	VideoID    uint      `gorm:"index;not null" json:"video_id"`                // 关联的视频 ID；普通 index（允许重复）因为一个视频有多条档位记录
	Quality    string    `gorm:"type:varchar(10);not null" json:"quality"`      // 清晰度标识（360p / 480p / 720p / 1080p）
	ObjectName string    `gorm:"type:varchar(500);not null" json:"object_name"` // 该档位 m3u8 在 MinIO 的对象名，如 videos/5/720p/index.m3u8
	FileSize   uint64    `gorm:"default:0" json:"file_size"`                    // 该档位所有 ts 分片的总大小（字节），转码 worker 累加得出
	CreatedAt  time.Time `gorm:"not null" json:"created_at"`                    // 创建时间，GORM 写入时自动填充
}

// TableName 指定表名。
func (VideoQuality) TableName() string {
	return "video_qualities"
}
