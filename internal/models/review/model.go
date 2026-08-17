// Package review 定义"视频审核记录"的数据库模型（实体）。
// 一张 video_reviews 表，记录每次管理员审核的结果与意见。
// 与视频的关系：一个视频可有多条审核记录（未来支持"驳回后重新提交再审核"），
// 作者端展示"审核失败原因"时取该视频最新一条记录。
package review

import "time"

// VideoReview 视频审核记录实体，对应数据库表 video_reviews。
// 与 Video.Status 的关系：Result 取值与视频 Status 对齐（2=通过 3=失败），
// 但 Video.Status 是"视频当前状态"，本表是"每次审核的流水"，两者职责不同。
type VideoReview struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`                  // 记录主键，自增
	VideoID   uint      `gorm:"index;not null" json:"video_id"`                      // 被审核的视频 ID（普通 index，一视频可多条记录）
	AdminID   uint      `gorm:"index;not null" json:"admin_id"`                      // 审核管理员 ID（对应 users.id）
	Result    uint8     `gorm:"type:tinyint;not null" json:"result"`                 // 审核结果：2=通过 3=失败（与视频 Status 对齐）
	Reason    string    `gorm:"type:varchar(200);default:''" json:"reason"`          // 审核意见：失败时必填（驳回原因），通过时可空
	CreatedAt time.Time `gorm:"not null" json:"created_at"`                          // 审核时间
}

// TableName 指定表名。
func (VideoReview) TableName() string {
	return "video_reviews"
}

// 审核结果枚举：与视频 Status 取值对齐，便于同一套常量流转。
const (
	ResultApprove uint8 = 2 // 审核通过
	ResultReject  uint8 = 3 // 审核失败
)
