// Package favorites 提供收藏夹相关的数据访问层（Repository）实现。
// 负责对收藏夹（Favorites）表进行 CRUD 操作，屏蔽底层 GORM 细节。
package favorites

import (
	modelsFavorites "LikeBili/internal/models/favorites"
	modelVideo "LikeBili/internal/models/video"
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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

// ExistsByName 判断该用户下是否已存在同名收藏夹（创建前查重）。
// 走 user_id 索引即可；收藏夹数量很少，无需额外建 (user_id, name) 联合索引。
func (r *Repository) ExistsByName(c context.Context, userID uint, name string) (bool, error) {
	var count int64
	if err := r.db.WithContext(c).Model(&modelsFavorites.Favorites{}).
		Where("user_id = ? AND name = ?", userID, name).
		Count(&count).Error; err != nil {
		return false, fmt.Errorf("Method:favorites.repository.ExistsByName: %w", err)
	}
	return count > 0, nil
}

// CountFavorites 统计用户的收藏夹数量，供个人中心聚合展示。
// onlyPublic=true 只统计公开收藏夹（他人主页视角）；false 统计全部（本人视角，含私密）。
func (r *Repository) CountFavorites(c context.Context, userID uint, onlyPublic bool) (int64, error) {
	query := r.db.WithContext(c).Model(&modelsFavorites.Favorites{}).Where("user_id = ?", userID)
	if onlyPublic {
		query = query.Where("is_public = ?", 1)
	}

	var count int64
	if err := query.Count(&count).Error; err != nil {
		return 0, fmt.Errorf("Method:favorites.repository.CountFavorites: %w", err)
	}
	return count, nil
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
// 幂等：命中联合唯一索引 uk_fav_video（同一收藏夹重复收藏同一视频）时冲突忽略、不报错，
// 避免"先查再插"在并发双击下抛 Duplicate entry 导致 500。
func (r *Repository) CreateItem(c context.Context, item *modelsFavorites.FavoritesItem) error {
	if err := r.db.WithContext(c).Clauses(clause.OnConflict{DoNothing: true}).Create(item).Error; err != nil {
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

// Item 收藏夹条目（Repository 层中间结构，供 Service 组装响应与失效占位）。
//   - VideoID：始终有值，即使视频已删除（前端去重/跳转判断需要）
//   - Video：nil 表示该视频已软删除或记录不存在（GORM 默认 scope 已过滤），由 Service 判定为失效
//   - CreatedAt：收藏时间，用于按收藏时间倒序展示
type Item struct {
	VideoID   uint
	Video     *modelVideo.Video
	CreatedAt time.Time
}

// FindFavoriteItems 分页查询指定收藏夹中的条目列表（按收藏时间倒序）。
// 与旧实现的关键差异：**不再丢弃查不到的视频**——返回行数始终与分页一致，
// 已删除/不可见的视频由 Service 替换为失效占位，保证 total 与 items 行数同口径。
//
// 三阶段查询：
//  1. 统计总数（含失效条目）
//  2. 取本页 favorite_items 行（video_id + created_at，保持收藏时间倒序）
//  3. 按 video_id 批量查 videos（Preload 发布者），再映射回条目原顺序
func (r *Repository) FindFavoriteItems(c context.Context, favoriteID uint, page, pageSize int) ([]Item, int64, error) {
	var rows []modelsFavorites.FavoritesItem
	var total int64

	// ① 总数：收藏夹内条目数（含失效视频，与返回行数保持同口径）
	if err := r.db.WithContext(c).Model(&modelsFavorites.FavoritesItem{}).
		Where("favorites_id = ?", favoriteID).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("Method:favorites.repository.FindFavoriteItems: %w", err)
	}

	// ② 本页条目：取整行（不再只 Pluck video_id，否则失效条目会整行丢失）
	offset := (page - 1) * pageSize
	if err := r.db.WithContext(c).
		Where("favorites_id = ?", favoriteID).
		Order("created_at DESC").
		Offset(offset).
		Limit(pageSize).
		Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("Method:favorites.repository.FindFavoriteItems: %w", err)
	}
	if len(rows) == 0 {
		return nil, total, nil
	}

	// ③ 批量查视频：GORM 默认 scope 自动过滤软删除，查不到即"已删除/不存在"
	videoIDs := make([]uint, len(rows))
	for i := range rows {
		videoIDs[i] = rows[i].VideoID
	}
	var videos []modelVideo.Video
	if err := r.db.WithContext(c).Preload("User").
		Where("id IN ?", videoIDs).
		Find(&videos).Error; err != nil {
		return nil, 0, fmt.Errorf("Method:favorites.repository.FindFavoriteItems: %w", err)
	}
	videoMap := make(map[uint]*modelVideo.Video, len(videos))
	for i := range videos {
		videoMap[videos[i].ID] = &videos[i]
	}

	// ④ 按条目原顺序组装：缺失的视频 Video 置 nil（不丢行），交由 Service 做占位
	items := make([]Item, 0, len(rows))
	for i := range rows {
		items = append(items, Item{
			VideoID:   rows[i].VideoID,
			Video:     videoMap[rows[i].VideoID],
			CreatedAt: rows[i].CreatedAt,
		})
	}
	return items, total, nil
}

// FindDefaultFavorite 查找用户的默认收藏夹。
// 默认收藏夹的名称由 modelsFavorites.DefaultFavoriteName 定义，在用户首次访问收藏夹时自动创建。
// 返回 (nil, nil) 表示收藏夹不存在
func (r *Repository) FindDefaultFavorite(c context.Context, userid uint) (*modelsFavorites.Favorites, error) {
	var favorites modelsFavorites.Favorites
	if err := r.db.WithContext(c).Where("user_id = ? AND name = ?", userid, modelsFavorites.DefaultFavoriteName).First(&favorites).Error; err != nil {
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
