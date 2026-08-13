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

func (h *Handler) GetVideo(c *gin.Context) {
	operation := "GetVideo"
	videoID, err := param.Parse[uint](c, "id")
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	viewerID := middleware.GetUserID(c)

	resp, err := h.svc.GetVideo(c.Request.Context(), videoID, viewerID)
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	response.Success(c, resp)
}

func (h *Handler) UpdateVideo(c *gin.Context) {
	operation := "UpdateVideo"
	userID := middleware.GetUserID(c)
	if userID == 0 {
		response.ErrorFrom(c, operation, codeErrors.ErrUnauthorized)
		return
	}

	videoID, err := param.Parse[uint](c, "id")
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}
	var req modelsVideo.UpdateVideoReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}
	resp, err := h.svc.UpdateVideo(c.Request.Context(), videoID, userID, &req)
	if err != nil {

	}

}
