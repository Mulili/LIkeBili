// Package meta 定义"视频元信息"的数据库模型（实体）。
// 一张 video_metas 表，记录视频的时长、分辨率、编码、码率等技术参数。
// 这些数据由转码 worker 用 ffprobe 提取后写入（对应 internal/transcode 包的 runFFProbe）。
//
// 用途：前端播放器拿到时长/分辨率可做展示和提示；服务端可据此判断视频规格。
// 与 video_qualities（清晰度档位表）的区别：本表记录"源视频本身"的参数，
// 一张视频只有一条记录；quality 表记录"转码出的每个清晰度档位"，一个视频多条。
package meta

import "time"

// VideoMeta 视频元信息实体，对应数据库表 video_metas。
// 关系：一个视频至多一条元信息（VideoID 唯一索引），转码时反复写入用"存在则更新"策略。
type VideoMeta struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`       // 记录主键，自增
	VideoID   uint      `gorm:"uniqueIndex;not null" json:"video_id"`     // 关联的视频 ID；uniqueIndex 保证"一个视频只有一条元信息"，与 quality 表的普通 index 形成对比
	Duration  float64   `gorm:"not null" json:"duration"`                 // 视频时长（秒），float64 因为 ffprobe 返回带小数（如 123.45）
	Width     uint      `gorm:"default:0" json:"width"`                   // 视频宽度（像素）；转码时用来判断能转哪些档位（不放大）
	Height    uint      `gorm:"default:0" json:"height"`                  // 视频高度（像素）
	Codec     string    `gorm:"type:varchar(50);default:''" json:"codec"` // 编码格式（如 h264、hevc）
	Bitrate   uint      `gorm:"default:0" json:"bitrate"`                 // 码率（kbps，注意不是 bps——runFFProbe 里除以了 1000）
	CreatedAt time.Time `gorm:"not null" json:"created_at"`               // 创建时间，GORM 写入时自动填充
}

// TableName 指定表名。
// 不写的话 GORM 会按"结构体名 → snake_case 复数"推断出 video_metas，
// 显式声明更稳：结构体重命名不会悄悄改掉表名。
func (VideoMeta) TableName() string {
	return "video_metas"
}
