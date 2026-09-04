package history

import (
	modelsHistory "LikeBili/internal/models/history"
	repohistory "LikeBili/internal/repository/history"
	repovideo "LikeBili/internal/repository/video"
	"LikeBili/pkg/toresp"
	"context"
	"fmt"
	"time"
)

type Service struct {
	repo      *repohistory.Repository  // 历史表访问层
	videorepo *repovideo.Repository    // 视频表访问层：观看时回填视频真实时长
	toresp    *toresp.VideoRespBuilder // 视频响应 DTO 转换器：统一封面/头像 URL 拼接规则
}

// NewService 构造历史服务，注入历史仓储、视频仓储与视频 DTO 转换器。
func NewService(repo *repohistory.Repository, videorepo *repovideo.Repository, toresp *toresp.VideoRespBuilder) *Service {
	return &Service{repo: repo, videorepo: videorepo, toresp: toresp}
}

// CreateOrUpdateHistory 上报一次观看进度（创建或更新观看历史）。
// 同一用户对同一视频只有一条记录：存在则刷新进度与观看时间，不存在则新建。
// 附带副作用：当视频时长尚未解析（videos.duration=0）时，用播放器上报的真实时长回填视频表。
func (s *Service) CreateOrUpdateHistory(c context.Context, userID uint, req modelsHistory.CreateHistoryReq) error {
	// ① 服务层统一写入当前时刻，保证 watched_at 不会是零值时间戳（0001-01-01）
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

	// ② 仅在"确实有播放进度"时回填时长：progress 限制在 (0, 24h) 排除异常上报。
	// UpdateDuration 内部 WHERE duration = 0，只回填一次；失败不影响历史主流程，静默容忍
	if req.Progress > 0 && req.Progress < 24*3600 {
		_ = s.videorepo.UpdateDuration(c, req.VideoID, uint(req.Duration))
	}

	return nil
}

// List 分页查询当前用户的观看历史，倒序返回（最近观看在前）。
// 每条记录内嵌完整视频信息（含发布者），供前端列表直接渲染。
func (s *Service) List(c context.Context, userID uint, page, pageSize int) (*modelsHistory.HistoryListResp, error) {
	// ① 分页参数防御：page 最小 1；pageSize 越界（<1 或 >50）回退默认 16，防超大页拖垮 DB
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 50 {
		pageSize = 16
	}

	// ② 查历史列表（仓储层已 Preload 视频与发布者）与总数
	list, total, err := s.repo.FindListHistory(c, userID, page, pageSize)
	if err != nil {
		return nil, fmt.Errorf("history.service.List: %w", err)
	}

	// ③ 组装响应：模型 → DTO，统一走 toresp 拼封面/头像公开 URL
	items := make([]modelsHistory.HistoryItemResp, 0, len(*list))
	for i := range *list {
		h := &(*list)[i]
		// 视频被删除/下架时，Preload 按默认 scope 过滤后 Video.ID 为 0，
		// 该条历史已无展示意义，跳过不返回给前端
		if h.Video.ID == 0 {
			continue
		}
		items = append(items, modelsHistory.HistoryItemResp{
			Video:     *s.toresp.ToVideoResp(&h.Video),
			Progress:  h.Progress,
			WatchedAt: h.WatchedAt,
		})
	}

	return &modelsHistory.HistoryListResp{
		Items:    items,
		Total:    total,
		Page:     uint16(page),
		PageSize: uint16(pageSize),
	}, nil
}
