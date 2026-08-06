// Package user 提供用户模块的 HTTP 处理器（Handler）层。
//
// 职责：把 HTTP 请求翻译成对 Service 层的调用，再统一通过 pkg/response 输出响应。
// 调用链：gin 路由 → Handler（本包）→ Service → Repository →（GORM / MinIO）。
// 所有错误都走 response.ErrorFrom：能提取到业务错误就返回对应的业务码 + HTTP 状态码，
// 提取不到（如 GORM/存储原始错误）则兜底 500 并记日志。
package user

import (
	"LikeBili/internal/middleware"
	modelsUser "LikeBili/internal/models/user"
	userservice "LikeBili/internal/service/user"
	codeErrors "LikeBili/pkg/errors"
	"LikeBili/pkg/param"
	"LikeBili/pkg/response"
	"LikeBili/pkg/upload"

	"github.com/gin-gonic/gin"
)

// Handler 持有用户模块的 Service 依赖。
// 通过构造函数注入（依赖倒置），Handler 本身不关心 Service 内部如何实现。
type Handler struct {
	svc *userservice.Service
}

// NewHandler 创建 Handler 实例，注入 Service 依赖。
func NewHandler(svc *userservice.Service) *Handler {
	return &Handler{svc: svc}
}

// GetUser 处理"查询用户信息"请求：GET /user/:id。
// 流程：解析路径参数 id → 调 Service 查询 → 成功返回用户信息（头像已转为完整 URL）。
func (h *Handler) GetUser(c *gin.Context) {
	// param.Parse[uint]：泛型解析路径参数，类型写在方括号里，返回直接是 uint。
	// 解析失败（如 /user/abc）时，内部已把错误包装为 CodeInvalid（→ HTTP 400）。
	id, err := param.Parse[uint](c, "id")
	operation := "GetUser" // operation 用于 ErrorFrom 兜底分支的日志标记，便于排查"哪个接口挂了"
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	// c.Request.Context()：把请求上下文传给 Service，超时/取消时可中断数据库查询。
	// 注意与 gin 的 c 区分：Service 层依赖标准库 context.Context，而非 gin.Context。
	resp, err := h.svc.GetUser(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}
	response.Success(c, resp)
}

// UpdateUser 处理"修改个人资料"请求。
// 流程：解析 id → 校验"只能改自己"（路径 id 必须等于 JWT 中的 userId）→ 绑定 JSON → 更新。
// 注意：修改头像不在这里，头像走 UploadAvatar 接口（方案 B：UpdateUser 只改文字资料）。
func (h *Handler) UpdateUser(c *gin.Context) {
	id, err := param.Parse[uint](c, "id")
	operation := "UpdateUser"
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	// middleware.GetUserID 从 gin.Context 读取鉴权中间件写入的 userId（来自 JWT claims）。
	// 越权防护：URL 里的 id 必须与登录用户一致，否则返回 403（ErrCodeForbidden → HTTP 403）。
	userid := middleware.GetUserID(c)
	if id != userid {
		response.ErrorFrom(c, operation, codeErrors.ErrCodeForbidden)
		return
	}

	// ShouldBindJSON 把请求体反序列化到结构体（失败通常是前端 JSON 格式问题 → 400）。
	var req modelsUser.UpdateProfileReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	resp, err := h.svc.UpdateUser(c.Request.Context(), id, &req)
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}
	response.Success(c, resp)
}

// UploadAvatar 处理"上传/更换头像"请求：multipart/form-data。
// 流程：解析 id → 权限校验 → 取文件 → upload.Validate 校验（大小 + 嗅探格式）→ Service 上传 → 返回新头像 URL。
// 头像唯一写入入口：数据库 avatar 列只在这里被写入 objKey，读取时由 storage.URL() 拼完整 URL。
func (h *Handler) UploadAvatar(c *gin.Context) {
	operation := "UploadAvatar"
	id, err := param.Parse[uint](c, "id")
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	// 同 UpdateUser：只能上传自己的头像。
	userid := middleware.GetUserID(c)
	if id != userid {
		response.ErrorFrom(c, operation, codeErrors.ErrCodeForbidden)
		return
	}

	// FormFile 从 multipart 请求中取出名为 "avatar" 的文件（前端表单字段名必须对应）。
	// file 是 multipart.File（io.Reader），fileHander 含文件名、大小、请求头等信息。
	file, fileHander, err := c.Request.FormFile("avatar")
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}
	defer file.Close() // 方法结束时释放文件句柄

	// upload.Validate：通用上传校验工具。
	//   - MaxSize 4<<20 = 4MB，超限 → ErrCodeFileTooLarge（HTTP 413）
	//   - AllowedMIME：嗅探出的真实类型白名单（防伪装文件）
	//   - AllowedExt：嗅探识别不出（octet-stream）时的扩展名兜底
	// 校验通过后内部已把文件指针 Seek 回起点，返回的 contentType 是"真实类型"（非客户端声明），
	// 因此下面的 Service 调用可以直接复用 file，无需重新读取。
	contentType, ext, err := upload.Validate(file, fileHander.Filename, fileHander.Size, upload.Config{
		MaxSize:     4 << 20,
		AllowedMIME: []string{"image/jpeg", "image/png", "image/gif", "image/webp"},
		AllowedExt:  []string{"jpeg", "png", "gif", "webp","jpg"},
	})
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}

	// 直接传 file（流式上传）：Validate 已重置指针，fileHander.Size 作为上传大小。
	// Service 内部生成 objKey（avatar/{id}.{ext}）存 MinIO + 更新数据库，响应里返回完整 URL。
	resp, err := h.svc.UploadAvatar(c.Request.Context(), id, file, fileHander.Size, contentType, ext)
	if err != nil {
		response.ErrorFrom(c, operation, err)
		return
	}
	response.Success(c, resp)
}
