package follow

import (
	modelsUser "LikeBili/internal/models/user"
	"time"
)

// Follow 关注关系表（user_follows）。
// 语义：一行 = 一条单向关注，FollowerID（主动关注方/关注者）→ FolloweeID（被关注方/创作者）。
//   - 关注列表（我关注了谁）：WHERE follower_id = ? → 查 FollowerID 是我的行
//   - 粉丝列表（谁关注了我）：WHERE followee_id = ? → 反查同表
//   - 是否已关注/互关：同表两条方向相反的记录，无需单独维护
//
// 索引设计（对齐三个高频查询）：
//   - uk_follower_followee(follower_id, followee_id)：联合唯一，数据库层防重复关注，同时精确支撑"是否已关注"查询
//   - idx_follower_created(follower_id, created_at)：支撑"我的关注列表"按关注时间倒序
//   - idx_followee_created(followee_id, created_at)：支撑"我的粉丝列表"按关注时间倒序
//
// 注销占位：用户注销走 users 软删除（DeletedAt 置位），本表记录保留不删。
// 关联（Preload/Join）查不到已注销用户时，由 service 层把该用户替换为官方统一"已注销"占位展示。
type Follow struct {
	ID         uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	FollowerID uint      `gorm:"not null;uniqueIndex:uk_follower_followee,priority:1;index:idx_follower_created,priority:1" json:"follower_id"` // 关注者 ID（主动发起关注的一方）
	FolloweeID uint      `gorm:"not null;uniqueIndex:uk_follower_followee,priority:2;index:idx_followee_created,priority:1" json:"followee_id"` // 被关注者 ID（被关注的创作者）
	CreatedAt  time.Time `gorm:"not null;index:idx_follower_created,priority:2;index:idx_followee_created,priority:2" json:"created_at"`        // 关注时间（列表默认按此倒序，取消后重关会生成新时间）

	// 双向关联用户：列表渲染关注者/创作者信息用（json 排除，对外输出走 DTO 转换）
	Follower modelsUser.User `gorm:"foreignKey:FollowerID" json:"-"`
	Followee modelsUser.User `gorm:"foreignKey:FolloweeID" json:"-"`
}

func (Follow) TableName() string {
	return "user_follows"
}
