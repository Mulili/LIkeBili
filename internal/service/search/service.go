package search

import (
	modelsVideo "LikeBili/internal/models/video"
	reposearch "LikeBili/internal/repository/search"
	repovideo "LikeBili/internal/repository/video"
	"LikeBili/pkg/toresp"
	"context"
	"fmt"
	"strings"
)

const (
	// maxKeywordRunes 关键词最大长度（按字符计），超出直接截断，避免超长输入打到 DB
	maxKeywordRunes = 50
	// maxPage 分页上限：全文检索的 OFFSET 深分页代价高，限制页数防止被刷
	maxPage = 100
	// defaultPageSize / maxPageSize 分页大小口径，与项目其它列表保持一致
	defaultPageSize = 16
	maxPageSize     = 50
)

// Service 搜索服务：基于 video_search 的 ngram 全文检索 + 分类兜底。
type Service struct {
	searchRepo *reposearch.Repository // 全文检索（video_search）
	videoRepo  *repovideo.Repository  // 回表取详情 / 分类字典查询
	toresp     *toresp.VideoRespBuilder
}

// NewService 构造搜索服务，注入检索仓储、视频仓储与 DTO 转换器。
func NewService(searchRepo *reposearch.Repository, videoRepo *repovideo.Repository, toresp *toresp.VideoRespBuilder) *Service {
	return &Service{searchRepo: searchRepo, videoRepo: videoRepo, toresp: toresp}
}

// SearchVideos 搜索公开视频。
// 分支策略：
//   - 关键词 ≥2 字：走 ngram 全文检索（标题/简介/UP主昵称/分类名），按相关度或最新排序
//   - 关键词 1 字：ngram 按 2 字切分，单字无法命中全文索引 → 降级为"按分类名模糊匹配"，
//     返回第一个命中分类下的视频
//   - 关键词为空：直接返回空结果，不查库
func (s *Service) SearchVideos(c context.Context, keyword string, categoryID uint, order string, page, pageSize int) (*modelsVideo.ListVideo, error) {
	// ① 参数防御：分页钳制
	keyword = strings.TrimSpace(keyword)
	page, pageSize = normalizePage(page, pageSize)

	// ② 关键词截断（按字符计，避免超长输入）
	runes := []rune(keyword)
	if len(runes) > maxKeywordRunes {
		runes = runes[:maxKeywordRunes]
		keyword = string(runes)
	}

	// ③ 空关键词：直接返回空结果
	if keyword == "" {
		return emptyResult(page, pageSize), nil
	}

	// ④ 单字兜底：交给分类名模糊匹配
	if len(runes) < 2 {
		return s.searchByCategory(c, keyword, page, pageSize)
	}

	// ⑤ 全文检索：先取命中（带分数、已按序），再回表取详情
	hits, total, err := s.searchRepo.SearchVideos(c, keyword, categoryID, order, page, pageSize)
	if err != nil {
		return nil, fmt.Errorf("Method:search.Service.SearchVideos: %w", err)
	}
	if len(hits) == 0 {
		return &modelsVideo.ListVideo{
			List:     []modelsVideo.VideoResp{},
			Total:    uint(total),
			Page:     uint16(page),
			PageSize: uint16(pageSize),
		}, nil
	}

	ids := make([]uint, len(hits))
	for i, hit := range hits {
		ids[i] = hit.VideoID
	}

	// ⑥ 回表 + 二次可见性校验：检索表的冗余状态可能滞后，这里再按 videos 表实际状态过滤
	videos, err := s.videoRepo.FindPublicByIDs(c, ids)
	if err != nil {
		return nil, fmt.Errorf("Method:search.Service.SearchVideos: %w", err)
	}
	videoMap := make(map[uint]modelsVideo.Video, len(videos))
	for _, v := range videos {
		videoMap[v.ID] = v
	}

	// ⑦ 按检索顺序（相关度/时间）重排，保证返回顺序与 score 一致
	items := make([]modelsVideo.VideoResp, 0, len(hits))
	for _, hit := range hits {
		v, ok := videoMap[hit.VideoID]
		if !ok {
			continue // 二次校验未通过（视频状态已变更）：跳过该条
		}
		items = append(items, *s.toresp.ToVideoResp(&v))
	}

	return &modelsVideo.ListVideo{
		List:     items,
		Total:    uint(total),
		Page:     uint16(page),
		PageSize: uint16(pageSize),
	}, nil
}

// searchByCategory 单字搜索兜底：把关键词当分类名做模糊匹配，命中第一个分类后返回该分类的视频。
// 之所以不直接 LIKE 搜标题：中缀模糊查询无法利用索引，大表上会退化为全表扫描。
func (s *Service) searchByCategory(c context.Context, keyword string, page, pageSize int) (*modelsVideo.ListVideo, error) {
	category, err := s.videoRepo.FindFirstCategoryLikeName(c, keyword)
	if err != nil {
		return nil, fmt.Errorf("Method:search.Service.searchByCategory: %w", err)
	}
	if category == nil {
		// 未命中任何分类：返回空结果（前端提示"没有找到相关内容"）
		return emptyResult(page, pageSize), nil
	}

	// 复用公开列表查询（status=2 + view_status=1 + 分类过滤，按创建时间倒序）
	videos, total, err := s.videoRepo.FindList(c, uint(page), uint(pageSize), category.ID)
	if err != nil {
		return nil, fmt.Errorf("Method:search.Service.searchByCategory: %w", err)
	}

	items := make([]modelsVideo.VideoResp, 0, len(videos))
	for i := range videos {
		items = append(items, *s.toresp.ToVideoResp(&videos[i]))
	}
	return &modelsVideo.ListVideo{
		List:     items,
		Total:    uint(total),
		Page:     uint16(page),
		PageSize: uint16(pageSize),
	}, nil
}

// emptyResult 构造空结果（List 保持非 nil 空切片，前端可直接遍历）。
func emptyResult(page, pageSize int) *modelsVideo.ListVideo {
	return &modelsVideo.ListVideo{
		List:     []modelsVideo.VideoResp{},
		Total:    0,
		Page:     uint16(page),
		PageSize: uint16(pageSize),
	}
}

// normalizePage 分页参数防御：page 限制在 [1, maxPage]，pageSize 限制在 [1, maxPageSize]。
func normalizePage(page, pageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if page > maxPage {
		page = maxPage
	}
	if pageSize < 1 || pageSize > maxPageSize {
		pageSize = defaultPageSize
	}
	return page, pageSize
}
