package video

import (
	usermodel "LikeBili/internal/models/user"
	"time"

	"gorm.io/gorm"
)

type Video struct {
	ID          uint           `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID      uint           `gorm:"index;not null" json:"user_id"`
	Title       string         `gorm:"type:varchar(64);not null" json:"title"`
	Description string         `gorm:"type:text" json:"description"`
	CoverURL    string         `gorm:"type:varchar(512);default:''" json:"cover_url"`
	VideoURL    string         `gorm:"type:varchar(512);not null" json:"video_url"`
	Duration    uint           `gorm:"default:0" json:"duration"`
	FileSize    uint64         `gorm:"default:0" json:"file_size"`
	CategoryID  uint32         `gorm:"index;default:0" json:"category_id"`   //类型id，建立索引，否则如果以后查找某一个类型就得全表查询
	Status      uint8          `gorm:"type:tinyint;default:1" json:"status"` //视频状态：1待审核2审核成功3审核失败4隐藏
	Views       uint32         `gorm:"default:0" json:"views"`
	CreatedAt   time.Time      `gorm:"not null" json:"created_at"`
	UpdatedAt   time.Time      `gorm:"not null" json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"` //软删除

	//首先由Preload查询user和category两表，随后由gorm动态填入video中，让video可以显示发布者头像名称，以及分类名称
	User     usermodel.User `gorm:"foreignKey:UserID" json:"-"`     //关联发布者
	Category Category       `gorm:"foreignKey:CategoryID" json:"-"` //关联分类
}

type Category struct {
	ID   uint   `gorm:"primaryKey" json:"id"`         //分类id
	Name string `gorm:"type:varchar(64)" json:"name"` //分类名称
	Slug string `gorm:"type:varchar(64)" json:"slug"` //分类英文标识
}

func (Video) TableName() string {
	return "videos"
}

func (Category) TableName() string {
	return "categories"
}
