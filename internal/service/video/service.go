// Package video 提供视频模块的 Service（业务逻辑）层。
// 职责：上传文件到 MinIO → 建数据库记录 → 触发转码。
// 约定：文件校验已在 Handler 完成，这里只接收"校验后的文件流 + 校验结果"。
package video

import (
	"context"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"time"

	"LikeBili/internal/models/transcode"
	modelsVideo "LikeBili/internal/models/video"
	rpvideo "LikeBili/internal/repository/video"
	"LikeBili/internal/service/rank"
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
	rank    *rank.Service

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
func NewService(repo *rpvideo.Repository, rdb *redis.Client, storage *storage.MinIO, toresp *toresp.VideoRespBuilder, rank *rank.Service, opts ...Option) *Service {
	s := &Service{repo: repo, rdb: rdb, storage: storage, toresp: toresp, rank: rank}
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

// GetVideo 获取视频详情（读操作）。
// 校验与 GetPresignedUrl 一致：视频必须存在且可见（Status=2 公开，或作者本人可看）。
// 顺带做两件事：① 播放量 +1（DB，失败不阻塞，只记日志）；② 热度埋点（rank.Incr，播放权重）。
// 封面/头像由 toresp 统一拼公开 URL；播放地址由 GetPresignedUrl 现签，DTO 不含。
func (s *Service) GetVideo(c context.Context, videoID, userID uint) (*modelsVideo.VideoResp, error) {
	// ① 读前置校验：视频必须存在，且对当前用户可见。
	// 非作者时要求 status=2（过审）且非私密；作者本人始终可见（含待审核/私密）。
	video, err := s.FindVideoAndForbidden(c, videoID, userID)
	if err != nil {
		return nil, err
	}
	// ② 播放量落库 +1：统计性数据，DB 失败不阻塞详情返回，只记日志容忍丢失
	if err := s.repo.IncrementViews(c, videoID); err != nil {
		logger.Warn("自增播放量失败", zap.Uint("video_id", videoID), zap.Error(err))
	}
	// ③ 内存态 Views+1：让本次响应里的播放量立刻 +1，省一次回查 DB
	video.Views++
	// ④ 热度埋点：把本次播放投递到 rank 服务（Redis 当天桶 +1.5 分），供日/周/月榜使用。
	// Redis 抖动失败同样不阻塞主流程，只记日志。
	if err := s.rank.Incr(c, videoID, rank.DeltaViews); err != nil {
		logger.Warn("热度埋点失败", zap.Uint("video_id", videoID), zap.Error(err))
	}
	// ⑤ 转 DTO：封面/头像由 toresp 统一拼公开 URL；
	// 播放地址故意不出现在 DTO 里（防爬），前端点播放时另行请求 GetPresignedUrl 现签
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

// 软删除视频
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

// ListPagePublicVideo 分页获取公开视频列表（首页/分类页）。
// 查询条件：status=2（审核通过）+ view_status=1（公开）；categoryID 为 0 时不过滤分类。
// 分页约束：page 最小 1，pageSize 限制在 1~50（默认 16）。
// 返回 ListVideo：items 已由 toresp 拼好封面/头像公开 URL。
func (s *Service) ListPagePublicVideo(c context.Context, page, pageSize uint, categoryID uint) (*modelsVideo.ListVideo, error) {
	// ① 分页参数防御：page 最小 1；pageSize 越界（<1 或 >50）回退默认 16。
	// 上限 50 是防超大页拖垮 DB 查询，默认 16 是首页常规一屏数量。
	if page < 1 {
		page = 1
	}
	if pageSize > 50 || pageSize < 1 {
		pageSize = 16
	}
	// ② 查询公开视频列表：repo 层已过滤 status=2（审核通过）+ view_status=1（公开）；
	// categoryID=0 表示不过滤分类（首页全量）；total 是满足条件的总条数，供前端算总页数。
	videos, total, err := s.repo.FindList(c, page, pageSize, categoryID)
	if err != nil {
		return nil, fmt.Errorf("Method:video.Service.FindList: %w", err)
	}
	// ③ 逐个转 DTO：封面/头像由 toresp 在此拼成公开 URL。
	// 用 &videos[i] 取切片元素地址（可寻址），而不是 range 循环变量 &v，避免取地址陷阱。
	items := make([]modelsVideo.VideoResp, len(videos))
	for i := range videos {
		items[i] = *s.toresp.ToVideoResp(&videos[i])
	}
	// ④ 组装分页响应：List 是本次页数据，Total 是总数，Page/PageSize 原样回传供前端维护分页状态
	return &modelsVideo.ListVideo{
		List:     items,
		Total:    uint(total),
		Page:     uint16(page),
		PageSize: uint16(pageSize),
	}, nil
}

// HotVideos 获取热门视频列表（首页/热门榜）。
// 两级策略：
//  1. 优先读 Redis 缓存 "video:hot"（有序列表，按热度从高到低存视频 ID，10 分钟过期）：
//     命中则按页取 ID → 逐个回查 DB → 转 DTO 返回，避免每次请求都全量计算热度。
//  2. 缓存未命中或读取异常：降级为 DB 全量公开视频，按热度公式实时计算并排序，
//     随后回写 Redis 供后续请求命中。
//
// 分页约束：page 最小 1，pageSize 限制在 1~50（默认 16）。
func (s *Service) HotVideos(c context.Context, page, pageSize uint) (*modelsVideo.ListVideo, error) {
	// ① 分页参数防御：page 最小 1；pageSize 越界（<1 或 >50）回退默认 16
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 50 {
		pageSize = 16
	}
	// 热门榜单的 Redis 缓存 key
	rdbKey := "video:hot"

	// ② 优先走缓存：未接入 Redis 或缓存为空时才降级到 DB 计算
	if s.rdb != nil {
		// 取缓存列表总长度，同时作为响应里的 Total；读取失败只记日志并降级
		lenth, err := s.rdb.LLen(c, rdbKey).Result()
		if err != nil {
			// LLen 失败 → lenth=0 且 err!=nil，下方缓存分支不会进入，直接降级到 DB 实时计算
			logger.Warn("Redis 获取热门列表总数失败，降级走 DB 实时计算", zap.String("operation", "HotVideos"), zap.String("key", rdbKey), zap.Error(err))
		}
		// 缓存非空才走缓存分支
		if err == nil && lenth > 0 {
			// 计算本页在有序列表中的下标区间 [start, stop]（LRange 含 stop 端点，故减 1）
			start := int64((page - 1) * pageSize)
			stop := start + int64(pageSize) - 1
			// 取本页的视频 ID 字符串列表
			idxs, err := s.rdb.LRange(c, rdbKey, start, stop).Result()
			if err == nil && len(idxs) > 0 {
				items := make([]modelsVideo.VideoResp, 0, len(idxs))
				// 逐个 ID 回查 DB：ID 解析失败、视频已删除均跳过（缓存可能残留失效 ID）
				for _, idStr := range idxs {
					id, err := strconv.ParseUint(idStr, 10, 64)
					if err != nil {
						continue
					}
					video, err := s.repo.FindByID(c, uint(id))
					if err != nil || video == nil {
						continue
					}
					// 可见性兜底：仅展示公开（过审）视频；userID=0 表示匿名游客视角
					if _, err := s.FindVideoAndForbidden(c, video.ID, 0); err != nil {
						continue
					}
					items = append(items, *s.toresp.ToVideoResp(video))
				}
				// 缓存命中：Total 用缓存列表总长度（与 items 长度可能不同，因跳过了失效视频）
				total := lenth
				return &modelsVideo.ListVideo{
					List:     items,
					Total:    uint(total),
					Page:     uint16(page),
					PageSize: uint16(pageSize),
				}, nil
			}
		}
	}

	// ③ 降级路径：缓存未命中/异常 → DB 全量公开视频 + 实时热度计算
	videos, err := s.repo.ListPublic(c)
	if err != nil {
		return nil, fmt.Errorf("Method:video.Service.HotVideos: %w", err)
	}
	nowTime := time.Now()
	// scored 是"视频 ID + 热度得分"的中间结构，仅用于本次排序
	type scored struct {
		id    uint
		score float64
	}
	// 热度公式：score = 播放量 / 时间衰减因子
	scoreList := make([]scored, len(videos))
	for i, v := range videos {
		// 视频发布距现在的小时数（越小越新）
		scoreTime := nowTime.Sub(v.CreatedAt).Hours()
		// 单位时间内播放量越高热度越高；+2 防除零，1.5 次方为时间衰减（越老衰减越快）
		score := float64(v.Views) / math.Pow(scoreTime+2, 1.5)
		scoreList[i] = scored{id: v.ID, score: score}
	}
	// 按热度降序排序（快排，平均 O(nlogn)）
	sort.Slice(scoreList, func(i, j int) bool {
		return scoreList[i].score > scoreList[j].score
	})

	// ④ 把本次计算的榜单回写 Redis（10 分钟过期），后续请求直接命中缓存
	if s.rdb != nil {
		// 先清空旧榜单再写入，避免新旧数据叠加
		s.rdb.Del(c, rdbKey)
		// Pipeline 批量提交，减少网络往返
		pipe := s.rdb.Pipeline()
		for _, sc := range scoreList {
			pipe.RPush(c, rdbKey, strconv.FormatUint(uint64(sc.id), 10))
		}
		pipe.Expire(c, rdbKey, 600*time.Second)
		pipe.Exec(c)
	}

	// ⑤ 按当前页在降序榜单上切片，取本页数据
	start := (page - 1) * pageSize
	end := start + pageSize
	if end > uint(len(scoreList)) {
		end = uint(len(scoreList))
	}

	// ⑥ 逐个回查 DB 转 DTO：视频已不存在则跳过（榜单计算期间可能被删除）
	items := make([]modelsVideo.VideoResp, 0, end-start)
	for i := start; i < end; i++ {
		video, repoErr := s.repo.FindByID(c, scoreList[i].id)
		if repoErr != nil || video == nil {
			continue
		}
		items = append(items, *s.toresp.ToVideoResp(video))
	}
	return &modelsVideo.ListVideo{
		List:     items,
		Total:    uint(len(scoreList)),
		Page:     uint16(page),
		PageSize: uint16(pageSize),
	}, nil
}

func (s *Service) ListUserVideos(c context.Context, userID uint, status *uint8, page, pageSize int) (*modelsVideo.ListVideo, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 50 {
		pageSize = 16
	}

	videos, total, err := s.repo.FindListByUser(c, userID, page, pageSize, status)
	if err != nil {
		return nil, fmt.Errorf("Method:video.Service.ListUserVideos: %w", err)
	}

	items := make([]modelsVideo.VideoResp, len(videos))
	for i, v := range videos {
		items[i] = *s.toresp.ToVideoResp(&v)
	}

	return &modelsVideo.ListVideo{
		List:     items,
		Total:    uint(total),
		Page:     uint16(page),
		PageSize: uint16(pageSize),
	}, nil
}

// HotRankVideos 按窗口（日/周/月）返回 TopN 视频详情。
// rank.HotRank 只返回视频 ID，这里回查 DB 拼成完整 DTO（封面/头像 URL）。
func (s *Service) HotRankVideos(c context.Context, window time.Duration, top int) (*modelsVideo.ListVideo, error) {
	ids, err := s.rank.HotRank(c, window, top)
	if err != nil {
		return nil, fmt.Errorf("Method:video.Service.HotRankVideos: %w", err)
	}
	items := make([]modelsVideo.VideoResp, 0, len(ids))
	for _, id := range ids {
		video, err := s.FindVideoAndForbidden(c, id, 0)
		if err != nil {
			continue
		}
		items = append(items, *s.toresp.ToVideoResp(video))
	}
	return &modelsVideo.ListVideo{List: items, Total: uint(len(items))}, nil
}

func (s *Service) HotRankVideosByWindow(c context.Context, windowname string, top int) (*modelsVideo.ListVideo, error) {
	window, err := rank.ParseWindow(windowname)
	if err != nil {
		return nil, fmt.Errorf("Method:video.Service.HotRankVideosByWindow: %w", codeErrors.New(codeErrors.CodeInvalid, "window 参数仅支持 day/week/month"))
	}
	return s.HotRankVideos(c, window, top)
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
