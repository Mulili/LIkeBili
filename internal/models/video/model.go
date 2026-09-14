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
	CategoryID  uint32         `gorm:"index;default:0" json:"category_id"`       //类型id，建立索引，否则如果以后查找某一个类型就得全表查询
	Status      uint8          `gorm:"type:tinyint;default:1" json:"status"`     //视频状态：1待审核2审核成功3审核失败
	ViewStatus  *uint8         `gorm:"type:tinyint;defalt:1" json:"view_status"` //1公开，2私密,可视状态，用于用户自己决定视频仅自己可见还是公开
	Views       uint32         `gorm:"default:0" json:"views"`
	CreatedAt   time.Time      `gorm:"not null" json:"created_at"`
	UpdatedAt   time.Time      `gorm:"not null" json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"` //软删除

	//首先由Preload查询user和category两表，随后由gorm动态填入video中，让video可以显示发布者头像名称，以及分类名称
	User     usermodel.User `gorm:"foreignKey:UserID" json:"-"`     //关联发布者
	Category Category       `gorm:"foreignKey:CategoryID" json:"-"` //关联分类
}

type Category struct {
	ID   uint   `gorm:"primaryKey" json:"id"`                     //分类id
	Name string `gorm:"type:varchar(64)" json:"name"`             //分类名称
	Slug string `gorm:"type:varchar(64);uniqueIndex" json:"slug"` //分类英文标识（唯一：seed 幂等的冲突判定依据）
}

// DefaultCategories 内置分类字典（启动时幂等写入）。
// ID 固定写死：前端上传/筛选按 ID 传参，固定 ID 可避免自增顺序变化导致"分类错位"；
// slug 唯一索引用于 seed 幂等（已存在则忽略，不覆盖运维侧的名称调整）。
var DefaultCategories = []Category{
	{ID: 1, Name: "动画", Slug: "anime"},
	{ID: 2, Name: "番剧", Slug: "bangumi"},
	{ID: 3, Name: "游戏", Slug: "game"},
	{ID: 4, Name: "音乐", Slug: "music"},
	{ID: 5, Name: "舞蹈", Slug: "dance"},
	{ID: 6, Name: "科技", Slug: "tech"},
	{ID: 7, Name: "生活", Slug: "life"},
	{ID: 8, Name: "鬼畜", Slug: "kichiku"},
	{ID: 9, Name: "影视", Slug: "movie"},
	{ID: 10, Name: "娱乐", Slug: "entertainment"},
	{ID: 11, Name: "知识", Slug: "knowledge"},
}

func (Video) TableName() string {
	return "videos"
}

func (Category) TableName() string {
	return "categories"
}
