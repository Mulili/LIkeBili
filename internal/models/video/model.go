package video

import (
	"time"

	"gorm.io/gorm"
)

type Video struct {
	ID          uint           `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID      uint           `gorm:"index;not null" json:"user_id"`
	Title       string         `gorm:"type:varchar(64);not null" json:"title"`
	Description string         `gorm:"type:text" json:"description"`
	CoverURL    string         `gorm:"type:varchar(512);default:''" json:"cover_url"`
	VideoURL    string         `gorm:"type:varchar(512);not null" json:"vidio_url"`
	Duration    uint           `gorm:"default:0" json:"duration"`
	FileSize    uint64         `gorm:"default:0" json:"file_size"`
	CategoryID  uint32         `gorm:"index;default:0" json:"category_id"`   //类型id，建立索引，否则如果以后查找某一个类型就得全表查询
	Status      uint8          `gorm:"type:tinyint;default:1" json:"status"` //视频状态：1待审核2审核成功3审核失败4隐藏
	Views       uint32         `gorm:"default:0" json:"views"`
	CreatedAt   time.Time      `gorm:"not null" json:"created_at"`
	UpdatedAt   time.Time      `gorm:"not null" json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"` //软删除

}
