package codeErrors

import "errors"

type Error struct {
	Code    int    // 业务错误码
	Message string // 对外展示的消息
	Err     error  // 内部原始 error（可选，用于日志）
}

// Error 实现 error 接口，使 *Error 能被 fmt.Errorf("%w", ...) 挂入错误链，
// 也能被 errors.As 从错误链中提取出来。这是整个 codeErrors 错误体系能工作的前提。
func (e *Error) Error() string {
	return e.Message
}

const (
	// HTTP 标准状态码
	OK                 = 200
	BadRequest         = 400
	Unauthorized       = 401
	Forbidden          = 403
	NotFound           = 404
	Conflict           = 409
	Internal           = 500
	TooManyRequests    = 429
	ServiceUnavailable = 503
	//auth状态码
	CodeUsernameExists    = 10001
	CodeEmailExists       = 10002
	CodeUserNotFound      = 10003
	CodeWrongPassword     = 10004
	CodePasswordsNotMatch = 10005
	CodeUserIsBan         = 10006
	// 并发注册时，用户名/邮箱的预检查存在竞态窗口，最终由数据库唯一索引兜底。
	// 命中后统一返回该错误，不区分具体是哪个字段重复。
	CodeUsernameOrEmailExists = 10007
	CodeInvalid               = 10008
	CodeForbidden             = 10009
	CodeInternal              = 50000
	//JWT状态码
	CodeTokenInvalid = 20001
	CodeTokenExpired = 20002
	CodeUnauthorized = 20003
	//文件字节流状态码
	CodeFileTooLarge      = 30001 // 文件大小超过限制
	CodeFileFormatInvalid = 30002 // 文件格式/类型不支持
	CodeFileEmpty         = 30003 // 文件内容为空
	CodeFileTooMany       = 30004 // 上传的文件数量超过限制
	CodeFileUploadFailed  = 30005 // 文件写入对象存储失败
	//video状态码
	CodeVideoNotFound        = 40001
	CodeVideoForbidden       = 40002
	CodeVideoStatusForbidden = 40003
	CodeVideoTransFailed     = 40004
	CodeVideoTransNotReady   = 40005
	CodeVideoNotPass         = 40006
	CodeTaskNotFound         = 40007
	//favorite状态码
	CodeFavoriteNotFound  = 60001
	CodeFavoriteForbidden = 60002
)

var (
	ErrUsernameExists    = &Error{Code: CodeUsernameExists, Message: "用户名已存在"}
	ErrEmailExists       = &Error{Code: CodeEmailExists, Message: "邮箱已被注册"}
	ErrUserNotFound      = &Error{Code: CodeUserNotFound, Message: "用户不存在"}
	ErrWrongPassword     = &Error{Code: CodeWrongPassword, Message: "密码错误"}
	ErrPasswordsNotMatch = &Error{Code: CodePasswordsNotMatch, Message: "两次密码不一致"}
	ErrFavoriteNotFound  = &Error{Code: CodeFavoriteNotFound, Message: "收藏夹不存在"}
	ErrFavoriteForbidden = &Error{Code: CodeFavoriteForbidden, Message: "无权访问该收藏夹"}
	ErrTokenInvalid      = &Error{Code: CodeTokenInvalid, Message: "令牌无效"}
	ErrTokenExpired      = &Error{Code: CodeTokenExpired, Message: "令牌已过期"}
	ErrUnauthorized      = &Error{Code: CodeUnauthorized, Message: "无法获取资源，请先登录"}
	ErrCodeUserIsBan     = &Error{Code: CodeUserIsBan, Message: "该用户已被封禁"}
	// 注册竞态兜底错误：并发注册时唯一索引冲突（MySQL 1062）统一返回该错误
	ErrUsernameOrEmailExists = &Error{Code: CodeUsernameOrEmailExists, Message: "用户名或邮箱重复"}
	ErrCodeInvalid           = &Error{Code: CodeInvalid, Message: "输入的参数无法校验"}
	// ---- 文件字节流类（3xxxx）----
	ErrCodeFileTooLarge      = &Error{Code: CodeFileTooLarge, Message: "文件大小超过限制"}
	ErrCodeFileFormatInvalid = &Error{Code: CodeFileFormatInvalid, Message: "不支持的文件格式"}
	ErrCodeFileEmpty         = &Error{Code: CodeFileEmpty, Message: "文件内容为空"}
	ErrCodeFileTooMany       = &Error{Code: CodeFileTooMany, Message: "上传的文件数量超过限制"}
	ErrCodeFileUploadFailed  = &Error{Code: CodeFileUploadFailed, Message: "文件上传失败"}
	// ---- 视频类（4xxxx）----
	ErrVideoNotFound        = &Error{Code: CodeVideoNotFound, Message: "视频不存在"}
	ErrVideoForbidden       = &Error{Code: CodeVideoForbidden, Message: "无权访问该视频"}
	ErrVideoStatusForbidden = &Error{Code: CodeVideoStatusForbidden, Message: "非管理员无权通过审核"}
	ErrVideoTransFailed     = &Error{Code: CodeVideoTransFailed, Message: "视频转码失败,请重新上传"}
	ErrVideoTransNotReady   = &Error{Code: CodeVideoTransNotReady, Message: "视频转码尚未完成,请稍后重试"}
	ErrVideoNotPass         = &Error{Code: CodeVideoNotPass, Message: "未审核通过的视频"}
	ErrTaskNotFound         = &Error{Code: CodeTaskNotFound, Message: "未查询到转码状态"}
	// ---- HTTP 层标准状态码对应的默认提示 ----
	// 命名用 Error 前缀，避免与上方业务哨兵（如 ErrUnauthorized）重名。
	ErrorBadRequest      = &Error{Code: BadRequest, Message: "请求参数错误"}
	ErrorUnauthorized    = &Error{Code: Unauthorized, Message: "未授权，请先登录"}
	ErrCodeForbidden     = &Error{Code: CodeForbidden, Message: "无权限执行该操作"}
	ErrorNotFound        = &Error{Code: NotFound, Message: "资源不存在"}
	ErrorConflict        = &Error{Code: Conflict, Message: "资源冲突"}
	ErrorInternal        = &Error{Code: Internal, Message: "服务器内部错误"}
	ErrorTooManyRequests = &Error{Code: TooManyRequests, Message: "请求过于频繁，请稍后再试"}
	// 服务暂时不可用（503）：用于"凭证本身有效但后端暂时无法验证"的场景，
	// 与"未授权(401)"严格区分，避免前端误以为登录过期而误导用户退出重登。
	ErrorServiceUnavailable = &Error{Code: ServiceUnavailable, Message: "服务暂时不可用，请稍后重试"}
)

// 创建纯业务错误（不包裹内部 error）
func New(code int, msg string) *Error {
	return &Error{Code: code, Message: msg}
}

// 包裹内部 error，用于 Service 层记录原始错误
func Wrap(err error, code int, msg string) *Error {
	return &Error{Code: code, Message: msg, Err: err}
}

// 从 error 中提取 *Error，用于 Handler 层判断
func From(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}
