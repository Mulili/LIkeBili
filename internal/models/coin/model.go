package coin

import "time"

const (
	SendCoins    = 2 //发币常量
	MaxDropCoins = 2 //投币最大值
)

// Coin 投币记录流水表（coins）：记录"哪个用户给哪个视频投了几个币"。
// 与钱包 UserCoin 的关系：投币即扣减钱包余额，本表只记流水、不存余额。
// 联合唯一索引 (user_id, video_id)：同一用户对同一视频只有一条记录，
// 数据库层杜绝重复投币；Count 支持 1~2，由 service 层校验（可一次投 2，或累计补投到 2）。
type Coin struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID    uint      `gorm:"uniqueIndex:uk_user_video;not null" json:"user_id"`  // 持有人（联合唯一：一人一视频一条记录）
	VideoID   uint      `gorm:"uniqueIndex:uk_user_video;not null" json:"video_id"` // 投币视频
	Count     uint8     `gorm:"type:tinyint;not null" json:"count"`                 // 该用户对该视频累计投的币（1 或 2）
	CreatedAt time.Time `gorm:"not null" json:"created_at"`
}

// UserCoin 用户币钱包表（user_coins）：与 User 1:1 关联，一人一条钱包记录。
// 持有逻辑集中在钱包表，users 表零改动。
// 不活跃的日子不补发、不累计；LastCoinGrant 即"最后一次签到发币日"。
type UserCoin struct {
	ID            uint       `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID        uint       `gorm:"uniqueIndex;not null" json:"user_id"` // 关联用户（唯一：一人一条钱包记录）
	Balance       uint       `gorm:"default:0" json:"balance"`            // 当前持有币数（投币时原子扣减）
	LastCoinGrant *time.Time `json:"last_coin_grant"`                     // 最后签到发币日：null=从未签到（新用户首次访问发当天 2 币），非 null=上次发币日，当天不重复发
	CreatedAt     time.Time  `gorm:"not null" json:"created_at"`
	UpdatedAt     time.Time  `gorm:"not null" json:"updated_at"`
}

func (Coin) TableName() string {
	return "coins"
}

func (UserCoin) TableName() string {
	return "user_coins"
}
