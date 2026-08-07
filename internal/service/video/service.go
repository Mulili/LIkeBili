// Package video 提供视频模块的 Service（业务逻辑）层。
// 职责：上传文件到 MinIO → 建数据库记录 → 触发转码。
// 约定：文件校验已在 Handler 完成，这里只接收"校验后的文件流 + 校验结果"。
package video

import (
	"context"
	"fmt"
	"io"
	"time"

	modelsVideo "LikeBili/internal/models/video"
	rpvideo "LikeBili/internal/repository/video"
	codeErrors "LikeBili/pkg/errors"
	"LikeBili/pkg/logger"
	"LikeBili/pkg/storage"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// Service 视频模块的业务服务，持有全部依赖。
type Service struct {
	repo    *rpvideo.Repository // 数据库访问层，Service 不直接碰 db
	rdb     *redis.Client       // Redis 客户端（预留）
	storage *storage.MinIO      // 对象存储客户端

	// transcodePublisher 可选旁路依赖：转码任务发布者（如 MQ）。
	// nil = 未接入 MQ，走进程内降级转码（transcodeLocal）。
	transcodePublisher func(videoID uint) error
	transcodeRunner    func(videoID uint)
}

// UploadVideoInput 上传视频的入参 DTO。
type UploadVideoInput struct {
	UserID      uint // 来自 JWT，不信任前端传的
	Title       string
	Description string
	CategoryID  uint32

	File        io.Reader // 视频文件流
	FileSize    int64
	ContentType string // 嗅探出的真实 MIME，防伪造
	Ext         string // 嗅探出的真实扩展名，拼对象名用

	CoverFile        io.Reader // 封面图流，可选
	CoverSize        int64
	CoverContentType string
	CoverExt         string
}

// Option 可选依赖注入器（functional options 模式）。
type Option func(*Service)

// WithTranscodePublisher 注入转码发布者（MQ）。不调用 = 无 MQ，走进程内降级转码。
func WithTranscodePublisher(fn func(videoID uint) error) Option {
	return func(s *Service) { s.transcodePublisher = fn }
}

func WithTranscodeRunner(fn func(videoID uint)) Option {
	return func(s *Service) { s.transcodeRunner = fn }
}

// NewService 构造 Service。必传依赖走位置参数，可选依赖走 Option。
func NewService(repo *rpvideo.Repository, rdb *redis.Client, storage *storage.MinIO, opts ...Option) *Service {
	s := &Service{repo: repo, rdb: rdb, storage: storage}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// UploadVideo 上传视频的完整业务链路：
// 生成唯一对象名 → 传视频到 MinIO → 传封面（可选，失败不阻塞）→ 建数据库记录
// → DB 失败则回滚删 MinIO 文件 → 触发转码 → 返回响应 DTO。
func (s *Service) UploadVideo(c context.Context, input *UploadVideoInput) (*modelsVideo.VideoResp, error) {
	// ① 对象名：videos/{userID}/{纳秒}.{扩展名}，用户ID分目录 + 纳秒时间戳保证唯一
	objkey := fmt.Sprintf("videos/%d/%d.%s", input.UserID, time.Now().UnixNano(), input.Ext)

	// ② 上传视频流到 MinIO（流式上传，不落本地盘）
	if err := s.storage.UploadVideo(c, objkey, input.File, input.FileSize, input.ContentType); err != nil {
		return nil, fmt.Errorf("Method:video.Service.UploadVideo: %w",
			codeErrors.Wrap(err, codeErrors.CodeFileUploadFailed, "视频上传失败"))
	}

	// ③ 封面（可选）：失败只记日志，不阻塞视频主流程
	var coverObjKey string
	if input.CoverFile != nil {
		coverObjKey = fmt.Sprintf("videos/%d/%d-cover.%s", input.UserID, time.Now().UnixNano(), input.CoverExt)
		if err := s.storage.UploadVideo(c, coverObjKey, input.CoverFile, input.CoverSize, input.CoverContentType); err != nil {
			logger.Warn("封面上传失败，忽略", zap.String("operation", "UploadVideo"), zap.Error(err))
			coverObjKey = "" // 置空，避免库里写入不存在的对象名
		}
	}

	// ④ 建数据库记录，Status=1 待审核
	video := &modelsVideo.Video{
		UserID:      input.UserID,
		Title:       input.Title,
		Description: input.Description,
		CoverURL:    coverObjKey, // 库里存对象名，返回前端时才拼完整 URL
		VideoURL:    objkey,
		FileSize:    uint64(input.FileSize),
		CategoryID:  input.CategoryID,
		Status:      1,
	}
	if err := s.repo.Create(c, video); err != nil {
		// ⑤ 回滚：DB 是唯一可信源，写入失败必须删掉刚上传的 MinIO 文件，防孤儿对象
		_ = s.storage.Delete(c, objkey)
		if coverObjKey != "" {
			_ = s.storage.Delete(c, coverObjKey)
		}
		return nil, fmt.Errorf("Method:video.Service.UploadVideo: %w", err)
	}

	// ⑥ 异步触发转码，不阻塞上传响应
	s.triggerTranscode(video.ID)

	// ⑦ 对象名 → 完整可访问 URL
	return s.toVideoResp(video), nil
}

// triggerTranscode 触发转码任务。
// 有 MQ 发布者 → 发布任务（失败则降级本地）；没有 MQ → 直接本地协程。
func (s *Service) triggerTranscode(videoID uint) {
	if s.transcodePublisher != nil {
		go func() {
			if err := s.transcodePublisher(videoID); err != nil {
				logger.Warn("MQ发布转码任务失败，降级本地转码", zap.Uint("video_id", videoID), zap.Error(err))
				s.transcodeLocal(videoID)
			}
		}()
		return
	}
	s.transcodeLocal(videoID)
}

// transcodeLocal 本地降级转码（占位实现）。
func (s *Service) transcodeLocal(videoID uint) {
	if s.transcodeRunner == nil {
		logger.Warn("未配置本地转码执行器", zap.Uint("video_id", videoID))
		return
	}
	s.transcodeRunner(videoID)
}

// toVideoResp 把数据库模型转成响应 DTO。库里存对象名，必须经 storage.URL 拼成完整 URL。
func (s *Service) toVideoResp(v *modelsVideo.Video) *modelsVideo.VideoResp {
	return &modelsVideo.VideoResp{
		ID:          v.ID,
		Title:       v.Title,
		Description: v.Description,
		CoverURL:    s.storage.URL(v.CoverURL),
		VideoURL:    s.storage.URL(v.VideoURL),
		Duration:    v.Duration,
		FileSize:    v.FileSize,
		CategoryID:  v.CategoryID,
		Status:      v.Status,
		Views:       v.Views,
		CreatedAt:   v.CreatedAt,
		UpdatedAt:   v.UpdatedAt,
	}
}
