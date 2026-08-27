package comment

import (
	"LikeBili/internal/middleware"
	modelsComments "LikeBili/internal/models/comments"
	svccomment "LikeBili/internal/service/comment"
	codeErrors "LikeBili/pkg/errors"
	"LikeBili/pkg/response"
	vt "LikeBili/pkg/validator"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	svc *svccomment.Service
}

func NewHandler(svc *svccomment.Service) *Handler {
	return &Handler{
		svc: svc,
	}
}

// Create 发表评论/回复（薄壳）：鉴权 → 解析路径参数 → 绑定并校验请求体 → 调 Service → 包装响应。
// 对应 POST /videos/:id/comments；未登录一律拒绝（service 层 Create 不校验 userID==0，鉴权在 handler 兜底）。
func (h *Handler) Create(c *gin.Context) {
	operation := "Create"
	// ① 鉴权：未登录直接拒绝并返回，防止未登录用户绕过鉴权创建评论
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

	// ③ 绑定 JSON 请求体（Content + ParentID，ParentID 缺省为 0=根评论）
	var req modelsComments.CommentReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.ErrorFrom(c, operation, codeErrors.Wrap(err, codeErrors.BadRequest, "请求参数类型错误"))
		return
	}

	// ④ 结构体校验：Content 必填且 ≤1000 字符（validate 标签，max 拦截超长评论入库）
	if err := vt.Struct(&req); err != nil {
		response.ErrorFrom(c, operation, codeErrors.Wrap(codeErrors.ErrorBadRequest, codeErrors.BadRequest, vt.TranslateError(err)))
		return
	}

	// ⑤ 冗余兜底：空内容直接拒绝（④ 的 required 已拦截，此处防御性保留）
	if strings.TrimSpace(req.Content) == "" {
		response.ErrorFrom(c, operation, codeErrors.Wrap(codeErrors.ErrorBadRequest, codeErrors.BadRequest, "评论不能为空"))
		return
	}

	// ⑥ 调 Service：根评论/回复的 ParentID 归属校验、RootID 继承、落库、通知被回复人均在 service 内部完成
	resp, err := h.svc.Create(c.Request.Context(), uint(videoID), userID, &req)
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	response.Success(c, resp)
}

// GetComments 视频根评论列表查询（公开读接口，游客可访问）。
// 登录用户 userID>0 时由 service 批量填充 IsLiked；游客 userID=0 跳过点赞关系查询、恒为 false。
// 查询参数：page/pageSize 分页，sort=hot 按点赞降序，其余值按创建时间倒序。
func (h *Handler) GetComments(c *gin.Context) {
	operation := "GetComments"
	// ① 取登录态：游客为 0（读接口不强制登录，仅影响 IsLiked 填充）
	userID := middleware.GetUserID(c)

	// ② 解析路径参数：视频 ID
	videoID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.ErrorFrom(c, operation, codeErrors.Wrap(err, codeErrors.BadRequest, "视频ID错误"))
		return
	}

	// ③ 分页与排序参数：非法/缺失值由 service 兜底（page>=1、pageSize 限 [1,64]、sort 白名单）
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "16"))
	sort := c.DefaultQuery("sort", "new")
	if sort != "hot" && sort != "new" {
		sort = "new"
	}

	// ④ 调 Service：根评论分页 + 回复预览 + IsLiked 批量填充均在 service 内部完成
	resp, err := h.svc.GetComments(c.Request.Context(), uint(videoID), userID, page, pageSize, sort)
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}
	response.Success(c, resp)
}

// LikeComment 评论点赞/取消（toggle，写操作需登录）。
// 对应 POST /videos/:id/comments/:comment_id/like；评论 ID 来自路径参数 :comment_id，
// 所属视频由 service 从评论行解析，无需前端提供；返回最新 Liked/Likes 供前端切换红心。
func (h *Handler) LikeComment(c *gin.Context) {
	operation := "LikeComment"
	// ① 鉴权：未登录直接拒绝并返回（写操作，与 Create 一致）
	userID := middleware.GetUserID(c)
	if userID == 0 {
		response.ErrorFrom(c, operation, codeErrors.ErrUnauthorized)
		return
	}

	// ② 解析评论 ID：路径参数 :comment_id（ToggleLike 只需评论 ID）
	commID, err := strconv.ParseUint(c.Param("comment_id"), 10, 64)
	if err != nil {
		response.ErrorFrom(c, operation, codeErrors.Wrap(err, codeErrors.BadRequest, "评论ID错误"))
		return
	}

	// ③ 调 Service：点赞/取消 toggle + 计数原子增减 + 视频热度埋点均在 service 内部完成
	resp, err := h.svc.ToggleLike(c.Request.Context(), userID, uint(commID))
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	response.Success(c, resp)
}

// DeleteComments 删除评论（软删除，写操作需登录）。
// 对应 DELETE /videos/:id/comments/:comment_id；权限模型：评论作者本人 或 视频作者可删
// （视频作者管理自己视频下的任意评论）；删除后由 service 侧的占位评论机制兜底展示。
func (h *Handler) DeleteComments(c *gin.Context) {
	operation := "DeleteComments"
	// ① 鉴权：未登录直接拒绝并返回（写操作，与 Create 一致）
	userID := middleware.GetUserID(c)
	if userID == 0 {
		response.ErrorFrom(c, operation, codeErrors.ErrUnauthorized)
		return
	}

	// ② 解析视频 ID：供 service 查询视频作者、判断"视频作者管理权"
	videoID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.ErrorFrom(c, operation, codeErrors.Wrap(err, codeErrors.BadRequest, "视频ID错误"))
		return
	}

	// ③ 解析评论 ID：路径参数 :comment_id
	commID, err := strconv.ParseUint(c.Param("comment_id"), 10, 64)
	if err != nil {
		response.ErrorFrom(c, operation, codeErrors.Wrap(err, codeErrors.BadRequest, "评论ID错误"))
		return
	}

	// ④ 调 Service：权限校验（评论作者/视频作者）与软删除均在 service 内部完成
	if err := h.svc.DeleteComments(c.Request.Context(), userID, uint(commID), uint(videoID)); err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	response.Success(c, nil)
}

// GetReplies 某根评论下的子评论分页查询（楼中楼"加载更多"，公开读接口，游客可访问）。
// 对应 GET /videos/:id/comments/:root_id/replies?page=X&pageSize=Y；root_id 来自路径参数，
// 登录用户 userID>0 时填充 IsLiked，游客恒为 false（与 GetComments 同款策略）。
func (h *Handler) GetReplies(c *gin.Context) {
	operation := "GetReplies"
	// ① 取登录态：游客为 0（读接口不强制登录，仅影响 IsLiked 填充）
	userID := middleware.GetUserID(c)

	// ② 分页参数：非法/缺失值由 service 兜底（page>=1、pageSize 限 [1,64]）
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "16"))

	// ③ 解析根评论 ID：路径参数 :root_id（GetReplies 按 root_id 定位整棵子树）
	rootID, err := strconv.ParseUint(c.Param("root_id"), 10, 64)
	if err != nil {
		response.ErrorFrom(c, operation, codeErrors.Wrap(err, codeErrors.BadRequest, "根ID错误"))
		return
	}

	// ④ 调 Service：按 root_id 分页拉取回复（含深层楼中楼平铺）+ IsLiked 批量填充
	resp, err := h.svc.GetReplies(c.Request.Context(), uint(rootID), userID, page, pageSize)
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}
	response.Success(c, resp)
}
