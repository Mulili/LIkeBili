package like

import (
	modelsLike "LikeBili/internal/models/like"
	"context"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// Delete 取消点赞：按"用户 + 视频"删除点赞关系记录（物理删除，表无软删字段）。
func (r *Repository) Delete(c context.Context, userID, videoID uint) error {
	if err := r.db.WithContext(c).Where("video_id = ? and user_id = ?", videoID, userID).Delete(&modelsLike.VideoLikes{}).Error; err != nil {
		return fmt.Errorf("Method:like.repository.Delete: %w", err)
	}
	return nil
}

// Exists 判断用户是否已点赞该视频：true=已点赞，false=未点赞。
func (r *Repository) Exists(c context.Context, userID, videoID uint) (bool, error) {
	var likes modelsLike.VideoLikes
	if err := r.db.WithContext(c).
		Where("user_id = ? AND video_id = ?", userID, videoID).
		Limit(1).Find(&likes).Error; err != nil {
		return false, fmt.Errorf("Method:like.repository.Exists: %w", err)
	}
	return likes.ID != 0, nil
}

// InsertIgnore 幂等插入点赞记录（冲突时忽略，不报错）。
// 依赖 video_likes 表的 uk_user_video 联合唯一索引：
// 同一用户对同一视频已有点赞时，INSERT 冲突被忽略（Modifier: "IGNORE" → MySQL INSERT IGNORE）。
// 返回值：true=本次真正插入（新增点赞）；false=记录已存在（重复点赞，被忽略）。
// 供 service 层区分"新增点赞"与"重复点击"，避免先查再插的并发竞态。
func (r *Repository) InsertIgnore(c context.Context, userID, videoID uint) (bool, error) {
	result := r.db.WithContext(c).
		Clauses(clause.Insert{Modifier: "IGNORE"}).
		Create(&modelsLike.VideoLikes{UserID: userID, VideoID: videoID})
	if result.Error != nil {
		return false, fmt.Errorf("Method:like.repository.InsertIgnore: %w", result.Error)
	}
	return result.RowsAffected > 0, nil
}

// Count 统计视频的点赞总数。
// 因为 videos 表不冗余 like_count 字段，点赞数需要实时从 video_likes 表 COUNT 得出，
// 供视频详情页/列表展示点赞数使用。
// 数据量大时后续可叠加 Redis 计数缓存（本层只负责 DB 计数，缓存策略由上层决策）。
func (r *Repository) Count(c context.Context, videoID uint) (int64, error) {
	var count int64
	if err := r.db.WithContext(c).
		Model(&modelsLike.VideoLikes{}).
		Where("video_id = ?", videoID).
		Count(&count).Error; err != nil {
		return 0, fmt.Errorf("Method:like.repository.Count: %w", err)
	}
	return count, nil
}

// InformAuthorLike 查询视频作者的用户 ID（用于点赞后通知作者）。
// 注意：视频不存在或已软删时，Pluck 返回 gorm.ErrRecordNotFound（而非 nil 空值），
// 由 service 用 errors.Is(err, gorm.ErrRecordNotFound) 判断作者不存在；
// err==nil 时 authorID 为有效作者 ID。
func (r *Repository) InformAuthorLike(c context.Context, videoID uint) (uint, error) {
	var authorID uint
	if err := r.db.WithContext(c).Table("videos").
		Where("id = ?", videoID).Pluck("user_id", &authorID).Error; err != nil {
		return 0, fmt.Errorf("Method:like.repository.InformAuthorLike: %w", err)
	}
	return authorID, nil
}

// FindLikeUsername 查询用户展示名（昵称优先，昵称为空时回退用户名，用于点赞通知文案）。
// 注意：用户不存在时，Pluck 返回 gorm.ErrRecordNotFound（而非 nil），
// 由 service 用 errors.Is 判断；err==nil 时 name 即为有效展示名。
func (r *Repository) FindLikeUsername(c context.Context, userID uint) (string, error) {
	var name string
	if err := r.db.WithContext(c).Table("users").
		Where("id = ?", userID).
		Pluck("COALESCE(NULLIF(nickname,''),username)", &name).Error; err != nil {
		return "", fmt.Errorf("Method:like.repository.FindLikeUsername: %w", err)
	}
	return name, nil
}
