// Package favorites 提供收藏夹模块的模型层定义，包含数据模型（model）和传输对象（DTO）。
// DTO 文件定义客户端与服务端之间的请求/响应结构体，用于参数绑定和接口契约。
package favorites

import "LikeBili/internal/models/video"

// =================== 请求体 ===================

// FavoritesReq 创建或更新收藏夹时客户端传入的请求体。
// Name 为必填字段，最长 16 个字符。
// IsPublic 为可选字段，使用 *uint8（指针）来区分"不传"和"传 0"两种语义：
//   - 不传（nil）→ 服务层使用默认值 1（公开）
//   - 传 0 → 私密
//   - 传 1 → 公开
type FavoritesReq struct {
	Name     string `json:"name" validate:"required,max=16"`          // 收藏夹名称（必填，1~16 个字符）
	IsPublic *uint8 `json:"is_public" validate:"omitempty,oneof=0 1"` // 是否公开（可选：不传=默认公开，0=私密，1=公开）
}

// FavoritesItemReq 收藏/取消收藏视频时客户端传入的请求体。
// VideoID 指定要操作的目标视频，必填。
type FavoritesItemReq struct {
	VideoID uint `json:"video_id" validate:"required"` // 要收藏或取消收藏的视频 ID（必填）
}

// =================== 响应体 ===================

// FavoritesResp 收藏夹概要信息响应，用于列表展示。
// 不包含具体的视频列表，仅返回收藏夹自身属性和统计信息。
type FavoritesResp struct {
	ID        uint   `json:"id"`                  // 收藏夹主键 ID
	Name      string `json:"name"`                // 收藏夹名称
	IsPublic  uint8  `json:"is_public"`           // 是否公开：1=公开，0=私密
	ItemCount int64  `json:"item_count"`          // 该收藏夹内收藏的视频总数
	CoverURL  string `json:"cover_url,omitempty"` // 封面图 URL（取最早收藏的视频封面，空则不返回此字段）
}

// FavoriteDetailResp 收藏夹详情响应，包含收藏夹信息及其分页视频列表。
// 用于收藏夹详情页展示，Items 中按收藏时间降序排列。
type FavoriteDetailResp struct {
	Favorite FavoritesResp     `json:"favorite"`  // 收藏夹基本信息
	Items    []video.VideoResp `json:"items"`     // 当前页的视频列表（按收藏时间降序）
	Total    int64             `json:"total"`     // 该收藏夹的视频总数（用于前端分页计算）
	Page     int               `json:"page"`      // 当前页码（从 1 开始）
	PageSize int               `json:"page_size"` // 每页条数
}

// FavoriteToggleResp 收藏/取消收藏操作的响应。
// 用于通知前端当前视频的收藏状态，方便 UI 即时更新。
type FavoriteToggleResp struct {
	Favorited bool `json:"favorited"` // 当前操作后的收藏状态：true=已收藏，false=未收藏
}
