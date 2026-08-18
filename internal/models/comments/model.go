package comments

import (
	modelsUser "LikeBili/internal/models/user"
	"time"

	"gorm.io/gorm"
)

// VideoComments 视频评论模型。
// 对应数据库 video_comments 表，支持无限嵌套的评论结构（评论的评论仍可被评论）。
// 采用"邻接表 + 冗余根 ID"方案：
//   - ParentID 指向直接父级：根评论为 0，子评论为父评论的 ID（父评论可能是根评论，也可能是子评论）
//   - RootID   指向整棵树的根评论：一次 WHERE root_id=? 即可拉出整棵树的所有节点，
//     再在内存中按 ParentID 组装成树，避免递归逐层查询的 N+1 问题
//
// 索引设计：
//   - idx_video_parent (video_id, parent_id)：根评论分页查询（WHERE video_id=? AND parent_id=0）
//   - root_id 独立索引：拉取某条根评论下的整棵树
//   - user_id 独立索引：我的评论列表
type VideoComments struct {
	ID       uint   `gorm:"primaryKey;autoIncrement" json:"id"`                           // 评论唯一标识
	VideoID  uint   `gorm:"index:idx_video_parent,priority:1;not null" json:"video_id"`   // 所属视频 ID（联合索引第一列）
	ParentID uint   `gorm:"index:idx_video_parent,priority:2;default:0" json:"parent_id"` // 直接父级评论 ID：0=根评论，非 0=父评论 ID（联合索引第二列）
	UserID   uint   `gorm:"index;not null" json:"user_id"`                                // 评论者用户 ID
	RootID   uint   `gorm:"index;default:0" json:"root_id"`                               // 根评论 ID：整棵树的根，用于一次拉取整棵树（根评论自身为 0）
	Content  string `gorm:"type:text;not null" json:"content"`                            // 评论内容
	Likes    uint   `gorm:"default:0" json:"likes"`                                       // 点赞总数（冗余计数，由 comment_likes 表的增删维护）

	CreatedAt time.Time       `gorm:"not null" json:"created_at"` // 评论创建时间
	DeletedAt gorm.DeletedAt  `gorm:"not null" json:"deleted_at"` // 软删除时间：删除评论时写入，列表查询自动过滤
	User      modelsUser.User `gorm:"foreignKey:UserID" json:"-"` // 关联评论者（Preload 填充头像/昵称）
}

func (VideoComments) TableName() string {
	return "video_comments"
}

// CommentLikes 评论点赞记录模型。
// 对应数据库 comment_likes 表，记录"哪个用户点赞了哪条评论"的点赞关系。
// 与 VideoLikes 同款模式：联合唯一索引 (user_id, comment_id) 由数据库层防重复点赞。
// 作用：支撑"当前用户是否已赞过某条评论"的判断、取消点赞，
// 以及精确维护 VideoComments.Likes 计数的增减（先写本表，再同步冗余计数）。
type CommentLikes struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`                     // 点赞记录唯一标识
	UserID    uint      `gorm:"uniqueIndex:uk_user_comment;not null" json:"user_id"`    // 点赞用户 ID（联合唯一索引的一部分）
	CommentID uint      `gorm:"uniqueIndex:uk_user_comment;not null" json:"comment_id"` // 被点赞的评论 ID（联合唯一索引的一部分）
	CreatedAt time.Time `gorm:"not null" json:"created_at"`                             // 点赞时间
}

func (CommentLikes) TableName() string {
	return "comment_likes"
}
