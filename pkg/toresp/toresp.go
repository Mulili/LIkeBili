// Package toresp 提供跨模块复用的"数据库模型 → 响应 DTO"转换器。
// 评论、关注、历史、管理等模块返回视频/用户信息时统一走本包，
// 保证 URL 拼接规则全项目一致：封面/头像永久公开、播放地址不在此生成。
package toresp

import (
	modelsUser "LikeBili/internal/models/user"
	modelsVideo "LikeBili/internal/models/video"
	"LikeBili/pkg/storage"
)

// VideoRespBuilder 把 *modelsVideo.Video 转成 *modelsVideo.VideoResp。
// 持有 storage 用于拼公开 URL。播放地址（VideoURL）已从 DTO 移除：
// 它属于受保护内容，由视频模块的 GetPresignedUrl 现签 1 小时预签名 URL。
type VideoRespBuilder struct {
	storage   *storage.MinIO
	userBrief *UserBriefRespBuilder
}
type UserBriefRespBuilder struct {
	storage *storage.MinIO
}

// UserBrief 转换器，用于转换UserBrief同时添加
func NewToRespBuilder(storage *storage.MinIO) *UserBriefRespBuilder {
	return &UserBriefRespBuilder{storage: storage}
}

// NewVideoRespBuilder 构造转换器。各 Service 用自己持有的 storage 实例化。
func NewVideoRespBuilder(storage *storage.MinIO, userBrief *UserBriefRespBuilder) *VideoRespBuilder {
	return &VideoRespBuilder{storage: storage, userBrief: userBrief}
}

// ToVideoResp 转换主方法：Video → VideoResp。
// 封面/头像经 storage.URL 拼成永久公开 URL（空串自动返回空）。
func (b *VideoRespBuilder) ToVideoResp(v *modelsVideo.Video) *modelsVideo.VideoResp {
	resp := &modelsVideo.VideoResp{
		ID:          v.ID,
		Title:       v.Title,
		Description: v.Description,
		CoverURL:    b.storage.URL(v.CoverURL), // 封面永久公开；空串返回空
		Duration:    v.Duration,
		FileSize:    v.FileSize,
		CategoryID:  v.CategoryID,
		Status:      v.Status,
		Views:       v.Views,
		CreatedAt:   v.CreatedAt,
		UpdatedAt:   v.UpdatedAt,
	}
	if v.User.ID != 0 {
		resp.User = b.userBrief.ToUserBriefResp(&v.User)
	}
	return resp
}

func (t *UserBriefRespBuilder) ToUserBriefResp(u *modelsUser.User) *modelsUser.UserBrief {
	brief := &modelsUser.UserBrief{
		ID:       u.ID,
		Username: u.Username,
		Nickname: u.Nickname,
		Avatar:   t.storage.URL(u.Avatar),
	}
	return brief
}
