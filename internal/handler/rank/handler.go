package rank

import (
	"LikeBili/internal/service/rank"
	codeErrors "LikeBili/pkg/errors"
	"LikeBili/pkg/response"
	"context"
	"strconv"

	"github.com/gin-gonic/gin"
)

// FallbackFunc Redis 冷启动时取榜的 DB 兜底查询（依赖倒置：handler 不直接依赖业务模块）。
// 由 main.go 装配时注入（如 video 模块按播放量取 TopN），HotRank 无数据时调用。
type FallbackFunc func(ctx context.Context, top int) ([]uint, error)

// Handler 热门榜的 HTTP 薄壳：解析窗口与条数参数 → 调 rank 服务取榜 → 包装响应。
type Handler struct {
	svc      *rank.Service
	fallback FallbackFunc
}

// NewHandler 构造热门榜处理器，注入排行服务与冷启动兜底查询。
func NewHandler(svc *rank.Service, fallback FallbackFunc) *Handler {
	return &Handler{svc: svc, fallback: fallback}
}

// HotRank 热门榜查询（公开接口，游客可访问）。
// 对应 GET /rank/hot?window=day|week|month&top=20；
// 返回按热度降序的视频 ID 列表，供前端批量拉视频详情拼榜单页。
func (h *Handler) HotRank(c *gin.Context) {
	operation := "HotRank"

	// ① 解析窗口参数：window=day|week|month，非法值直接 400。
	//    ParseWindow 是 handler 与前端共用的唯一口径，避免各端口径漂移
	window, err := rank.ParseWindow(c.DefaultQuery("window", "day"))
	if err != nil {
		response.ErrorFrom(c, operation, codeErrors.Wrap(err, codeErrors.BadRequest, "窗口参数错误"))
		return
	}

	// ② 解析 top：缺失/非法回退默认 20，并设上限 50（防止恶意大 top 拖垮 ZUNIONSTORE 合并）
	top := 20
	if v, err := strconv.Atoi(c.DefaultQuery("top", "20")); err == nil && v > 0 {
		top = v
	}
	if top > 50 {
		top = 50
	}

	// ③ 调服务取榜：内部合并窗口内所有"天桶"，返回 TopN 视频 ID
	ids, err := h.svc.HotRank(c.Request.Context(), window, top)
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}
	// ④ 冷启动兜底：Redis 无任何埋点数据（空榜）时，改走 DB 按播放量取 TopN，
	//    保证榜单页不空；Redis 有数据后以 HotRank 的加权结果为准
	if len(ids) == 0 && h.fallback != nil {
		ids, err = h.fallback(c.Request.Context(), top)
		if err != nil {
			response.ErrorFrom(c, operation, err)
			return
		}
	}
	response.Success(c, ids)
}
