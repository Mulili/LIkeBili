// Package video 提供视频模块的 Service（业务逻辑）层。
// 职责：上传文件到 MinIO → 建数据库记录 → 触发转码。
// 约定：文件校验已在 Handler 完成，这里只接收"校验后的文件流 + 校验结果"。
package video

import (
	"context"
	"fmt"
	"io"
	"time"

	"LikeBili/internal/models/transcode"
	modelsVideo "LikeBili/internal/models/video"
	rpvideo "LikeBili/internal/repository/video"
	codeErrors "LikeBili/pkg/errors"
	"LikeBili/pkg/logger"
	"LikeBili/pkg/storage"
	"LikeBili/pkg/toresp"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// Service 视频模块的业务服务，持有全部依赖。
type Service struct {
	repo    *rpvideo.Repository      // 数据库访问层，Service 不直接碰 db
	rdb     *redis.Client            // Redis 客户端（预留）
	storage *storage.MinIO           // 对象存储客户端
	toresp  *toresp.VideoRespBuilder // 视频响应 DTO 转换器（跨模块统一出口，构造时注入）

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
func NewService(repo *rpvideo.Repository, rdb *redis.Client, storage *storage.MinIO, toresp *toresp.VideoRespBuilder, opts ...Option) *Service {
	s := &Service{repo: repo, rdb: rdb, storage: storage, toresp: toresp}
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

	// ⑦ 返回 DTO：封面/头像已由 toresp.ToVideoResp 统一拼公开 URL；
	// DTO 不含播放地址（Status=1 未转码完，前端点播放走 GetPresignedUrl 现签）
	return s.toresp.ToVideoResp(video), nil
}
func (s *Service) GetVideo(c context.Context, videoID, userID uint) (*modelsVideo.VideoResp, error) {
	video, err := s.FindVideoAndForbidden(c, videoID, userID)
	if err != nil {
		return nil, err
	}
	if err := s.repo.IncrementViews(c, videoID); err != nil {
		logger.Warn("自增播放量失败", zap.Uint("video_id", videoID), zap.Error(err))
	}
	video.Views++

	// 封面/头像由 toresp 统一拼公开 URL，播放地址由 GetPresignedUrl 现签
	return s.toresp.ToVideoResp(video), nil
}

// GetPresignedUrl 生成视频播放地址：1 小时有效的预签名 URL。
// 何时调用：前端点击播放时单独请求（详情接口不返回播放地址，防爬虫抓取）。
// 校验与 GetVideo 一致：视频必须存在且可见（Status=2 公开，或作者本人可看）。
// 参数来源：videoID/author 来自路由 + JWT；签名目标 video.VideoURL 是库里存的源文件对象名。
func (s *Service) GetPresignedUrl(c context.Context, videoID, userID uint) (string, error) {
	video, err := s.FindVideoAndForbidden(c, videoID, userID)
	if err != nil {
		return "", err
	}
	url, err := s.storage.GetPresignedURL(c, video.VideoURL, time.Hour)
	if err != nil {
		return "", fmt.Errorf("Method:video.service.GetPresignedUrl: %w", err)
	}
	return url, nil
}

// UpdateVideo 更新视频信息（作者专属，写操作）。
// 可更新字段：Title / Description / CategoryID / ViewStatus（1公开 2私密）。
// 变更 ViewStatus=1（公开）时：先校验视频已通过审核，再查转码任务——
// 任务丢失则触发重试、转码失败或未完成均不阻塞显示，先以源 MP4 播放；
// 转码是后台异步任务，完成后播放自动升级为 HLS 分片。
// ViewStatus=2（私密）直接生效；非法值返回 ErrCodeInvalid。
// 最终统一落库并返回最新 DTO。
func (s *Service) UpdateVideo(c context.Context, videoID, userID uint, upReq *modelsVideo.UpdateVideoReq) (*modelsVideo.VideoResp, error) {
	// ① 写操作前置校验：视频必须存在，且必须是本视频作者（非作者 → ErrVideoForbidden）
	video, err := s.AssessVideoAndAuthor(c, videoID, userID)
	if err != nil {
		return nil, err
	}
	// ② 可选字段逐个覆盖：字段为 nil 表示前端未传，跳过不覆盖，避免误清空已有值
	if upReq.Title != nil {
		video.Title = *upReq.Title
	}
	if upReq.Description != nil {
		video.Description = *upReq.Description
	}
	if upReq.CategoryID != nil {
		video.CategoryID = *upReq.CategoryID
	}
	// ③ 可视状态（公开/私密）变更，走状态校验分支
	if upReq.ViewStatus != nil {
		switch *upReq.ViewStatus {
		case 1: // 显示（公开）
			// 显示前提：视频已通过审核（Status=2）
			if video.Status != 2 {
				return nil, fmt.Errorf("Method:video.service.UpdateVideo: %w", codeErrors.ErrVideoNotPass)
			}
			// 查转码任务
			task, err := s.repo.FindTask(c, videoID)
			if err != nil {
				return nil, fmt.Errorf("Method:video.service.UpdateVideo: %w", err)
			}
			// 降级策略：转码未完成不阻塞显示。播放时由 GetPresignedUrl 决定
			//（完成→HLS，未完成/失败→源 mp4 预签名），因此这里一律放行。
			if task == nil {
				// 任务丢失 → 触发重试重建，本次先以源 mp4 发布
				logger.Warn("未查询到转码任务，触发重试", zap.Uint("video_id", videoID))
				s.triggerTranscode(videoID)
			} else if task.Status == transcode.StatusFailed {
				// 转码失败 → 降级播源 mp4，记日志供排查
				logger.Warn("转码失败，降级播放源文件", zap.Uint("video_id", videoID), zap.String("err_message", task.ErrMessage))
			}
			// 待处理/转码中/已完成 均放行
		case 2: // 隐藏（私密）：直接生效
		default:
			return nil, fmt.Errorf("Method:video.service.UpdateVideo: %w", codeErrors.ErrCodeInvalid)
		}
		// DTO 与 model 均为 *uint8，指针直接赋值（含 nil 跳过语义）
		video.ViewStatus = upReq.ViewStatus
	}

	// ④ 落库：Save 全量更新（本方法改什么字段都通过这一个出口写库）
	if err := s.repo.Update(c, video); err != nil {
		return nil, fmt.Errorf("Method:video.service.UpdateVideo: %w", err)
	}

	// ⑤ 返回最新 DTO：封面/头像由 toresp 统一拼公开 URL
	return s.toresp.ToVideoResp(video), nil
}

// ===========================下一步制作删除minio的内容
func (s *Service) DeleteVideo(c context.Context, videoID, userID uint) error {
	video, err := s.AssessVideoAndAuthor(c, videoID, userID)
	if err != nil {
		return err
	}
	if err := s.repo.DeleteVideo(c, video); err != nil {
		return fmt.Errorf("Method:video.Service.DeleteVideo: %w", err)
	}
	return nil
}

//=========================辅助方法============================

// video是否存在以及权限是否充分（读：公开或作者）
func (s *Service) FindVideoAndForbidden(c context.Context, videoID, userID uint) (*modelsVideo.Video, error) {
	video, err := s.repo.FindByID(c, videoID)
	if err != nil {
		return nil, fmt.Errorf("Method:video.Service.FindVideoAndForbidden: %w", err)
	}
	if video == nil {
		return nil, fmt.Errorf("Method:video.Service.FindVideoAndForbidden: %w", codeErrors.ErrVideoNotFound)
	}
	// 可见性：非作者时，审核未通过（status!=2）或私密（view_status==2）一律视为不存在。
	// 作者本人永远可见（含待审核/私密），保证自己的作品可回看。
	if video.UserID != userID && (video.Status != 2 || (video.ViewStatus != nil && *video.ViewStatus == 2)) {
		return nil, fmt.Errorf("Method:video.Service.FindVideoAndForbidden: %w", codeErrors.ErrVideoNotFound)
	}
	return video, nil
}

// video是否存在以及是否为本视频作者
func (s *Service) AssessVideoAndAuthor(c context.Context, videoID, userID uint) (*modelsVideo.Video, error) {
	video, err := s.repo.FindByID(c, videoID)
	if err != nil {
		return nil, fmt.Errorf("Method:video.Service.AssessVideoAndAuthor: %w", err)
	}
	if video == nil {
		return nil, fmt.Errorf("Method:video.Service.AssessVideoAndAuthor: %w", codeErrors.ErrVideoNotFound)
	}
	if video.UserID != userID {
		return nil, fmt.Errorf("Method:video.Service.AssessVideoAndAuthor: %w", codeErrors.ErrVideoForbidden)
	}
	return video, nil
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
