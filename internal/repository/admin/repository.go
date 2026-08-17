// Package admin 提供管理员审核视频的数据访问层。
// 审核业务：查询待审核（status=1）视频 → 管理员观看 → 通过(2)/驳回(3)。
// 驳回原因不写在视频表，而是落在 video_reviews 审核流水表（见 models/review）。
package admin

import (
	modelsReview "LikeBili/internal/models/review"
	modelsVideo "LikeBili/internal/models/video"
	"context"
	"fmt"

	"gorm.io/gorm"
)

// Repository 封装管理员审核相关的数据库操作，不含业务逻辑。
type Repository struct {
	db *gorm.DB
}

// NewRepository 创建管理员审核数据访问实例。
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// ListPending 分页查询待审核（status=1）的视频，带发布者与分类关联，供审核列表展示。
// 返回按发布时间倒序的列表及总数（total 用于前端分页）。
func (r *Repository) ListPending(c context.Context, page, pageSize int) ([]*modelsVideo.Video, int64, error) {
	var videos []*modelsVideo.Video
	var total int64
	// 先统计总数，再分页取数据（与 video 模块 FindList 模式一致）
	query := r.db.WithContext(c).Model(&modelsVideo.Video{}).Where("status = ?", 1)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("Method:admin.repository.ListPending: %w", err)
	}
	offset := (page - 1) * pageSize
	err := query.Preload("User").Preload("Category").
		Order("created_at DESC").
		Offset(offset).Limit(pageSize).
		Find(&videos).Error
	if err != nil {
		return nil, 0, fmt.Errorf("Method:admin.repository.ListPending: %w", err)
	}
	return videos, total, nil
}

// GetByID 按 ID 查视频（不过滤审核状态），供审核详情与审核观看使用。
// 注意：这里故意不走 video 模块的 FindVideoAndForbidden（它会把待审核视频视为不存在）。
func (r *Repository) GetByID(c context.Context, id uint) (*modelsVideo.Video, error) {
	var video modelsVideo.Video
	result := r.db.WithContext(c).Preload("User").First(&video, id)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, nil // 数据不存在不是错误
		}
		return nil, fmt.Errorf("Method:admin.repository.GetByID: %w", result.Error)
	}
	return &video, nil
}

// ReviewTx 在事务内完成一次审核：更新视频审核状态 + 写入一条审核流水记录，
// 保证"状态变更"与"审核留痕"要么同时成功、要么同时回滚。
// status 取值 2=通过 / 3=失败；失败时 reason 即驳回原因，通过时可为空串。
func (r *Repository) ReviewTx(c context.Context, videoID, adminID uint, status uint8, reason string) error {
	return r.db.WithContext(c).Transaction(func(tx *gorm.DB) error {
		// ① 更新视频状态（只改 status 字段，避免覆盖其它字段）
		if err := tx.Model(&modelsVideo.Video{}).Where("id = ?", videoID).Update("status", status).Error; err != nil {
			return err
		}
		// ② 写入审核流水
		review := &modelsReview.VideoReview{
			VideoID: videoID,
			AdminID: adminID,
			Result:  status,
			Reason:  reason,
		}
		return tx.Create(review).Error
	})
}

// ListReviews 查询某视频的全部审核记录，按审核时间倒序（最新在前）。
// 用途：审核详情页展示完整审核历史；作者端也可复用。
func (r *Repository) ListReviews(c context.Context, videoID uint) ([]modelsReview.VideoReview, error) {
	var reviews []modelsReview.VideoReview
	err := r.db.WithContext(c).Where("video_id = ?", videoID).
		Order("created_at DESC").
		Find(&reviews).Error
	if err != nil {
		return nil, fmt.Errorf("Method:admin.repository.ListReviews: %w", err)
	}
	return reviews, nil
}

// GetLatestReview 取某视频最新一条审核记录，按审核时间倒序取第一条。
// 用途：作者端展示"审核失败原因"时，查询 status=3 的视频的最新驳回意见。
func (r *Repository) GetLatestReview(c context.Context, videoID uint) (*modelsReview.VideoReview, error) {
	var review modelsReview.VideoReview
	result := r.db.WithContext(c).Where("video_id = ?", videoID).
		Order("created_at DESC").First(&review)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, nil // 尚无审核记录
		}
		return nil, fmt.Errorf("Method:admin.repository.GetLatestReview: %w", result.Error)
	}
	return &review, nil
}
