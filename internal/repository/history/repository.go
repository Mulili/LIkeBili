package history

import (
	modelsHistory "LikeBili/internal/models/history"
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

func (r *Repository) CreateOrUpdateHistory(c context.Context, h modelsHistory.UserHistory) error {
	result := r.db.WithContext(c).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}, {Name: "video_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"progress", "watched_at"}),
	}).Create(h)
	if result.Error != nil {
		return fmt.Errorf("Method:history.repository.CreateOrUpdateHistory: %w", result.Error)
	}
	return nil
}
