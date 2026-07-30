package favorites

import (
	// modelsUser"LikeBili/internal/models/user"
	modelsFavorites "LikeBili/internal/models/favorites"
	repositoryFavorites "LikeBili/internal/repository/favorites"
	"context"
	"fmt"
)

type Service struct {
	repo *repositoryFavorites.Repository
}

func NewService(repo *repositoryFavorites.Repository) *Service {
	return &Service{repo: repo}
}

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
func (s *Service) FindUserPublicFavorite(c context.Context, userid uint) ([]modelsFavorites.Favorites, error) {
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

		count, _ := s.repo.CountItem(c, f.ID)

		var coverurl string
		if url, err := s.repo.FindItemFirstCover(c, f.ID); err != nil {

		}

	}
}
