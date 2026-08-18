package comment

import (
	modelsComment "LikeBili/internal/models/comments"
	modelsVideo "LikeBili/internal/models/video"
	"context"
	"fmt"

	"gorm.io/gorm"
)

type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Create(c context.Context, comment *modelsComment.VideoComments) error {
	if err := r.db.WithContext(c).Create(comment).Error; err != nil {
		return fmt.Errorf("Method:comment.repository.Create: %w", err)
	}
	return nil
}

func (r *Repository) FindByID(c context.Context, id uint) (*modelsComment.VideoComments, error) {
	var cmt modelsComment.VideoComments
	if err := r.db.WithContext(c).Preload("User").First(&cmt, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("Method:comment.repository.FindByID: %w", err)
	}
	return &cmt, nil
}

func (r *Repository) FindRootComments(c context.Context, videoID uint, sort string, page, pageSize int) ([]modelsComment.VideoComments, int64, error) {
	var total int64
	query := r.db.WithContext(c).Model(&modelsComment.VideoComments{}).Where("video_id = ? AND parent_id = 0", videoID)

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("Method:comment.repository.FindRootComments: %w", err)
	}

	orderBy := "created_at DESC"
	if sort == "hot" {
		orderBy = "likes DESC"
	}

	offset := (page - 1) * pageSize

	var list []modelsComment.VideoComments
	if err := query.Preload("User").
		Order(orderBy).Offset(offset).
		Limit(pageSize).Find(&list).Error; err != nil {
		return nil, 0, fmt.Errorf("Method:comment.repository.FindRootComments: %w", err)
	}
	return list, total, nil
}

func (r *Repository) FindRepliesByRootIDs(c context.Context, ids []uint) ([]modelsComment.VideoComments, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	var list []modelsComment.VideoComments
	if err := r.db.WithContext(c).Preload("User").
		Where("root_id IN ?", ids).
		Order("created_at ASC").Find(&list).Error; err != nil {
		return nil, fmt.Errorf("Method:comment.repository.FindRepliesByRootIDs: %w", err)
	}
	return list, nil
}

func (r *Repository) Delete(c context.Context, id uint) error {
	if err := r.db.Delete(&modelsComment.VideoComments{}, id).Error; err != nil {
		return fmt.Errorf("Method:comment.repository.Delete: %w", err)
	}
	return nil
}

func (r *Repository) UpdateVideoLikes(c context.Context, id, likes uint) error {
	if err := r.db.WithContext(c).
		Model(&modelsComment.VideoComments{}).
		Where("id = ?", id).
		Update("likes", likes).Error; err != nil {
		return fmt.Errorf("Method:comment.repository.UpdateVideoLikes: %w", err)
	}
	return nil
}

// 用于当有人评论时通知视频作者
func (r *Repository) FindAuthorID(c context.Context, videoID uint) (uint, error) {
	var authorID uint
	if err := r.db.WithContext(c).
		Table("videos").Where("id = ?", videoID).
		Pluck("user_id", authorID).Error; err != nil {
		return 0, fmt.Errorf("Method:comment.repository.FindAuthorID: %w", err)
	}
	return authorID, nil
}

// 评论前判断视频是否真实存在
func (r *Repository) FindVideoExist(c context.Context, videoID uint) (bool, error) {
	var video *modelsVideo.Video
	if err := r.db.WithContext(c).
		Table("videos").Where("id = ?", videoID).
		First(video).Error; err != nil {
		return false, fmt.Errorf("Method:comment.repository.FindVideoExist: %w", err)
	}
	return video != nil, nil
}
