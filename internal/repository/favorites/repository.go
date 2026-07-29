// Package favorites 提供收藏夹相关的数据访问层（Repository）实现。
// 负责对收藏夹（Favorites）表进行 CRUD 操作，屏蔽底层 GORM 细节。
package favorites

import (
	modelsFavorites "LikeBili/internal/models/favorites"
	"context"
	"fmt"

	"gorm.io/gorm"
)

// Repository 封装收藏夹相关的数据库操作。
// 持有一个 *gorm.DB 实例，所有方法都应当通过 NewRepository 创建。
type Repository struct {
	db *gorm.DB
}

// NewRepository 创建一个新的 Repository 实例。
// 接收一个 *gorm.DB 作为唯一参数，调用方需确保 db 不为 nil。
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// Create 插入一条收藏夹记录。
// 参数 c 用于传递 context（支持超时/链路追踪），favorites 为待插入的模型指针。
// 成功返回 nil，失败返回包装后的错误。
func (r *Repository) Create(c context.Context, favorites *modelsFavorites.Favorites) error {
	if err := r.db.WithContext(c).Create(favorites).Error; err != nil {
		return fmt.Errorf("Method:favorites.repository.Create: %w", err)
	}
	return nil
}

// FindFavoritesByUserID 根据用户 ID 查询该用户的所有收藏夹。
// 结果按创建时间降序排列（最新的在前）。
// 即使没有记录也返回空切片（而非 nil），方便调用方直接 range。
func (r *Repository) FindFavoritesByUserID(c context.Context, userid uint) ([]modelsFavorites.Favorites, error) {
	var favorites []modelsFavorites.Favorites
	if err := r.db.WithContext(c).Where("user_id = ?", userid).
		Order("created_at DESC").
		Find(&favorites).Error; err != nil {
		return nil, fmt.Errorf("Method:favorites.repository.FindFavoritesByUserID: %w", err)
	}
	return favorites, nil
}

// FindByFavoritesID 根据收藏夹主键 ID 查询单条记录。
// 返回 (*modelsFavorites.Favorites, nil) 表示查询成功；
// 返回 (nil, nil) 表示记录不存在；
// 返回 (nil, error) 表示数据库查询出错。
func (r *Repository) FindByFavoritesID(c context.Context, id uint) (*modelsFavorites.Favorites, error) {
	var favorite modelsFavorites.Favorites
	if err := r.db.WithContext(c).Where("id = ?", id).First(&favorite).Error; err == gorm.ErrRecordNotFound {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("Method:favorites.repository.FindFavoritesByUserID: %w", err)
	}
	return &favorite, nil
}
func (r *Repository) CountItem(c context.Context, favoretesID int) (int64, error) {
	var count int64
	if err := r.db.WithContext(c).Model(&modelsFavorites.Favorites{}).Where("favorites_id = ?", favoretesID).Count(&count).Error; err != nil {
		return 0, fmt.Errorf("Method:favorites.repository.COuntItem: %w", err)
	}
	return count, nil
}
