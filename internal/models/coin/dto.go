package coin

// CoinReq 投币请求体：目标视频 + 本次投币数（1 或 2）。
// Count 经 validator 校验 required + oneof=1 2，非法值在 handler 层直接拒绝。
type CoinReq struct {
	VideoID uint  `json:"video_id" validate:"required"`        // 目标视频 ID
	Count   uint8 `json:"count" validate:"required,oneof=1 2"` // 本次投币数：最多两个币
}

// CoinResp 投币成功响应：返回该用户对该视频的累计投币数 + 投币后的剩余余额。
// 前端据此展示"已投 X/2"与"我的余额还剩多少"。
type CoinResp struct {
	VideoID   uint  `json:"video_id"`   // 目标视频 ID
	CoinCount uint8 `json:"coin_count"` // 该用户对该视频累计投的币（1 或 2）
	Balance   uint  `json:"balance"`    // 投币扣减后的剩余余额
}

// CoinBalanceResp 用户币余额响应：签到发币（懒结算）补发后返回最新余额。
type CoinBalanceResp struct {
	Balance uint `json:"balance"` // 当前持有币数
}

// FindUserDropResp 用户对某视频的投币状态响应：前端据此控制投币按钮。
type FindUserDropResp struct {
	CanDrop     bool  `json:"can_drop"`     // true=还可投币（按钮可用）；false=已投满 2 个（按钮置灰）
	RemainCount uint8 `json:"remain_count"` // 剩余可投币数（0~2）
}
