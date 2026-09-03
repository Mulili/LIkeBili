package history

import (
	modelsHistory "LikeBili/internal/models/history"
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// CreateOrUpdateHistory 创建或更新观看历史（upsert）。
// 同一用户对同一视频只保留一条记录：若 (user_id, video_id) 已存在则更新观看进度与最近观看时间，否则插入新记录。
func (r *Repository) CreateOrUpdateHistory(c context.Context, h *modelsHistory.UserHistory) error {
	result := r.db.WithContext(c).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}, {Name: "video_id"}},       // 冲突判定依据唯一索引 uk_user_video
		DoUpdates: clause.AssignmentColumns([]string{"progress", "watched_at"}), // 冲突时只更新进度与观看时间，保留首次观看的 id
	}).Create(h)
	if result.Error != nil {
		return fmt.Errorf("Method:history.repository.CreateOrUpdateHistory: %w", result.Error)
	}
	return nil
}

// FindUserViewVideo 查询某用户对某视频的历史记录。
// 返回 (nil, nil) 表示该用户尚未观看过此视频，便于调用方区分"未找到"与"查询出错"。
func (r *Repository) FindUserViewVideo(c context.Context, userID, videoID uint) (*modelsHistory.UserHistory, error) {
	var h modelsHistory.UserHistory
	result := r.db.WithContext(c).
		Where("user_id = ? AND video_id = ?", userID, videoID).
		First(&h)
	if result.Error != nil {
		// 未查到记录不属于错误，返回 nil 让上层走"首次观看"逻辑
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("Method:history.repository.FindUserViewVideo: %w", result.Error)
	}
	return &h, nil
}

// FindListHistory 分页查询某用户的观看历史，按 watched_at 倒序（由复合索引 idx_user_watched 支撑）。
// 同时预加载关联视频及其发布者信息，返回记录列表与该用户历史总数。
func (r *Repository) FindListHistory(c context.Context, userID uint, page, pageSize int) (*[]modelsHistory.UserHistory, int64, error) {
	var list []modelsHistory.UserHistory
	var total int64
	// 先按用户过滤统计总数，供响应体返回 total 做分页
	query := r.db.WithContext(c).Model(&modelsHistory.UserHistory{}).Where("user_id = ?", userID)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("Method:history.repository.FindListHistory: %w", err)
	}

	offset := (page - 1) * pageSize
	// Preload("Video") 关联视频、Preload("Video.User") 关联发布者，避免 N+1 查询
	if err := query.Preload("Video").Preload("Video.User").
		Offset(offset).Limit(pageSize).Find(&list).Error; err != nil {
		return nil, 0, fmt.Errorf("Method:history.repository.FindListHistory: %w", err)
	}
	// watched_at 为 not null 字段，若写入时传入零值 time.Time，库里会存成 0001-01-01 00:00:00 而非 NULL
	// 读出后 IsZero() 为 true，这里统一回填查询时刻，避免把异常时间返回给前端
	now := time.Now()
	for i := range list {
		if list[i].WatchedAt.IsZero() {
			list[i].WatchedAt = now
		}
	}
	return &list, total, nil
}
