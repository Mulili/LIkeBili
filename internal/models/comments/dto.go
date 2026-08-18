// Package comments 的请求/响应 DTO。
package comments

import (
	modelsUser "LikeBili/internal/models/user"
	"time"
)

// ===============请求体======================

// CommentReq 发表评论的请求体（写方向）。
// ParentID 为 0 表示根评论；非 0 表示回复，指向直接父级评论 ID。
// RootID 无需前端传入：服务端根据父评论的 RootID 继承（父为根评论时取父评论 ID），
// 前端无法篡改树的归属。
type CommentReq struct {
	Content  string `json:"content" validate:"required,max=1000"` // 评论内容，必填，最长 1000 字符
	ParentID uint   `json:"parent_id"`                            // 直接父级评论 ID：0=根评论，非 0=父评论 ID
}

// ===============响应体======================

// CommentResp 单条评论的响应体（读方向），根评论与子评论共用。
// 根评论场景下额外带 ReplyTotal（回复总数）和 Replies（第一页回复），
// 更多回复通过子评论分页接口（ReplyListResp）加载。
// 子评论场景下 ReplyTotal/Replies 不返回（omitempty）。
// IsLiked 依赖登录态：当前用户已赞为 true，未登录恒为 false。
type CommentResp struct {
	ID        uint                  `json:"id"`         // 评论 ID
	VideoID   uint                  `json:"video_id"`   // 所属视频 ID
	ParentID  uint                  `json:"parent_id"`  // 直接父级评论 ID（0=根评论）
	RootID    uint                  `json:"root_id"`    // 根评论 ID（整棵树的根）
	Content   string                `json:"content"`    // 评论内容
	Likes     uint                  `json:"likes"`      // 点赞总数
	IsLiked   bool                  `json:"is_liked"`   // 当前登录用户是否已赞
	CreatedAt time.Time             `json:"created_at"` // 评论时间
	User      *modelsUser.UserBrief `json:"user"`       // 评论者简要信息（头像/昵称）

	ReplyTotal uint          `json:"reply_total,omitempty"` // 仅根评论：该根评论下的回复总数
	Replies    []CommentResp `json:"replies,omitempty"`     // 仅根评论：第一页回复（加载更多走 ReplyListResp）
}

// CommentListResp 视频根评论列表响应体（根评论分页）。
// Total 为根评论总数；每条根评论带第一页回复（Replies）与回复总数（ReplyTotal）。
type CommentListResp struct {
	List     []CommentResp `json:"list"`      // 当前页根评论列表
	Total    uint          `json:"total"`     // 根评论总数
	Page     uint16        `json:"page"`      // 当前页码
	PageSize uint16        `json:"page_size"` // 每页条数
}

// ReplyListResp 某根评论下的子评论列表响应体（子评论分页）。
// 通过子评论分页接口按 root_id 查询：GET /videos/:id/comments/:root_id/replies?page=X
type ReplyListResp struct {
	List     []CommentResp `json:"list"`      // 当前页子评论列表
	Total    uint          `json:"total"`     // 该根评论下子评论总数
	Page     uint16        `json:"page"`      // 当前页码
	PageSize uint16        `json:"page_size"` // 每页条数
}

// CommentLikeResp 评论点赞/取消后的响应体。
// 前端依据 Liked 切换红心状态，依据 Likes 更新计数。
type CommentLikeResp struct {
	Liked bool `json:"liked"` // true=已点赞，false=已取消
	Likes uint `json:"likes"` // 最新点赞总数
}
