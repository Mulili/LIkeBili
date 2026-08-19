package like

import (
	modelsLike "LikeBili/internal/models/like"
	rplike "LikeBili/internal/repository/like"
	"context"
	"fmt"

	"LikeBili/pkg/logger"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// 将点赞转发给作者的接口
type Notifier interface {
	SendNotification(c context.Context, userID uint, fromUserID uint, msgType int8, targetID uint, content string) error
}

type Service struct {
	repo     *rplike.Repository
	rdb      *redis.Client
	notifier Notifier
}

func NewService(repo *rplike.Repository, rdb *redis.Client, notifier Notifier) *Service {
	return &Service{repo: repo, rdb: rdb, notifier: notifier}
}

// likeCountKey 返回视频点赞计数的 Redis 缓存键。
// 该键退化为纯计数（INCR/DEL），不承载"谁点了赞"的集合判断——is_liked 一律查 MySQL。
func likeCountKey(videoID uint) string {
	return fmt.Sprintf("video:like:count:%d", videoID)
}

// Create 点赞/取消（toggle），以 MySQL 为权威数据源：
//   - 未赞 → InsertIgnore 幂等新增（唯一索引 uk_user_video 防重复，true=本次真正新增）
//   - 已赞 → Delete 删除关系记录（再次点击 = 取消点赞）
//
// Redis 仅作计数缓存（尽力同步，失败只记日志，不阻塞主流程）；
// is_liked（返回值 Liked）由 MySQL 的 InsertIgnore 返回值决定，不依赖 Redis。
func (s *Service) Create(c context.Context, userID, videoID uint) (*modelsLike.LikeResp, error) {
	// ① MySQL 权威写入：幂等插入，返回是否真正新增（false=已存在，说明当前已是已赞态）
	inserted, err := s.repo.InsertIgnore(c, userID, videoID)
	if err != nil {
		return nil, fmt.Errorf("Method:like.service.Create: %w", err)
	}

	liked := inserted
	if !inserted {
		// ② 已赞状态下再次点击 = 取消点赞：删除 MySQL 关系记录
		if err := s.repo.Delete(c, userID, videoID); err != nil {
			return nil, fmt.Errorf("Method:like.service.Create: %w", err)
		}
	}

	// ③ 尽力同步 Redis 计数缓存（可选层）：失败只记日志，主流程照常返回
	s.syncCountCache(c, videoID, liked)

	// ④ 新增点赞时通知视频作者（作者不能给自己点赞，查询失败/不存在也跳过）
	if inserted && s.notifier != nil {
		s.notifyAuthor(c, userID, videoID)
	}

	// ⑤ 读最新计数：Redis 缓存未命中/故障时回退 MySQL COUNT
	count, err := s.GetVideoLikes(c, videoID)
	if err != nil {
		return nil, fmt.Errorf("Method:like.service.Create: %w", err)
	}
	return &modelsLike.LikeResp{Liked: liked, Count: count}, nil
}

// GetVideoLikes 读取视频点赞总数。
// 优先 Redis 计数缓存（GET）；未命中（redis.Nil）或连接故障时回退 MySQL COUNT（权威），
// 回源成功后写回缓存供后续请求命中。
func (s *Service) GetVideoLikes(c context.Context, videoID uint) (uint, error) {
	key := likeCountKey(videoID)
	if s.rdb != nil {
		val, err := s.rdb.Get(c, key).Int64()
		if err == nil {
			return uint(val), nil
		}
		// redis.Nil=未命中、其他=连接故障：均走 MySQL 回源
	}

	// 回源 MySQL（权威计数源）
	count, err := s.repo.Count(c, videoID)
	if err != nil {
		return 0, fmt.Errorf("Method:like.service.GetVideoLikes: %w", err)
	}

	// 回源成功后写回缓存（尽力，失败忽略）
	if s.rdb != nil {
		if err := s.rdb.Set(c, key, count, 0).Err(); err != nil {
			logger.Warn("点赞计数回源写缓存失败", zap.Uint("video_id", videoID), zap.Error(err))
		}
	}
	return uint(count), nil
}

// syncCountCache 尽力同步 Redis 计数缓存：点赞 → INCR；取消 → DEL。
// 取消用 DEL 而非 DECR：避免在空键上 DECR 产生负值、以及与 MySQL 漂移——
// 删除后下次读取未命中，会自动从 MySQL 回源重建精确计数。
func (s *Service) syncCountCache(c context.Context, videoID uint, liked bool) {
	if s.rdb == nil {
		return
	}
	key := likeCountKey(videoID)
	var err error
	if liked {
		_, err = s.rdb.Incr(c, key).Result()
	} else {
		_, err = s.rdb.Del(c, key).Result()
	}
	if err != nil {
		logger.Warn("点赞计数缓存同步失败", zap.Uint("video_id", videoID), zap.Bool("liked", liked), zap.Error(err))
	}
}

// notifyAuthor 点赞后通知视频作者（尽力而为，通知失败不阻塞点赞主流程）。
func (s *Service) notifyAuthor(c context.Context, userID, videoID uint) {
	// ① 查作者 ID：Pluck 无 ErrRecordNotFound 语义，作者不存在时返回 (0, nil)
	authorID, err := s.repo.InformAuthorLike(c, videoID)
	if err != nil || authorID == 0 || authorID == userID {
		return // 查询失败 / 视频无作者 / 点赞者就是作者本人：无需通知
	}

	// ② 取点赞者展示名（昵称优先，空则回退为"用户"）
	likeUsername := "用户"
	if name, err := s.repo.FindLikeUsername(c, userID); err == nil && name != "" {
		likeUsername = name
	}

	// ③ 发送通知（msgType=2 为点赞类型）
	content := fmt.Sprintf("%s 点赞了你的视频", likeUsername)
	if err := s.notifier.SendNotification(c, authorID, userID, 2, videoID, content); err != nil {
		logger.Warn("点赞通知发送失败", zap.Uint("author_id", authorID), zap.Uint("video_id", videoID), zap.Error(err))
	}
}
