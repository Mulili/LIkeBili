// Package rank 提供基于 Redis ZSet 的热度排行榜服务。
// 埋点（Incr）由播放/点赞/评论模块调用；取榜（HotRank）按日/周/月窗口合并"天桶"返回 TopN 视频 ID。
package rank

import (
	"LikeBili/pkg/logger"
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// Service 热度排行榜服务：只依赖 Redis（rdb），不依赖任何业务模块。
// 职责：① Incr 埋点累加热度；② HotRank 按窗口（日/周/月）返回 TopN 视频 ID。
type Service struct {
	rdb *redis.Client
}

// NewService 构造排行榜服务。rdb 为 nil 时 Incr/HotRank 直接返回空（调用方走 DB 兜底）。
func NewService(rdb *redis.Client) *Service {
	return &Service{rdb: rdb}
}

// 互动行为的热度权重：播放 1.5、点赞 2.0、评论 2.5（按产品策略可调）。
const (
	DeltaViews = 1.5
	DeltaLike  = 2.0
	DeltaReply = 2.5

	// 榜单时间窗口：日/周/月，Handler 与前端共用的唯一口径
	WindowDay   = 24 * time.Hour
	WindowWeek  = 7 * 24 * time.Hour
	WindowMonth = 30 * 24 * time.Hour
)

// dayKey 生成"当天桶"的 key：rank:heat:20260811。
// 按天分桶，取榜时按窗口合并多个桶（ZUNIONSTORE）。
func dayKey(t time.Time) string {
	return "rank:heat:" + t.Format("20060102")
}

// Incr 热度埋点：向"当天桶"累加某视频的热度分数。
// 播放/点赞/评论等互动发生时调用一次；权重由 delta 决定（播放 1.5、点赞 2、评论 2.5）。
// 桶保留 3 天防内存膨胀；rdb 为 nil 时静默跳过，不阻塞业务主流程。
func (s *Service) Incr(c context.Context, videoID uint, delta float64) error {
	if s.rdb == nil {
		return nil
	}
	key := dayKey(time.Now())
	member := strconv.FormatUint(uint64(videoID), 10) // ZSet 的 member 必须是字符串
	if err := s.rdb.ZIncrBy(c, key, delta, member).Err(); err != nil {
		return fmt.Errorf("Method:rank.Service.Incr: %w", err)
	}
	s.rdb.Expire(c, key, 31*24*time.Hour) // 续期：覆盖月榜窗口（30 天）+1 天余量，保证合并时桶未过期
	return nil
}

// ParseWindow 把前端传来的窗口名解析为时间窗口；非法值返回错误，由 Handler 报参数错误。
func ParseWindow(name string) (time.Duration, error) {
	switch name {
	case "day":
		return WindowDay, nil
	case "week":
		return WindowWeek, nil
	case "month":
		return WindowMonth, nil
	default:
		return 0, fmt.Errorf("unknown window: %s", name)
	}
}

// HotRank 取榜：合并窗口内所有天桶（ZUNIONSTORE 默认 SUM 累加分数），
// 返回按热度降序的前 top 个视频 ID。
// Redis 不可用或冷启动（无任何数据）时返回 nil, nil，由调用方走 DB 兜底。
func (s *Service) HotRank(c context.Context, window time.Duration, top int) ([]uint, error) {
	if s.rdb == nil {
		return nil, nil
	}
	if top <= 0 {
		return nil, nil // top 非法：不查，避免 ZRevRange(0, -1) 返回全量
	}

	// ① 收集窗口内"已存在"的天桶 key（跳过空桶）。
	// 注意用 < 不是 <=：日榜=今天 1 桶（d=0）、周榜=7 桶（d=0..6）、月榜=30 桶（d=0..29）
	keys := make([]string, 0, int(window.Hours()/24))
	for d := 0; d < int(window.Hours()/24); d++ {
		keys = append(keys, dayKey(time.Now().AddDate(0, 0, -d)))
	}
	if len(keys) == 0 {
		return nil, nil // 冷启动：今天都还没有埋点数据
	}
	// ② 合并到临时 key：带纳秒后缀防并发请求互相覆盖，用完即删
	tmpkey := "rank:heat:tmp:" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := s.rdb.ZUnionStore(c, tmpkey, &redis.ZStore{Keys: keys}).Err(); err != nil {
		logger.Error("排行榜无法连接", zap.String("operation", "HotRank"), zap.Error(err))
		return nil, nil
	}
	defer s.rdb.Del(c, tmpkey)
	// ③ 按分数降序取前 top 个 member（视频 ID）
	members, err := s.rdb.ZRevRange(c, tmpkey, 0, int64(top-1)).Result()
	if err != nil {
		logger.Error("排行榜无法连接", zap.String("operation", "HotRank"), zap.Error(err))
		return nil, nil
	}
	ids := make([]uint, 0, len(members))
	for _, m := range members {
		id, err := strconv.ParseUint(m, 10, 64)
		if err != nil {
			continue // 脏数据跳过，不阻塞整个榜单
		}
		ids = append(ids, uint(id))
	}
	return ids, nil
}
