package search

import "time"

// VideoSearch 视频检索表（video_search），与 videos 一对一（video_id 为主键）。
//
// 为什么独立成表而不是给 videos 加冗余列：
//   - 全文索引需要把"标题/简介/UP主昵称/分类名"放在同一张表（跨表无法共用 MATCH）
//   - 检索字段（昵称、分类名）属于冗余数据，独立成表可让主表保持干净
//
// 为什么冗余 status/view_status/created_at：
//
//	搜索必须只返回"审核通过 + 公开"的视频，若靠回表过滤，total（命中数）会与
//	实际返回条数不一致、分页错乱；冗余后过滤与排序都在检索表内完成。
//
// 注意：全文索引**不通过 GORM 标签声明**，而由 repository.EnsureFulltextIndex 用
// 原生 DDL 幂等创建——GORM 的索引标签无法保证带 `WITH PARSER ngram`，
// 若生成一个不带分词器的同名索引，中文检索会静默失效。
type VideoSearch struct {
	VideoID        uint      `gorm:"primaryKey" json:"video_id"`                                  // 对应 videos.id（一对一）
	UserID         uint      `gorm:"index:idx_vs_user;not null" json:"user_id"`                   // 冗余：改昵称时按它批量同步
	CategoryID     uint      `gorm:"index:idx_vs_category;not null;default:0" json:"category_id"` // 冗余：分类筛选 + 分类改名同步
	Title          string    `gorm:"type:varchar(64);not null;default:''" json:"title"`           // 视频标题（检索字段）
	Description    string    `gorm:"type:text" json:"description"`                                // 视频简介（检索字段）
	AuthorNickname string    `gorm:"type:varchar(32);not null;default:''" json:"author_nickname"` // UP 主展示名（检索字段）
	CategoryName   string    `gorm:"type:varchar(64);not null;default:''" json:"category_name"`   // 分类名（检索字段）
	Status         uint8     `gorm:"type:tinyint;not null;default:1" json:"status"`               // 冗余：审核状态（1待审核 2通过 3驳回）
	ViewStatus     uint8     `gorm:"type:tinyint;not null;default:1" json:"view_status"`          // 冗余：1公开 2私密
	CreatedAt      time.Time `gorm:"not null" json:"created_at"`                                  // 冗余：最新排序用（取视频创建时间）
	UpdatedAt      time.Time `json:"updated_at"`                                                  // 本次同步时间
}

func (VideoSearch) TableName() string {
	return "video_search"
}
