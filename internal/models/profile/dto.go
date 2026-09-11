package profile

import (
	modelsUser "LikeBili/internal/models/user"
)

// ProfileResp 个人中心 / 他人主页的聚合响应。
//
// 可见性规则（由 service 按 viewer 与 target 是否同一人决定）：
//   - 公开统计（所有人可见）：关注数、粉丝数
//   - 半公开统计：收藏夹数、投稿数——本人统计全部，他人只统计公开部分
//   - 私有数据（仅本人）：硬币余额、观看历史条数；他人视角为 nil，JSON 中省略该字段
type ProfileResp struct {
	User           modelsUser.UserInfoResp `json:"user"`            // 用户公开资料（昵称/头像/签名/注册时间）
	FollowingCount int64                   `json:"following_count"` // 关注数
	FollowerCount  int64                   `json:"follower_count"`  // 粉丝数
	FavoriteCount  int64                   `json:"favorite_count"`  // 收藏夹数量（本人=全部，他人=仅公开）
	VideoCount     int64                   `json:"video_count"`     // 投稿数（本人=全部未删，他人=仅审核通过且公开）
	IsSelf         bool                    `json:"is_self"`         // 是否本人视角（前端据此显示"编辑资料"入口）

	// 以下字段仅本人可见
	CoinBalance  *uint  `json:"coin_balance,omitempty"`  // 硬币余额
	HistoryCount *int64 `json:"history_count,omitempty"` // 观看历史条数
	UnreadCount  *int64 `json:"unread_count,omitempty"`  // 未读消息数（供个人中心红点）
}
