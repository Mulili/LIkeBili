package coin

import (
	"LikeBili/internal/middleware"
	modelsCoins "LikeBili/internal/models/coin"
	svccoins "LikeBili/internal/service/coin"
	codeErrors "LikeBili/pkg/errors"
	"LikeBili/pkg/response"
	vt "LikeBili/pkg/validator"
	"strconv"

	"github.com/gin-gonic/gin"
)

// Handler 币模块的 HTTP 薄壳：解析鉴权与请求体 → 调 Service → 包装响应。
type Handler struct {
	svc *svccoins.Service
}

// NewHandler 构造币处理器，注入币服务。
func NewHandler(svc *svccoins.Service) *Handler {
	return &Handler{svc: svc}
}

// DropCoins 投币接口（薄壳）：鉴权 → 绑定请求体 → 结构体校验 → 调 Service → 包装响应。
// 对应 POST /coins/drop；投币数必须 ∈ {1,2}（oneof 校验），否则直接 400，防止绕过 2 币上限。
func (h *Handler) DropCoins(c *gin.Context) {
	operation := "DropCoins"
	// ① 鉴权：未登录直接拒绝并返回（投币是扣费操作，必须登录）
	userID := middleware.GetUserID(c)
	if userID == 0 {
		response.ErrorFrom(c, operation, codeErrors.ErrUnauthorized)
		return
	}

	// ② 绑定 JSON 请求体（video_id + count）
	var req modelsCoins.CoinReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.ErrorFrom(c, operation, codeErrors.Wrap(err, codeErrors.BadRequest, "请求格式错误"))
		return
	}

	// ③ 结构体校验：video_id 必填、count 只能投 1 或 2。
	//    必须在此拦截——仓库层的累计上限检查只覆盖"补投"，首次投币不校验，绕过会让 count 任意值入库
	if err := vt.Struct(&req); err != nil {
		response.ErrorFrom(c, operation, codeErrors.Wrap(codeErrors.ErrorBadRequest, codeErrors.BadRequest, vt.TranslateError(err)))
		return
	}

	// ④ 调 Service：扣款/作者收币/流水在同一事务完成；返回累计投币数 + 最新余额
	resp, err := h.svc.DropCoins(c.Request.Context(), userID, &req)
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	response.Success(c, resp)
}

// CountVideoCoins 查询视频收到的投币总数（公开读接口，游客可访问）。
// 对应 GET /videos/:id/coins（或查询参数）；供视频页展示"已投 X 币"。
func (h *Handler) CountVideoCoins(c *gin.Context) {
	operation := "CountVideoCoins"
	// ① 解析路径参数：视频 ID
	videoID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.ErrorFrom(c, operation, codeErrors.Wrap(err, codeErrors.BadRequest, "视频ID错误"))
		return
	}

	// ② 调 Service：统计该视频所有用户累计投币数（无人投币返回 0）
	count, err := h.svc.CountVideoCoins(c.Request.Context(), uint(videoID))
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	response.Success(c, count)
}

// FindUserDrop 查询当前用户对该视频的投币状态（需登录）。
// 返回 can_drop（是否还可投币）+ remain_count（剩余可投数），前端据此控制投币按钮置灰。
func (h *Handler) FindUserDrop(c *gin.Context) {
	operation := "FindUserDrop"
	// ① 鉴权：投币状态是当前用户的私有信息，未登录拒绝
	userID := middleware.GetUserID(c)
	if userID == 0 {
		response.ErrorFrom(c, operation, codeErrors.ErrUnauthorized)
		return
	}

	// ② 解析路径参数：视频 ID
	videoID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.ErrorFrom(c, operation, codeErrors.Wrap(err, codeErrors.BadRequest, "视频ID错误"))
		return
	}

	// ③ 调 Service：返回用户对该视频的投币状态 DTO（可投与否 + 剩余可投数）
	resp, err := h.svc.FindUserDrop(c.Request.Context(), uint(videoID), userID)
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}
	response.Success(c, resp)
}

// FindBalance 查询当前用户硬币余额（需登录）。
// 对应个人中心"我的硬币"；新用户无钱包时由 service 兜底创建并返回 0 余额。
func (h *Handler) FindBalance(c *gin.Context) {
	operation := "FindBalance"
	// ① 鉴权：余额是当前用户的私有信息，未登录拒绝
	userID := middleware.GetUserID(c)
	if userID == 0 {
		response.ErrorFrom(c, operation, codeErrors.ErrorUnauthorized)
		return
	}

	// ② 调 Service：获取最新余额（内部含钱包 GetOrCreate 兜底）
	resp, err := h.svc.FindBalance(c.Request.Context(), userID)
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	response.Success(c, resp)
}
