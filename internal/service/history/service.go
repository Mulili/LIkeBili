package history

import (
	modelsHistory "LikeBili/internal/models/history"
	repohistory "LikeBili/internal/repository/history"
	repovideo "LikeBili/internal/repository/video"
	"context"
	"fmt"
	"time"
)

type Service struct {
	repo      *repohistory.Repository
	videorepo *repovideo.Repository
}

func NewService(repo *repohistory.Repository, videorepo *repovideo.Repository) *Service {
	return &Service{repo: repo, videorepo: videorepo}
}

func (s *Service) CreateOrUpdateHistory(c context.Context, userID uint, req modelsHistory.CreateHistoryReq) error {
	now := time.Now()
	h := &modelsHistory.UserHistory{
		UserID:    userID,
		VideoID:   req.VideoID,
		Progress:  req.Progress,
		WatchedAt: now,
	}
	if err := s.repo.CreateOrUpdateHistory(c, h); err != nil {
		return fmt.Errorf("history.service.CreateOrUpdateHistory: %w", err)
	}

	if req.Progress > 0 && req.Progress < 24*3600 {
		_ = s.videorepo.UpdateDuration(c, req.VideoID, uint(req.Duration))
	}

	return nil
}

func (s *Service) List(c context.Context, userID uint, page, pageSize int) (*modelsHistory.HistoryListResp, error) {
	//下一步进度，写完历史记录和关注
	return nil, nil
}
