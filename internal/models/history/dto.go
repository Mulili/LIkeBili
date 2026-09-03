package history

import (
	modelsVideo "LikeBili/internal/models/video"
	"time"
)

// 请求体
// CreateHistoryReq 创建/更新观看历史请求体。
type CreateHistoryReq struct {
	VideoID  uint    `json:"video_id" validate:"required"` // 视频 ID
	Progress uint    `json:"progress"`                     // 观看进度（秒）
	Duration float64 `json:"duration,omitempty"`           // 视频总时长（秒）
}

// 响应体
// HistoryItemResp 观看历史项响应体。
type HistoryItemResp struct {
	Video     modelsVideo.VideoResp `json:"video"`      // 视频信息
	Progress  uint                  `json:"progress"`   // 观看进度（秒）
	WatchedAt time.Time             `json:"watched_at"` // 最近观看时间
}

// HistoryListResp 观看历史列表响应体。
type HistoryListResp struct {
	Items    []HistoryItemResp `json:"items"`     // 历史记录列表
	Total    int64             `json:"total"`     // 历史记录总数
	Page     uint16            `json:"page"`      // 当前页码
	PageSize uint16            `json:"page_size"` // 每页数量
}
