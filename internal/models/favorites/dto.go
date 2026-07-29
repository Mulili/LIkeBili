package favorites

// =================== 请求体 ===================

// FavoritesReq 创建/更新收藏夹请求体。
// Name 为必填，最长 16 个字符。
// IsPublic 为可选，不传则不修改公开状态；传值只能为 0（私密）或 1（公开）。
type FavoritesReq struct {
	Name     string `json:"name" validate:"required,max=16"`          // 收藏夹名称（必填，最长 16 字）
	IsPublic *uint8 `json:"is_public" validate:"omitempty,oneof=0 1"` // 是否公开（可选，不传不修改，传值限 0 或 1）
}

// FavoritesItemReq 收藏视频请求体。
// VideoID 指定要收藏的视频。
type FavoritesItemReq struct {
	VideoID uint `json:"video_id" validate:"required"` // 要收藏的视频 ID（必填）
}

// =================== 响应体 ===================

// FavoritesResp 收藏夹详情响应。
type FavoritesResp struct {
	ID         uint   `json:"id"`          // 收藏夹 ID
	UserID     uint   `json:"user_id"`     // 所属用户 ID
	Name       string `json:"name"`        // 收藏夹名称
	IsPublic   uint8  `json:"is_public"`   // 是否公开：1=公开，0=私密
	VideoCount int64  `json:"video_count"` // 该收藏夹内的视频数量
	CreatedAt  string `json:"created_at"`  // 收藏夹创建时间
}

// FavoritesItemResp 收藏夹内单个视频的响应。
type FavoritesItemResp struct {
	ID          uint   `json:"id"`                    // 收藏记录 ID
	FavoritesID uint   `json:"favorites_id"`          // 所属收藏夹 ID
	VideoID     uint   `json:"video_id"`              // 被收藏的视频 ID
	VideoTitle  string `json:"video_title,omitempty"` // 视频标题（联表查询时填充）
	VideoCover  string `json:"video_cover,omitempty"` // 视频封面（联表查询时填充）
	CreatedAt   string `json:"created_at"`            // 收藏时间
}

// FavoritesListResp 收藏夹列表响应。
type FavoritesListResp struct {
	List  []FavoritesResp `json:"list"`  // 收藏夹列表
	Total int64           `json:"total"` // 收藏夹总数
}
