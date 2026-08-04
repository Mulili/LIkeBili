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
	OK              = 200
	BadRequest      = 400
	Unauthorized    = 401
	Forbidden       = 403
	NotFound        = 404
	Conflict        = 409
	Internal        = 500
	TooManyRequests = 429
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
	CodeInternal              = 50000
	//favorite状态码
	CodeFavoriteNotFound  = 60001
	CodeFavoriteForbidden = 60002
	//JWT状态码
	CodeTokenInvalid = 20001
	CodeTokenExpired = 20002
	CodeUnauthorized = 20003
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
	ErrUnauthorized      = &Error{Code: CodeUnauthorized, Message: "未授权，请先登录"}
	ErrCodeUserIsBan     = &Error{Code: CodeUserIsBan, Message: "该用户已被封禁"}
	// 注册竞态兜底错误：并发注册时唯一索引冲突（MySQL 1062）统一返回该错误
	ErrUsernameOrEmailExists = &Error{Code: CodeUsernameOrEmailExists, Message: "用户名或邮箱重复"}
	ErrCodeInvalid           = &Error{Code: CodeInvalid, Message: "输入的参数无法校验"}
	// ---- HTTP 层标准状态码对应的默认提示 ----
	// 命名用 Error 前缀，避免与上方业务哨兵（如 ErrUnauthorized）重名。
	ErrorBadRequest      = &Error{Code: BadRequest, Message: "请求参数错误"}
	ErrorUnauthorized    = &Error{Code: Unauthorized, Message: "未授权，请先登录"}
	ErrorForbidden       = &Error{Code: Forbidden, Message: "无权限访问"}
	ErrorNotFound        = &Error{Code: NotFound, Message: "资源不存在"}
	ErrorConflict        = &Error{Code: Conflict, Message: "资源冲突"}
	ErrorInternal        = &Error{Code: Internal, Message: "服务器内部错误"}
	ErrorTooManyRequests = &Error{Code: TooManyRequests, Message: "请求过于频繁，请稍后再试"}
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
