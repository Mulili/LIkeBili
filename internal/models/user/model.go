package user

import (
	"time"

	"gorm.io/gorm"
)

type User struct {
	ID           uint           `gorm:"primaryKey;autoIncrement" json:"id"`
	Username     string         `gorm:"type:varchar(32);uniqueIndex;not null" json:"username"`
	Nickname     string         `gorm:"type:varchar(32);not null" json:"nickname"`
	Email        string         `gorm:"type:varchar(100);uniqueIndex;not null" json:"email"`
	PasswordHash string         `gorm:"type:varchar(100);not null" json:"-"`
	Avatar       string         `gorm:"type:varchar(500);default:''" json:"avatar"`
	Bio          string         `gorm:"type:varchar(500);default:''" json:"bio"`
	Role         uint8          `gorm:"type:tinyint unsigned;default: 1" json:"role"` //权限：1普通用户，2管理员
	Status       int8           `gorm:"type:tinyint;default: 1" json:"status"`        //未注册用户为0，注册用户为1，正式用户为2，封禁用户为-1
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"-"`
}

func (User) TableName() string {
	return "users"
}

// 角色枚举：与 Role 字段取值对应。
// 注意：Role 是 tinyint unsigned，取值必须在 0~255 内。
const (
	RoleUser  uint8 = 1 // 普通用户：公开注册的默认角色
	RoleAdmin uint8 = 2 // 审核管理员：审核视频（通过/驳回），由超管创建
	RoleSuper uint8 = 3 // 超管：启动 seed 创建，仅负责创建审核管理员，不参与审核
)
