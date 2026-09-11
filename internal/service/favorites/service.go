package favorites

import (
	modelsFavorites "LikeBili/internal/models/favorites"
	repositoryFavorites "LikeBili/internal/repository/favorites"
	codeErrors "LikeBili/pkg/errors"
	"LikeBili/pkg/storage"
	"LikeBili/pkg/toresp"
	"context"
	"fmt"
	"strings"
	"time"
)

// 视频可见性判定常量，与 video 模块的模型约定保持一致：
//
//	Status：1 待审核、2 审核成功、3 审核失败
//	ViewStatus（*uint8）：1 公开、2 私密
//
// 收藏夹展示只放行"审核通过 + 公开"的视频，其余一律按失效占位处理。
const (
	videoStatusPassed = 2 // 审核成功
	viewStatusPublic  = 1 // 公开
)

type Service struct {
	repo    *repositoryFavorites.Repository // 收藏夹数据访问层
	storage *storage.MinIO                  // 收藏夹封面：需现签 1 小时预签名 URL（区别于 toresp 的永久公开 URL）
	toresp  *toresp.VideoRespBuilder        // 视频 → VideoResp 转换器：复用全站 URL 拼接规则
}

// NewService 构造收藏夹服务。
//   - storage：收藏夹封面需要预签名 URL（GetPresignedURL），与 toresp 的永久公开 URL 语义不同，故单独保留
//   - toresp：视频条目转换统一走全站转换器，避免本模块另写一套字段映射
func NewService(repo *repositoryFavorites.Repository, storage *storage.MinIO, toresp *toresp.VideoRespBuilder) *Service {
	return &Service{repo: repo, storage: storage, toresp: toresp}
}

// ===========================业务代码===========================
// 创建收藏夹
func (s *Service) CreateFavorite(c context.Context, userid uint, req *modelsFavorites.FavoritesReq) (*modelsFavorites.FavoritesResp, error) {
	// ① 名称归一化：去掉首尾空白后再做保留名判断、查重与落库。
	//    Go 的字符串比较是精确比较，而 MySQL 的 VARCHAR 比较受 collation 影响
	//   （PAD SPACE 会忽略尾部空格），不归一化会让用户建出与"默认收藏夹"等价的名字
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, fmt.Errorf("Method:favorite.service.CreateFavorite: %w",
			codeErrors.Wrap(codeErrors.ErrorBadRequest, codeErrors.BadRequest, "收藏夹名称不能为空"))
	}

	// ② 保留名校验："默认收藏夹"由系统自动创建，不允许用户手动占用，
	//    否则 FindDefaultFavorite 按名称查找会取到用户手建的那个，破坏默认夹语义
	if name == modelsFavorites.DefaultFavoriteName {
		return nil, fmt.Errorf("Method:favorite.service.CreateFavorite: %w", codeErrors.ErrFavoriteNameReserved)
	}

	// ③ 同名查重。先查后插，并发下仍可能插入同名——重名不影响数据正确性，
	//    仅为体验问题，因此不为此加唯一索引（避免存量重名数据迁移失败）
	exists, err := s.repo.ExistsByName(c, userid, name)
	if err != nil {
		return nil, fmt.Errorf("Method:favorite.service.CreateFavorite: %w", err)
	}
	if exists {
		return nil, fmt.Errorf("Method:favorite.service.CreateFavorite: %w", codeErrors.ErrFavoriteNameExists)
	}

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

// FindUserPublicFavorite 查找指定用户的公开收藏夹（他人主页视角）。
// 私密收藏夹（IsPublic=0）在此被过滤掉——与 FindAllFavorites 严格区分，避免私密收藏夹泄露。
func (s *Service) FindUserPublicFavorite(c context.Context, userid uint) ([]modelsFavorites.FavoritesResp, error) {
	favorites, err := s.repo.FindFavoritesByUserID(c, userid)
	if err != nil {
		return nil, fmt.Errorf("Method:favorites.service.FindUserPublicFavorite: %w", err)
	}

	// 只保留公开收藏夹
	public := make([]modelsFavorites.Favorites, 0, len(favorites))
	for _, f := range favorites {
		if f.IsPublic == 0 {
			continue
		}
		public = append(public, f)
	}

	resp, err := s.buildFavoritesResp(c, public)
	if err != nil {
		return nil, fmt.Errorf("Method:favorites.service.FindUserPublicFavorite: %w", err)
	}
	return resp, nil
}

// FindAllFavorites 获取本人全部收藏夹（含私密），若无收藏夹则自动创建默认收藏夹。
// 注意：本方法有副作用（可能创建默认收藏夹），因此只能挂在"需登录的我的收藏夹"接口上。
func (s *Service) FindAllFavorites(c context.Context, userid uint) ([]modelsFavorites.FavoritesResp, error) {
	favorites, err := s.repo.FindFavoritesByUserID(c, userid)
	if err != nil {
		return nil, fmt.Errorf("Method:favorites.service.FindAllFavorites: %w", err)
	}
	if len(favorites) == 0 {
		if err := s.CreateDefaultFavorite(c, userid); err != nil {
			return nil, fmt.Errorf("Method:favorites.service.FindAllFavorites: %w", err)
		}
		// 重新查询，确保返回刚创建的默认收藏夹
		if favorites, err = s.repo.FindFavoritesByUserID(c, userid); err != nil {
			return nil, fmt.Errorf("Method:favorites.service.FindAllFavorites: %w", err)
		}
	}

	resp, err := s.buildFavoritesResp(c, favorites)
	if err != nil {
		return nil, fmt.Errorf("Method:favorites.service.FindAllFavorites: %w", err)
	}
	return resp, nil
}

// buildFavoritesResp 统一组装收藏夹列表响应（条目数 + 封面预签名 URL）。
// 由 FindAllFavorites（本人全部）与 FindUserPublicFavorite（他人公开）复用，避免两处逻辑分叉：
// 修复前 FindAllFavorites 漏查封面，导致"我的收藏夹"列表封面恒为空。
func (s *Service) buildFavoritesResp(c context.Context, favorites []modelsFavorites.Favorites) ([]modelsFavorites.FavoritesResp, error) {
	resp := make([]modelsFavorites.FavoritesResp, 0, len(favorites))
	for _, f := range favorites {
		count, err := s.repo.CountItem(c, f.ID)
		if err != nil {
			return nil, err
		}
		resp = append(resp, modelsFavorites.FavoritesResp{
			ID:        f.ID,
			Name:      f.Name,
			IsPublic:  f.IsPublic,
			ItemCount: count,
			CoverURL:  s.favoriteCoverURL(c, f.ID),
		})
	}
	return resp, nil
}

// favoriteCoverURL 取收藏夹封面的预签名 URL。
// 封面来源为收藏夹内最近一次收藏的视频封面（见 repository.FindItemFirstCover）。
// 无收藏项、取不到封面或签名失败时返回空串——封面属装饰信息，不阻断列表返回。
func (s *Service) favoriteCoverURL(c context.Context, favoriteID uint) string {
	objectKey, err := s.repo.FindItemFirstCover(c, favoriteID)
	if err != nil || objectKey == "" {
		return ""
	}
	presigned, err := s.storage.GetPresignedURL(c, objectKey, time.Hour)
	if err != nil {
		return ""
	}
	return presigned
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
	rows, count, err := s.repo.FindFavoriteItems(c, favoriteid, page, pageSize)
	if err != nil {
		return nil, fmt.Errorf("Method:favorite.service.GetFavoriteDetail: %w", err)
	}

	items := s.buildDetailItems(rows)

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

// buildDetailItems 把仓库条目组装为响应条目，并处理失效占位。
// 失效判定（满足任一即为失效）：
//   - 视频不存在：已软删除或记录缺失（仓库层 Preload 查不到，Video == nil）
//   - 视频存在但不可见：非"审核通过(2)"或非"公开(1)"（待审核/已驳回/已私密）
//
// 失效条目保留 VideoID 与收藏时间、Video 置 nil，仅以 Invalid 标记，
// 且不返回失效原因——避免把 UP 主"设为私密/被驳回"的信息泄露给收藏者。
// 正常条目统一走 toresp.ToVideoResp 转换（封面/头像 URL 规则全站一致）。
func (s *Service) buildDetailItems(rows []repositoryFavorites.Item) []modelsFavorites.FavoriteItemResp {
	items := make([]modelsFavorites.FavoriteItemResp, 0, len(rows))
	for _, it := range rows {
		// ⚠️ ViewStatus 是 *uint8，必须先判 nil 再解引用，否则空指针 panic
		invalid := it.Video == nil || it.Video.Status != videoStatusPassed ||
			it.Video.ViewStatus == nil || *it.Video.ViewStatus != viewStatusPublic

		item := modelsFavorites.FavoriteItemResp{
			VideoID:     it.VideoID,
			Invalid:     invalid,
			FavoritedAt: it.CreatedAt,
		}
		if !invalid {
			// 复用全站转换器：字段映射与封面/头像 URL 规则不再由本模块维护
			// （其内部按 v.User.ID != 0 判断是否带发布者信息）
			item.Video = s.toresp.ToVideoResp(it.Video)
		}
		items = append(items, item)
	}
	return items
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

	//不存在则创建默认收藏夹（名称取常量，与查找/保留名校验共用同一来源）
	favorite := &modelsFavorites.Favorites{
		UserID:   userid,
		Name:     modelsFavorites.DefaultFavoriteName,
		IsPublic: 1,
	}

	if err := s.repo.Create(c, favorite); err != nil {
		return fmt.Errorf("Method:favorites.service.CreateDefaultFavorite: %w", err)
	}
	return nil
}
