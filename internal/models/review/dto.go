// Package review 的响应 DTO。
package review

import (
	modelsVideo "LikeBili/internal/models/video"
	"time"
)

// ReviewReq 管理员提交审核的请求体（语义：写方向）。
type ReviewReq struct {
	Result uint8  `json:"result"` // 审核结果：2=通过 3=失败
	Reason string `json:"reason"` // 审核意见（失败时即驳回原因）
}

// CleanupReq 管理员硬删除软删超期视频的请求体。
type CleanupReq struct {
	Days int `json:"days"` // 保留时限（天）：仅清理软删超过该天数的视频；<1 时服务端钳制为 30
}

// ReviewResp 作者/管理端查看审核结果的响应体（语义：读方向）。
type ReviewResp struct {
	ID        uint      `json:"id"`         // 审核记录 ID
	VideoID   uint      `json:"video_id"`   // 被审核的视频 ID
	AdminID   uint      `json:"admin_id"`   // 审核管理员 ID
	Result    uint8     `json:"result"`     // 审核结果：2=通过 3=失败
	Reason    string    `json:"reason"`     // 审核意见（失败时即驳回原因）
	CreatedAt time.Time `json:"created_at"` // 审核时间
}

// VideoDetailResp 审核详情页响应：视频完整信息 + 全部审核历史。
// 审核后台打开单个视频时返回，reviews 按时间倒序（最新在前）。
type VideoDetailResp struct {
	Video   *modelsVideo.VideoResp `json:"video"`   // 视频信息（复用公开视频 DTO）
	Reviews []ReviewResp           `json:"reviews"` // 审核历史（倒序，最新在前；未审核过则为空数组）
}
