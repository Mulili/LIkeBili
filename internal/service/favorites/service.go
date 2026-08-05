package favorites

import (
	modelsFavorites "LikeBili/internal/models/favorites"
	modelsUser "LikeBili/internal/models/user"
	modelsVideo "LikeBili/internal/models/video"
	repositoryFavorites "LikeBili/internal/repository/favorites"
	codeErrors "LikeBili/pkg/errors"
	"LikeBili/pkg/storage"
	"context"
	"fmt"
	"time"
)

type Service struct {
	repo    *repositoryFavorites.Repository
	storage *storage.MinIO
}

func NewService(repo *repositoryFavorites.Repository, storage *storage.MinIO) *Service {
	return &Service{repo: repo, storage: storage}
}

// ===========================业务代码===========================
// 创建收藏夹
func (s *Service) CreateFavorite(c context.Context, userid uint, req *modelsFavorites.FavoritesReq) (*modelsFavorites.FavoritesResp, error) {
	isPublic := uint8(1)
	if req.IsPublic != nil {
		isPublic = *req.IsPublic
	}

	favorite := &modelsFavorites.Favorites{
		UserID:   userid,
		Name:     req.Name,
		IsPublic: isPublic,
	}

	if err := s.repo.Create(c, favorite); err != nil {
		return nil, fmt.Errorf("favorite.service.CreateFavorite: %w", err)
	}

	return &modelsFavorites.FavoritesResp{
		ID:       favorite.ID,
		Name:     favorite.Name,
		IsPublic: favorite.IsPublic,
	}, nil
}

// 查找指定用户的所有公共收藏夹
func (s *Service) FindUserPublicFavorite(c context.Context, userid uint) ([]modelsFavorites.FavoritesResp, error) {
	var favorites []modelsFavorites.Favorites
	var err error
	if favorites, err = s.repo.FindFavoritesByUserID(c, userid); err != nil {
		return nil, fmt.Errorf("Method:favorites.service.FIndUserPublicFavorite: %w", err)
	}

	resp := make([]modelsFavorites.FavoritesResp, 0)
	for _, f := range favorites {
		if f.IsPublic == 0 {
			continue
		}

		count, err := s.repo.CountItem(c, f.ID)
		if err != nil {
			return nil, fmt.Errorf("Method:favorites.service.FIndUserPublicFavorite: %w", err)
		}

		var coverurl string
		if url, err := s.repo.FindItemFirstCover(c, f.ID); err == nil && url != "" {
			if presign, err := s.storage.GetPresignedURL(c, url, time.Hour); err == nil {
				coverurl = presign
			}
		}
		resp = append(resp, modelsFavorites.FavoritesResp{
			ID:        f.ID,
			Name:      f.Name,
			IsPublic:  f.IsPublic,
			ItemCount: count,
			CoverURL:  coverurl,
		})
	}
	return resp, nil
}

// 获取调用方用户的所有收藏夹，如果没有，自动创建默认收藏夹
func (s *Service) FindAllFavorites(c context.Context, userid uint) ([]modelsFavorites.FavoritesResp, error) {
	favorites, err := s.repo.FindFavoritesByUserID(c, userid)
	if err != nil {
		return nil, fmt.Errorf("Method:favorites.service.FindAllFavorites: %w", err)
	}
	if len(favorites) == 0 {
		if err := s.CreateDefaultFavorite(c, userid); err != nil {
			return nil, fmt.Errorf("Method:favorites.service.FindAllFavorites: %w", err)
		}
		//重新检查是否正常创建
		if favorites, err = s.repo.FindFavoritesByUserID(c, userid); err != nil {
			return nil, fmt.Errorf("Method:favorites.service.FindAllFavorites: %w", err)
		}
	}
	resp := make([]modelsFavorites.FavoritesResp, 0, len(favorites))
	for _, favorite := range favorites {
		count, err := s.repo.CountItem(c, favorite.ID)
		if err != nil {
			return nil, fmt.Errorf("Method:favorites.service.FindAllFavorites: %w", err)
		}
		var coverurl string
		resp = append(resp, modelsFavorites.FavoritesResp{
			ID:        favorite.ID,
			Name:      favorite.Name,
			IsPublic:  favorite.IsPublic,
			ItemCount: count,
			CoverURL:  coverurl,
		})
	}
	return resp, nil
}

// 获取收藏夹详情
func (s *Service) GetFavoriteDetail(c context.Context, userID, favoriteid uint, page, pageSize int) (*modelsFavorites.FavoriteDetailResp, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 64 {
		pageSize = 16
	}
	favorite, err := s.repo.FindByFavoritesID(c, favoriteid)
	if err != nil {
		return nil, fmt.Errorf("Method:favorite.service.GetFavoriteDetail: %w", err)
	}
	if favorite == nil {
		return nil, fmt.Errorf("Method:favorite.service.GetFavoriteDetail: %w", codeErrors.ErrFavoriteNotFound)
	}
	if favorite.IsPublic == 0 && favorite.UserID != userID {
		return nil, fmt.Errorf("Method:favorite.service.GetFavoriteDetail: %w", codeErrors.ErrFavoriteForbidden)
	}
	videos, count, err := s.repo.FindFavoriteItems(c, favoriteid, page, pageSize)
	if err != nil {
		return nil, fmt.Errorf("Method:favorite.service.GetFavoriteDetail: %w", err)
	}

	items := make([]modelsVideo.VideoResp, len(videos))
	for i, videoResp := range videos {
		items[i] = modelsVideo.VideoResp{
			ID:          videoResp.ID,
			Title:       videoResp.Title,
			Description: videoResp.Description,
			CoverURL:    videoResp.CoverURL,
			VideoURL:    videoResp.VideoURL,
			Duration:    videoResp.Duration,
			FileSize:    videoResp.FileSize,
			CategoryID:  videoResp.CategoryID,
			Status:      videoResp.Status,
			Views:       videoResp.Views,
			CreatedAt:   videoResp.CreatedAt,
			UpdatedAt:   videoResp.UpdatedAt,
		}
		if videoResp.User.ID != 0 {
			var avatar string
			if videoResp.User.Avatar != "" {
				avatar = s.storage.GetObjectURL(videoResp.User.Avatar)
			}
			items[i].User = &modelsUser.UserBrief{
				ID:       videoResp.User.ID,
				Username: videoResp.User.Username,
				Nickname: videoResp.User.Nickname,
				Avatar:   avatar,
			}
		}
		items[i].CoverURL = s.storage.URL(videoResp.CoverURL)
	}

	return &modelsFavorites.FavoriteDetailResp{
		Favorite: modelsFavorites.FavoritesResp{
			ID:       favorite.ID,
			Name:     favorite.Name,
			IsPublic: favorite.IsPublic,
		},
		Items:    items,
		Total:    count,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

// 切换视频是否收藏
func (s *Service) ToggleVideoFavorite(c context.Context, favoriteid, userid, videoid uint) (*modelsFavorites.FavoriteToggleResp, error) {
	favorite, err := s.repo.FindByFavoritesID(c, favoriteid)
	if err != nil {
		return nil, fmt.Errorf("Method:favorites.service.ToggleVideoFavorite: %w", err)
	}
	if favorite == nil {
		return nil, fmt.Errorf("Method:favorites.service.ToggleVideoFavorite: %w", codeErrors.ErrFavoriteNotFound)
	}
	if favorite.UserID != userid {
		return nil, fmt.Errorf("Method:favorites.service.ToggleVideoFavorite: %w", codeErrors.ErrFavoriteForbidden)
	}

	existing, err := s.repo.FindItem(c, favoriteid, videoid)
	if err != nil {
		return nil, fmt.Errorf("Method:favorites.service.ToggleVideoFavorite: %w", err)
	}
	if existing != nil {
		if err := s.repo.DeleteItem(c, existing.ID); err != nil {
			return nil, fmt.Errorf("Method:favorites.service.ToggleVideoFavorite: %w", err)
		}
		return &modelsFavorites.FavoriteToggleResp{Favorited: false}, nil
	}

	if err := s.repo.CreateItem(c, &modelsFavorites.FavoritesItem{
		FavoritesID: favoriteid,
		VideoID:     videoid,
	}); err != nil {
		return nil, fmt.Errorf("Method:favorites.service.ToggleVideoFavorite: %w", err)
	}
	return &modelsFavorites.FavoriteToggleResp{Favorited: true}, nil
}

// ============================复用逻辑=========================
func (s *Service) CreateDefaultFavorite(c context.Context, userid uint) error {
	//检测是否存在默认收藏夹
	existing, err := s.repo.FindDefaultFavorite(c, userid)
	if existing != nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("Method:favorites.service.CreateDefaultFavorite: %w", err)
	}

	//不存在则创建默认收藏夹
	favorite := &modelsFavorites.Favorites{
		UserID:   userid,
		Name:     "默认收藏夹",
		IsPublic: 1,
	}

	if err := s.repo.Create(c, favorite); err != nil {
		return fmt.Errorf("Method:favorites.service.CreateDefaultFavorite: %w", err)
	}
	return nil
}
