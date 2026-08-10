// Package transcode 定义"转码任务"的数据库模型（实体）。
// 一张 transcode_tasks 表，记录每个视频的转码状态：排不排队、转没转完、失败原因。
// 职责边界：本包只描述"数据长什么样"，转码怎么执行在 internal/transcode 包。
package transcode

import "time"

// TranscodeTask 转码任务实体，对应数据库表 transcode_tasks。
// 关系：一个视频只允许一条转码任务（VideoID 唯一索引），所以按视频查转码状态很自然。
type TranscodeTask struct {
	ID         uint      `gorm:"primaryKey;autoIncrement" json:"id"`              // 任务主键，自增
	VideoID    uint      `gorm:"uniqueIndex;not null" json:"video_id"`            // 关联的视频 ID；uniqueIndex 保证"一个视频至多一条任务"，防止重复转码
	Status     uint8     `gorm:"type:tinyint;default:0" json:"status"`            // 任务状态，取值见下方 StatusXxx 常量（0待处理 1转码中 2完成 3失败）
	Progress   uint8     `gorm:"type:tinyint unsigned;default:0" json:"progress"` // 转码进度 0-100（unsigned=无符号，进度不可能为负）
	ErrMessage string    `gorm:"type:varchar(500);default:''" json:"err_message"` // 失败原因；成功时为空串
	CreatedAt  time.Time `gorm:"not null" json:"created_at"`                      // 创建时间，GORM 写入时自动填充
	UpdatedAt  time.Time `gorm:"not null" json:"updated_at"`                      // 更新时间，GORM 每次 Save 自动刷新
}

// TableName 指定表名。
// 即使不写，GORM 也会按"结构体名 → snake_case 复数"推断出 transcode_tasks，
// 但显式声明更稳：结构体重命名不会悄悄改掉表名。
func (TranscodeTask) TableName() string {
	return "transcode_tasks"
}

// 转码任务状态枚举。
// 注意：全部统一为 uint8，与上面 Status 字段的类型一致。
// 如果混用（比如 StatusDone 是 int），`task.Status = StatusDone` 会因
// "int 不能直接赋给 uint8"而编译报错——typed 常量不像字面量会自动转换。
const (
	StatusPending   uint8 = 0 // 待处理：任务已创建，还没开始转码
	StatusTranscode uint8 = 1 // 转码中：worker 正在跑 FFmpeg
	StatusDone      uint8 = 2 // 完成：转码成功，视频可播放
	StatusFailed    uint8 = 3 // 失败：转码出错，原因记在 ErrMessage
)
