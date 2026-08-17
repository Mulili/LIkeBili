// Package admin 提供管理员审核视频的业务逻辑层（Service）。
// 角色约定（与 User.Role 字段一致）：
//   - role=1 普通用户：公开注册，无管理权限
//   - role=2 审核管理员：负责审核视频（通过/驳回），由超管创建
//   - role=3 超管：只负责创建审核管理员，不参与视频审核
//
// 调用链：Handler → Service → Repository（GORM）。
package admin

import (
	modelsReview "LikeBili/internal/models/review"
	modelsVideo "LikeBili/internal/models/video"
	"LikeBili/internal/repository/admin"
	rpvideo "LikeBili/internal/repository/video"
	codeErrors "LikeBili/pkg/errors"
	"LikeBili/pkg/logger"
	"LikeBili/pkg/storage"
	"LikeBili/pkg/toresp"
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
)

// Service 封装管理员审核相关的业务逻辑。
// 依赖：admin 仓库（审核数据访问）、video 仓库（软删超期视频硬删除）、
// 对象存储（审核观看预签名 + 硬删时删 MinIO）、DTO 转换器。
type Service struct {
	repo      *admin.Repository        // 审核数据访问层：待审核查询、状态更新、审核流水
	videoRepo *rpvideo.Repository      // 视频数据访问层：软删超期视频查询与事务硬删
	storage   *storage.MinIO           // 对象存储：审核时对视频源文件现签预签名 URL；清理时删 MinIO 对象
	toresp    *toresp.VideoRespBuilder // 视频 DTO 转换器：列表/详情页展示用
}

// NewService 创建管理员审核服务实例。
// 通过构造函数注入全部依赖（依赖倒置），便于单元测试替换 mock。
func NewService(repo *admin.Repository, videoRepo *rpvideo.Repository, storage *storage.MinIO, toresp *toresp.VideoRespBuilder) *Service {
	return &Service{
		repo:      repo,
		videoRepo: videoRepo,
		storage:   storage,
		toresp:    toresp,
	}
}

// ListPendingVideos 分页拉取待审核（status=1）的视频列表，供审核后台展示。
// 返回按发布时间倒序的公开结构（ListVideo），total 为待审核总数。
func (s *Service) ListPendingVideos(c context.Context, page, pageSize int) (*modelsVideo.ListVideo, error) {
	videos, total, err := s.repo.ListPending(c, page, pageSize)
	if err != nil {
		return nil, fmt.Errorf("Method:admin.service.ListPendingVideos: %w", err)
	}
	// 逐个转 DTO：repo 已 Preload User/Category，转换器可直接取用
	items := make([]modelsVideo.VideoResp, 0, len(videos))
	for _, v := range videos {
		items = append(items, *s.toresp.ToVideoResp(v))
	}
	return &modelsVideo.ListVideo{
		List:     items,
		Total:    uint(total),
		Page:     uint16(page),
		PageSize: uint16(pageSize),
	}, nil
}

// GetReviewPlayURL 获取管理员审核专用的播放地址。
// 与公开播放接口的区别：不过滤审核状态（待审核/被驳回的视频管理员都要能看），
// 直接对上传的源文件现签 1 小时预签名 URL（源文件上传即存在，无需等转码完成）。
func (s *Service) GetReviewPlayURL(c context.Context, videoID uint) (string, error) {
	video, err := s.repo.GetByID(c, videoID)
	if err != nil {
		return "", fmt.Errorf("Method:admin.service.GetReviewPlayURL: %w", err)
	}
	if video == nil {
		return "", fmt.Errorf("Method:admin.service.GetReviewPlayURL: %w", codeErrors.ErrVideoNotFound)
	}
	url, err := s.storage.GetPresignedURL(c, video.VideoURL, time.Hour)
	if err != nil {
		return "", fmt.Errorf("Method:admin.service.GetReviewPlayURL: %w", err)
	}
	return url, nil
}

// GetLatestReview 取某视频最新一条审核记录，转成响应 DTO。
// 用途：作者端展示"审核失败原因"（视频 status=3 时前端调详情接口取驳回意见）。
func (s *Service) GetLatestReview(c context.Context, videoID uint) (*modelsReview.ReviewResp, error) {
	review, err := s.repo.GetLatestReview(c, videoID)
	if err != nil {
		return nil, fmt.Errorf("Method:admin.service.GetLatestReview: %w", err)
	}
	if review == nil {
		return nil, nil // 尚无审核记录，不算错误
	}
	return &modelsReview.ReviewResp{
		ID:        review.ID,
		VideoID:   review.VideoID,
		AdminID:   review.AdminID,
		Result:    review.Result,
		Reason:    review.Reason,
		CreatedAt: review.CreatedAt,
	}, nil
}

// GetVideoDetail 审核详情：视频完整信息 + 全部审核历史（倒序）。
// 供审核后台打开单个视频时展示；视频不存在返回 ErrVideoNotFound。
func (s *Service) GetVideoDetail(c context.Context, videoID uint) (*modelsReview.VideoDetailResp, error) {
	// ① 查视频（不过滤审核状态，待审核/已审核都要能看）
	video, err := s.repo.GetByID(c, videoID)
	if err != nil {
		return nil, fmt.Errorf("Method:admin.service.GetVideoDetail: %w", err)
	}
	if video == nil {
		return nil, fmt.Errorf("Method:admin.service.GetVideoDetail: %w", codeErrors.ErrVideoNotFound)
	}
	// ② 查完整审核历史（按时间倒序）
	reviews, err := s.repo.ListReviews(c, videoID)
	if err != nil {
		return nil, fmt.Errorf("Method:admin.service.GetVideoDetail: %w", err)
	}
	// ③ 组装响应：视频 DTO + 审核记录 DTO 列表
	resp := &modelsReview.VideoDetailResp{
		Video:   s.toresp.ToVideoResp(video),
		Reviews: make([]modelsReview.ReviewResp, 0, len(reviews)),
	}
	for _, r := range reviews {
		resp.Reviews = append(resp.Reviews, modelsReview.ReviewResp{
			ID:        r.ID,
			VideoID:   r.VideoID,
			AdminID:   r.AdminID,
			Result:    r.Result,
			Reason:    r.Reason,
			CreatedAt: r.CreatedAt,
		})
	}
	return resp, nil
}

// Review 执行一次审核：通过（result=2）或驳回（result=3）。
// 业务规则：
//  1. result 仅允许 2/3（与 video_reviews.Result、Video.Status 对齐）
//  2. 视频不存在 → ErrVideoNotFound
//  3. 幂等：视频已非待审核状态（status!=1）→ 409"该视频已审核"，拒绝重复审核
//  4. 驳回必须填写原因（reason 非空），否则参数错误
//  5. 状态变更 + 审核流水在 repository 的事务内一次性完成
func (s *Service) Review(c context.Context, adminID, videoID uint, result uint8, reason string) error {
	// ① 校验审核结果取值合法
	if result != modelsReview.ResultApprove && result != modelsReview.ResultReject {
		return fmt.Errorf("Method:admin.service.Review: %w", codeErrors.ErrCodeInvalid)
	}
	// ② 查视频存在性（不过滤状态）
	video, err := s.repo.GetByID(c, videoID)
	if err != nil {
		return fmt.Errorf("Method:admin.service.Review: %w", err)
	}
	if video == nil {
		return fmt.Errorf("Method:admin.service.Review: %w", codeErrors.ErrVideoNotFound)
	}
	// ③ 幂等校验：只有待审核（status=1）才能被审核
	if video.Status != 1 {
		return fmt.Errorf("Method:admin.service.Review: %w",
			codeErrors.New(codeErrors.Conflict, "该视频已审核"))
	}
	// ④ 驳回必须给出原因，否则前端无法向作者展示驳回意见
	if result == modelsReview.ResultReject && strings.TrimSpace(reason) == "" {
		return fmt.Errorf("Method:admin.service.Review: %w", codeErrors.ErrCodeInvalid)
	}
	// ⑤ 事务：更新视频状态 + 写入审核流水
	if err := s.repo.ReviewTx(c, videoID, adminID, result, reason); err != nil {
		return fmt.Errorf("Method:admin.service.Review: %w", err)
	}
	return nil
}

// CleanupExpired 硬删除软删超过 days 天的视频（回收站清理，仅审核管理员可调用）。
// 流程：查询软删超期视频 → 收集 MinIO 删除目标 → DB 事务硬删（含级联关联表）→ 逐个删 MinIO。
// 业务规则：
//  1. days 钳制：<1 → 30（防误传 0/负数一次性清空整个回收站）
//  2. 收集 MinIO 目标必须在 DB 删除之前完成（否则 quality 记录删了就拿不到对象名）
//  3. MinIO 删除失败只记日志不阻断（留下孤儿对象，可后续手工清理，避免出现 404 坏链）
func (s *Service) CleanupExpired(c context.Context, adminID uint, days int) (int, error) {
	// ① 参数钳制：最坏情况也只清理 30 天前的软删视频
	if days < 1 {
		days = 30
	}
	// ② 查软删超过 days 天的视频
	before := time.Now().AddDate(0, 0, -days)
	videos, err := s.videoRepo.ListDeleteBefore(c, before)
	if err != nil {
		return 0, fmt.Errorf("Method:admin.service.CleanupExpired: %w", err)
	}
	if len(videos) == 0 {
		return 0, nil // 没有可清理的，直接返回
	}
	// ③ 收集 MinIO 删除目标（必须在 DB 删除前，见规则 2）
	ids := make([]uint, 0, len(videos))
	var fileObjs []string // 单个对象（源文件/封面）：用 Delete 精确删，不能按目录删（同目录还有别的视频）
	var dirObjs []string  // 转码档位目录（如 "videos/5/720p/"）：用 DeletePrefix 删整目录
	for _, v := range videos {
		ids = append(ids, v.ID)
		if v.VideoURL != "" {
			fileObjs = append(fileObjs, v.VideoURL)
		}
		if v.CoverURL != "" {
			fileObjs = append(fileObjs, v.CoverURL)
		}
	}
	qualityObjs, err := s.videoRepo.ListQualityObjects(c, ids)
	if err != nil {
		return 0, fmt.Errorf("Method:admin.service.CleanupExpired: %w", err)
	}
	for _, obj := range qualityObjs {
		// m3u8 对象名形如 "videos/5/720p/index.m3u8" → 取目录前缀 "videos/5/720p/"
		if idx := strings.LastIndex(obj, "/"); idx > 0 {
			dirObjs = append(dirObjs, obj[:idx+1])
		}
	}
	// ④ DB 事务硬删（视频本体 + 4 张关联表级联删除）
	if err := s.videoRepo.HardDeleteExpiredTx(c, ids); err != nil {
		return 0, fmt.Errorf("Method:admin.service.CleanupExpired: %w", err)
	}
	// ⑤ 删 MinIO：失败只记日志，不阻断主流程（孤儿对象可后续手工清理）
	for _, obj := range fileObjs {
		if err := s.storage.Delete(c, obj); err != nil {
			logger.Warn("MinIO 文件删除失败", zap.String("operation", "CleanupExpired"),
				zap.String("object", obj), zap.Error(err))
		}
	}
	for _, dir := range dirObjs {
		if err := s.storage.DeletePrefix(c, dir); err != nil {
			logger.Warn("MinIO 目录删除失败", zap.String("operation", "CleanupExpired"),
				zap.String("prefix", dir), zap.Error(err))
		}
	}
	// ⑥ 审计日志：谁触发、时限、清理数量
	logger.Info("管理员触发过期视频清理完成",
		zap.Uint("admin_id", adminID), zap.Int("days", days), zap.Int("count", len(videos)))
	return len(videos), nil
}
