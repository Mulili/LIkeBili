// Package favorites 提供收藏夹相关的数据访问层（Repository）实现。
// 负责对收藏夹（Favorites）表进行 CRUD 操作，屏蔽底层 GORM 细节。
package favorites

import (
	modelsFavorites "LikeBili/internal/models/favorites"
	modelVideo "LikeBili/internal/models/video"
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

// 创建一个新的收藏夹
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

// CountItem 统计指定收藏夹中已收藏的视频数量。
// 参数 favoritesID 为收藏夹 ID。
// 返回视频总数和可能的错误。
func (r *Repository) CountItem(c context.Context, favoritesID uint) (int64, error) {
	var count int64
	if err := r.db.WithContext(c).Model(&modelsFavorites.FavoritesItem{}).Where("favorites_id = ?", favoritesID).Count(&count).Error; err != nil {
		return 0, fmt.Errorf("Method:favorites.repository.CountItem: %w", err)
	}
	return count, nil
}

// FindItem 查询指定收藏夹中是否已存在指定的视频。
// 参数 favoriteID 为收藏夹 ID，itemID 为视频 ID。
// 返回 (nil, nil) 表示未收藏过该视频；
// 返回 (*FavoritesItem, nil) 表示已存在；
// 返回 (nil, error) 表示数据库查询出错。
func (r *Repository) FindItem(c context.Context, favoriteID, itemID uint) (*modelsFavorites.FavoritesItem, error) {
	var item modelsFavorites.FavoritesItem
	if err := r.db.WithContext(c).Where("favorites_id = ? AND video_id = ?", favoriteID, itemID).First(&item).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("Method:favorites.repository.FindItem: %w", err)
	}
	return &item, nil
}

// CreateItem 向收藏夹中添加一条视频收藏记录。
// 参数 item 为待插入的 FavoritesItem 模型指针。
// 成功返回 nil，失败返回包装后的错误。
func (r *Repository) CreateItem(c context.Context, item *modelsFavorites.FavoritesItem) error {
	if err := r.db.WithContext(c).Create(item).Error; err != nil {
		return fmt.Errorf("Method:favorites.repository.CreateItem: %w", err)
	}
	return nil
}

// DeleteItem 根据收藏记录主键 ID 删除一条收藏记录。
// 参数 id 为 FavoritesItem 的主键 ID（非 video_id）。
// 成功返回 nil，失败返回包装后的错误。
func (r *Repository) DeleteItem(c context.Context, id uint) error {
	if err := r.db.WithContext(c).Delete(&modelsFavorites.FavoritesItem{}, id).Error; err != nil {
		return fmt.Errorf("Method:favorites.repository.DeleteItem: %w", err)
	}
	return nil
}

// FindFavoriteItems 分页查询指定收藏夹中的视频列表。
// 使用三阶段查询：
//  1. 统计总数（count）
//  2. 从 favorite_items 表中按创建时间降序取出 video_id 列表（Pluck）
//  3. 根据 video_id 批量查询 video 表，并按第 2 步的 ID 顺序组装结果
//
// 参数 favoriteID 为收藏夹 ID，page 从 1 开始，pageSize 为每页数量。
// 返回视频列表、总数和可能的错误。即使没有数据也返回空切片而非 nil。
func (r *Repository) FindFavoriteItems(c context.Context, favoriteID uint, page, pageSize int) ([]modelVideo.Video, int64, error) {
	var videos []modelVideo.Video
	var videoID []uint
	var total int64

	if err := r.db.WithContext(c).Model(&modelsFavorites.FavoritesItem{}).Where("favorites_id = ?", favoriteID).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("Method:favorites.repository.FindFavoriteItems: %w", err)
	}

	offset := (page - 1) * pageSize
	if err := r.db.WithContext(c).
		Model(&modelsFavorites.FavoritesItem{}).
		Order("created_at DESC").
		Offset(offset).
		Limit(pageSize).
		Pluck("video_id", &videoID).Error; err != nil {
		return nil, 0, fmt.Errorf("Method:favorites.repository.FindFavoriteItems: %w", err)
	}

	// 无收藏项时提前返回
	if len(videoID) == 0 {
		return nil, total, nil
	}

	if err := r.db.WithContext(c).Preload("User").
		Where("id IN ?", videoID).
		Find(&videos).Error; err != nil {
		return nil, 0, fmt.Errorf("Method:favorites.repository.FindFavoriteItems: %w", err)
	}

	// 将查询到的视频按 videoID 的原始顺序（即收藏时间倒序）重新排列
	orderedVideos := make([]modelVideo.Video, 0, len(videoID))
	videoMap := make(map[uint]modelVideo.Video, len(videos))
	for _, v := range videos {
		videoMap[v.ID] = v
	}
	for _, id := range videoID {
		if v, ok := videoMap[id]; ok {
			orderedVideos = append(orderedVideos, v)
		}
	}

	return orderedVideos, total, nil
}

// FindDefaultFavorite 查找用户的默认收藏夹。
// 默认收藏夹的名称为"默认收藏夹"，在用户注册时自动创建。
// 返回 (nil, error) 表示查询出错或收藏夹不存在（需调用方判断）。
func (r *Repository) FindDefaultFavorite(c context.Context, userid uint) (*modelsFavorites.Favorites, error) {
	var favorites modelsFavorites.Favorites
	if err := r.db.WithContext(c).Where("user_id = ? AND name = ?", userid, "默认收藏夹").First(&favorites).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("Method:favorites.repository.FindDefaultFavorite: %w", err)
	}
	return &favorites, nil
}

// FindItemFirstCover 查询收藏夹中最早收藏的视频封面 URL，用于收藏夹封面展示。
// 参数 favoriteID 为收藏夹 ID。
// 第一步：从 favorite_items 表中按收藏时间升序取最早的一条记录；
// 第二步：根据 video_id 从 videos 表中查询 cover_url。
// 返回封面 URL 和可能的错误。
func (r *Repository) FindItemFirstCover(c context.Context, favoriteID uint) (string, error) {
	var item modelsFavorites.FavoritesItem
	var video modelVideo.Video

	if err := r.db.WithContext(c).Where("favorites_id = ?", favoriteID).Order("created_at DESC").First(&item).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return "", nil
		}
		return "", fmt.Errorf("Method:favorites.repository.FindItemFirstCover: %w", err)
	}

	if err := r.db.WithContext(c).Select("cover_url").First(&video, item.VideoID).Error; err != nil {
		return "", fmt.Errorf("Method:favorites.repository.FindItemFirstCover: %w", err)
	}
	return video.CoverURL, nil
}
