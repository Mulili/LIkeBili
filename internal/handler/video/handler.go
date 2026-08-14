package video

import (
	"LikeBili/internal/middleware"
	modelsVideo "LikeBili/internal/models/video"
	videoSvc "LikeBili/internal/service/video"
	codeErrors "LikeBili/pkg/errors"
	"LikeBili/pkg/logger"
	"LikeBili/pkg/param"
	"LikeBili/pkg/response"
	"LikeBili/pkg/upload"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Handler 持有视频模块的 Service 依赖。
// 通过构造函数注入（依赖倒置），Handler 不关心 Service 内部实现。
type Handler struct {
	svc *videoSvc.Service
}

// NewHandler 创建视频模块的 HTTP 处理器。
func NewHandler(svc *videoSvc.Service) *Handler {
	return &Handler{svc: svc}
}

// progressReader 包装上传流：每读一块字节，按"整百分比变化"回调一次进度。
// 整百分比节流：避免 16GB 文件刷爆 SSE 连接（否则每次 Read 都推一条 event）。
type progressReader struct {
	src     io.Reader
	read    int64
	total   int64
	lastPct int64 // 上次回调的百分比，变化时才再回调
	fn      func(uploaded, total int64)
}

// Read 实现 io.Reader：转发底层读取并累计已读字节，整百分比变化时回调进度。
func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.src.Read(p)
	r.read += int64(n)
	if pct := r.read * 100 / r.total; pct != r.lastPct {
		r.lastPct = pct
		r.fn(r.read, r.total)
	}
	return n, err
}

// Upload 处理视频上传请求（multipart/form-data），上传全程通过 SSE 推送进度。
//
// 业务流程：
//  1. 鉴权：从 JWT 中间件读取 userID，未登录直接拒绝（401）；
//  2. 取视频文件并校验：大小上限 + 嗅探真实 MIME/扩展名（防伪造类型）；
//  3. 解析表单字段 title/description/category_id 并做参数校验；
//  4. 取可选封面并校验（图片白名单，复用头像同一套 upload.Validate）；
//  5. 所有校验通过后才写 SSE 响应头——一旦写头状态码定 200，后续错误只能推事件、
//     不能再返回错误状态码，所以校验必须先于 SSE 全部完成；
//  6. 用 progressReader 包装视频流，随上传进度按整百分比推 SSE 事件；
//  7. 上传成功推 event:complete（含视频 DTO）；失败按业务/系统错误分别推送提示。
func (h *Handler) Upload(c *gin.Context) {
	// ---------- ① 鉴权：读取 JWT 中间件写入的 userID ----------
	userid := middleware.GetUserID(c)
	operation := "Upload"
	if userid == 0 {
		// 防御：路由正常挂 AuthRequired 时到不了这里；未挂中间件时由本判断兜底
		response.ErrorFrom(c, operation, codeErrors.ErrUnauthorized)
		return
	}

	// ---------- ② 取视频文件（multipart 字段名 "file"）----------
	file, fileHander, err := c.Request.FormFile("file")
	if err != nil {
		// 字段缺失/非 multipart：客户端问题，明确 400 提示
		response.ErrorFrom(c, operation, codeErrors.Wrap(err, codeErrors.BadRequest, "请上传视频文件！"))
		return
	}
	defer file.Close() // 请求结束释放文件句柄

	// ---------- ③ 视频校验：大小 + 嗅探真实类型 ----------
	// upload.Validate 读文件头 512 字节判断真实 MIME（不信任客户端 Content-Type）；
	// mp4 等嗅探不出时回退扩展名白名单；校验通过后文件指针已 Seek 回起点，可直接上传。
	contentType, ext, err := upload.Validate(file, fileHander.Filename, fileHander.Size, upload.Config{
		MaxSize:     1 << 34, // 16GB：给 4K 视频留足余量
		AllowedMIME: []string{"video/mp4", "video/webm", "video/quicktime", "video/x-matroska"},
		AllowedExt:  []string{"mp4", "webm", "mov", "mkv"},
	})
	if err != nil {
		// 超限→413，格式不符→400（response 的 httpStatusForCode 已映射）
		response.ErrorFrom(c, operation, err)
		return
	}

	// ---------- ④ 解析表单字段 ----------
	title := c.PostForm("title")
	description := c.PostForm("description")
	categoryIDStr := c.PostForm("category_id")

	// 标题必填 + 长度限制（64 字符内）
	if title == "" {
		response.ErrorFrom(c, operation, codeErrors.New(codeErrors.CodeInvalid, "标题不能为空"))
		return
	} else if len(title) > 64 {
		response.ErrorFrom(c, operation, codeErrors.New(codeErrors.CodeInvalid, "标题不能超过64个字符"))
		return
	}

	// 分类 ID 可选：传了但解析失败只记日志降级为 0（不过滤分类），不阻塞上传
	var categoryID uint
	if categoryIDStr != "" {
		parsed, err := strconv.ParseUint(categoryIDStr, 10, 64)
		if err == nil {
			categoryID = uint(parsed)
		} else if err != nil {
			logger.Warn("无法解析分类ID", zap.String("category_id", categoryIDStr))
		}
	}

	// ---------- ⑤ 可选封面 ----------
	// 封面不是必传项，分两种情况：字段缺失（ErrMissingFile）→ 静默跳过；传了 → 走图片白名单校验。
	var (
		coverFile        multipart.File
		coverSize        int64
		coverContentType string
		coverExt         string
	)
	coverFile, coverHeader, err := c.Request.FormFile("cover")
	if err != nil {
		// http.ErrMissingFile = 表单没传 cover 字段 → 封面可选，静默跳过
		if !errors.Is(err, http.ErrMissingFile) {
			response.ErrorFrom(c, operation, err)
			return
		}
		// 未传封面：coverFile 保持 nil，Service 侧跳过（可选字段）
	} else {
		defer coverFile.Close()
		// 封面走图片白名单校验（与头像上传同一套 upload.Validate）
		coverContentType, coverExt, err = upload.Validate(coverFile, coverHeader.Filename, coverHeader.Size, upload.Config{
			MaxSize:     1 << 23, // 8MB
			AllowedMIME: []string{"image/jpeg", "image/png", "image/webp"},
			AllowedExt:  []string{"jpeg", "png", "webp", "jpg"},
		})
		if err != nil {
			response.ErrorFrom(c, operation, err)
			return
		}
		coverSize = coverHeader.Size // 组装 input 时 coverHeader 可能为 nil，故单独存 size 兜底
	}

	// ---------- ⑥ 所有校验通过，开启 SSE 推送 ----------
	// 注意：一旦写出这些头，状态码固定 200，后续只能推事件、不能改状态码，
	// 因此所有校验分支（②~⑤）必须在此之前 return 完。
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.WriteHeader(http.StatusOK)

	// 校验响应流是否支持 Flush（SSE 即时推送的前提，nginx 等反向代理需关闭缓冲）
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		response.ErrorFrom(c, operation, codeErrors.New(codeErrors.Internal, "SSE未启动"))
		return
	}

	// progressFn：上传进度回调，把"已上传字节/总字节"推给前端进度条。
	// SSE 命名事件：progress（进度）/ error（失败）/ complete（完成），前端按事件名分别监听。
	progressFn := func(upLoad, total int64) {
		msg := modelsVideo.UpdateLoadReq{
			UpLoad: upLoad,
			Total:  total,
		}
		data, _ := json.Marshal(msg)
		fmt.Fprintf(c.Writer, "event: progress\ndata: %s\n\n", string(data))
		flusher.Flush()
	}

	// ---------- ⑦ 组装 Service 入参 ----------
	input := &videoSvc.UploadVideoInput{
		UserID:      userid,
		Title:       title,
		Description: description,
		CategoryID:  uint32(categoryID),
		// File 用 progressReader 包装：MinIO 上传时按实际读取字节回调进度（Service 零改动）
		File: &progressReader{
			src:   file,
			total: fileHander.Size,
			fn:    progressFn,
		},
		FileSize:         fileHander.Size,
		ContentType:      contentType, // 嗅探出的真实 MIME（防伪造）
		Ext:              ext,         // 嗅探出的真实扩展名（拼对象名用）
		CoverFile:        coverFile,   // 可选：nil 时 Service 跳过封面
		CoverSize:        coverSize,
		CoverContentType: coverContentType,
		CoverExt:         coverExt,
	}

	// ---------- ⑧ 执行上传（SSE 已开启，错误只能推事件、不能改状态码）----------
	resp, err := h.svc.UploadVideo(c.Request.Context(), input)
	if err != nil {
		if code, ok := codeErrors.From(err); ok {
			// 业务错误：把具体提示（如"标题不能为空"）以 event:error 推给前端
			msg := modelsVideo.UpdateLoadReq{ERROR: code.Message}
			data, _ := json.Marshal(msg)
			fmt.Fprintf(c.Writer, "event: error\ndata: %s\n\n", string(data))
			flusher.Flush()
			return
		}
		// 系统错误（GORM/MinIO 原始错误）：记日志排查根因，前端只见通用提示、不暴露内部细节
		logger.Error("上传视频失败", zap.String("operation", operation), zap.Uint("user_id", userid), zap.Error(err))
		msg := modelsVideo.UpdateLoadReq{ERROR: "上传视频失败，请稍后重试"}
		data, _ := json.Marshal(msg)
		fmt.Fprintf(c.Writer, "event: error\ndata: %s\n\n", string(data))
		flusher.Flush()
		return
	}

	// ---------- ⑨ 成功：推送 event:complete，携带视频 DTO ----------
	completeData, _ := json.Marshal(resp)
	fmt.Fprintf(c.Writer, "event: complete\ndata: %s\n\n", string(completeData))
	flusher.Flush()
}

// GetVideo 获取单个视频详情（GET /videos/:id，游客可访问）。
// 可见性规则统一收口在 Service 层的 FindVideoAndForbidden：
// 公开且审核通过的视频所有人可见；未过审/私密的视频仅作者本人可见，其他人一律返回 404。
func (h *Handler) GetVideo(c *gin.Context) {
	operation := "GetVideo"
	// ① 解析路径参数 id（缺失/非数字 → 400）
	videoID, err := param.Parse[uint](c, "id")
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	// ② 读取当前查看者身份：未挂 AuthRequired 的游客为 0，可见性判断时等同"非作者"
	viewerID := middleware.GetUserID(c)

	// ③ 查详情并做可见性校验：视频不存在 / 无权访问 → 404（Service 已映射好业务错误码）
	resp, err := h.svc.GetVideo(c.Request.Context(), videoID, viewerID)
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	// ④ 成功：返回视频 DTO（封面/头像等元信息，不含播放地址，播放走 /:id/play-url 现签）
	response.Success(c, resp)
}

// UpdateVideo 更新视频信息（PUT /videos/:id，需登录）。
// 权限要求：仅视频作者本人可修改，非作者 → 403 CodeVideoForbidden。
func (h *Handler) UpdateVideo(c *gin.Context) {
	operation := "UpdateVideo"
	// ① 鉴权兜底：正常路由挂 AuthRequired，此处防御 token 失效/未挂中间件的情况
	userID := middleware.GetUserID(c)
	if userID == 0 {
		response.ErrorFrom(c, operation, codeErrors.ErrUnauthorized)
		return
	}

	// ② 解析路径参数 id
	videoID, err := param.Parse[uint](c, "id")
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}
	// ③ 解析 JSON 请求体（字段缺失/类型不符 → 400）
	var req modelsVideo.UpdateVideoReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}
	// ④ 更新：Service 内部校验"视频存在 + 是作者本人"，失败分别映射 404/403
	resp, err := h.svc.UpdateVideo(c.Request.Context(), videoID, userID, &req)
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	// ⑤ 成功：返回更新后的视频 DTO
	response.Success(c, resp)
}

// DeleteVideo 删除视频（DELETE /videos/:id，需登录）。
// 权限要求：仅视频作者本人可删除（与 UpdateVideo 同一套 AssessVideoAndAuthor 校验）。
func (h *Handler) DeleteVideo(c *gin.Context) {
	operation := "DeleteVideo"
	// ① 鉴权兜底
	userID := middleware.GetUserID(c)
	if userID == 0 {
		response.ErrorFrom(c, operation, codeErrors.ErrUnauthorized)
		return
	}

	// ② 解析路径参数 id
	id, err := param.Parse[uint](c, "id")
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	// ③ 删除：Service 层校验作者身份 + 级联处理（失败映射 404/403）
	if err := h.svc.DeleteVideo(c.Request.Context(), id, userID); err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	// ④ 成功：无返回体，仅状态码 200
	response.Success(c, nil)
}

// ListVideo 分页拉取公开视频列表（GET /videos，游客可访问，首页/分类页用）。
// 查询参数：
//   - page：页码（默认 1，非法值由 Service 兜底为 1）
//   - page_size：每页条数（默认 16，上限 50）
//   - category_id：分类过滤（默认 0 = 全部分类；非法值降级为不过滤）
func (h *Handler) ListVideo(c *gin.Context) {
	operation := "ListVideo"
	// ① 解析分页参数：strconv 失败返回 0/默认值，由 Service 层统一做防御性兜底
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "16"))
	categoryIDStr := c.DefaultQuery("category_id", "0")
	categoryID, _ := strconv.ParseUint(categoryIDStr, 10, 64)

	// ② 查公开视频：repo 已过滤 status=2（审核通过）+ view_status=1（公开）
	resp, err := h.svc.ListPagePublicVideo(c.Request.Context(), uint(page), uint(pageSize), uint(categoryID))
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	// ③ 成功：返回分页结构（List + Total + Page + PageSize）
	response.Success(c, resp)
}

// ListUserVideos 分页查询指定用户的视频列表（GET /users/:id/videos，游客可访问）。
// 查询参数：
//   - page：页码（默认 1）、page_size：每页条数（默认 12，上限 50）
//   - status：按状态过滤，不传 = 全部（作者本人查看自己的作品时使用）
func (h *Handler) ListUserVideos(c *gin.Context) {
	operation := "ListUserVideos"
	// ① 解析路径参数：目标用户的 id
	userID, err := param.Parse[uint](c, "id")
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	// ② 解析分页参数
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "12"))

	// ③ 解析可选的状态过滤参数：仅当合法时才设置过滤器
	var statusFilter *uint8
	statusStr := c.DefaultQuery("status", "")
	if statusStr != "" {
		status, err := strconv.ParseUint(statusStr, 10, 8)
		if err == nil {
			value := uint8(status)
			statusFilter = &value
		} else {
			logger.Warn("无法解析视频状态", zap.Uint("user_id", userID), zap.Error(err))
		}

	}

	// ④ 查列表（按状态过滤可选）
	resp, err := h.svc.ListUserVideos(c.Request.Context(), userID, statusFilter, page, pageSize)
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	// ⑤ 成功：返回分页结构
	response.Success(c, resp)
}

// HotRank 返回按热度排序的排行榜（GET /videos/hot-rank，游客可访问）。
// 前端只需要告诉窗口口径即可，不用关心 Redis 天桶合并等内部实现：
//   - window：day=日榜 / week=周榜 / month=月榜（默认 day）
//   - top：返回条数上限（默认 10，钳制在 1~50，防超大 top 拖垮 DB 回查）
//
// 实现链路：Handler 只取窗口名字符串原样下传给 Service →
// Service.HotRankVideosByWindow 内部调 rank.ParseWindow 解析成时间窗口 →
// 复用 HotRankVideos：rank.HotRank 合并窗口内天桶取 TopN 视频 ID →
// 回查 DB 拼 DTO；Redis 冷启动/不可用时自动降级为空榜（Total=0），不阻塞页面。
func (h *Handler) HotRank(c *gin.Context) {
	operation := "HotRank"

	// ① 取窗口参数：原样传字符串，由 Service 内部调 rank.ParseWindow 解析（非法值 → 400）
	windowName := c.DefaultQuery("window", "day")

	// ② 解析 top 条数：默认 10，越界钳制到 1~50
	top, _ := strconv.Atoi(c.DefaultQuery("top", "10"))
	if top < 1 {
		top = 10
	}
	if top > 50 {
		top = 50
	}

	// ③ 取榜：窗口内按热度降序返回视频列表（空榜返回空 List，不算错误）
	resp, err := h.svc.HotRankVideosByWindow(c.Request.Context(), windowName, top)
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	// ④ 成功：返回按热度降序的视频列表
	response.Success(c, resp)
}

// HotVideos 返回热门视频列表（GET /videos/hot，游客可访问）。
// 两级策略在 Service 层实现：优先读 Redis "video:hot" 缓存（10 分钟过期），
// 未命中/异常时降级为 DB 全量实时计算热度并回写缓存。
// 查询参数：page（默认 1）、page_size（默认 16，上限 50）。
func (h *Handler) HotVideos(c *gin.Context) {
	operation := "HotVideos"
	// ① 解析分页参数：非法值由 Service 兜底（page<1→1，pageSize 越界→16）
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "16"))

	// ② 取热门列表：Redis 缓存优先，DB 实时计算兜底
	resp, err := h.svc.HotVideos(c.Request.Context(), uint(page), uint(pageSize))
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	// ③ 成功：返回分页结构
	response.Success(c, resp)
}

// GetPlayURL 获取视频播放地址（GET /videos/:id/play-url，游客可访问）。
// 详情接口不返回播放地址（防爬虫抓取），前端点击播放时单独请求，
// 拿到 1 小时有效的 MinIO 预签名 URL 后交给播放器。
// 可见性校验与 GetVideo 一致：公开且审核通过，或作者本人。
func (h *Handler) GetPlayURL(c *gin.Context) {
	operation := "GetPlayURL"
	// ① 解析路径参数 id
	videoID, err := param.Parse[uint](c, "id")
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	// ② 读取查看者身份（游客为 0），Service 层做可见性校验
	userID := middleware.GetUserID(c)

	// ③ 校验可见性 + 现签预签名 URL（1 小时有效）
	url, err := h.svc.GetPresignedUrl(c.Request.Context(), videoID, userID)
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	// ④ 成功：返回播放地址字符串
	response.Success(c, url)
}
