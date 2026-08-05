// Package param 提供从 gin 请求中解析参数的通用工具。
// 目前支持从 URL 路径参数（Path Param）解析 uint/int/float64/bool 等基本类型，
// 后续需要解析 query 参数或请求体字段时，可在此包内扩展。
package param

import (
	codeErrors "LikeBili/pkg/errors"
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"
)

// Parse 从 URL 路径参数解析指定基本类型的值（泛型实现）。
//
// 用法：把目标类型写在方括号里，返回值就是该类型，调用方无需类型断言：
//
//	id, err := param.Parse[uint](c, "id")      // id 是 uint
//	page, err := param.Parse[int](c, "page")    // page 是 int
//	ratio, err := param.Parse[float64](c, "r")  // ratio 是 float64
//	flag, err := param.Parse[bool](c, "flag")   // flag 是 bool
//
// 支持的类型：uint、int、float64、bool；传入其他类型返回 error。
// 解析失败时，统一包装为业务错误 CodeInvalid（对应 HTTP 400），
// 调用方拿到 err 后直接 response.ErrorFrom(c, operation, err) 输出即可。
func Parse[T any](c *gin.Context, name string) (T, error) {
	// 拿到 T 的零值后做类型断言 switch，在泛型里实现"按目标类型分发"
	var zero T
	s := c.Param(name)

	// 解析结果统一暂存到 v，错误集中到末尾统一判断，避免每个 case 重复处理
	var v any
	var err error
	switch any(zero).(type) {
	case uint:
		// ParseUint 返回 uint64，base=10（URL 参数是十进制），bitSize=64
		var u uint64
		u, err = strconv.ParseUint(s, 10, 64)
		v = uint(u)
	case int:
		var i int64
		i, err = strconv.ParseInt(s, 10, 64)
		v = int(i)
	case float64:
		v, err = strconv.ParseFloat(s, 64)
	case bool:
		v, err = strconv.ParseBool(s)
	default:
		// 不支持的参数类型属于程序内部使用错误，返回普通 error（ErrorFrom 会兜底 500）
		return zero, errors.New("param.Parse: 不支持的参数类型")
	}

	// 统一的错误判断：把 strconv 原始错误包装成业务错误，携带可读消息
	if err != nil {
		return zero, codeErrors.Wrap(err, codeErrors.CodeInvalid, "参数格式错误")
	}
	// type switch 已确认 v 与 T 一致，这个断言必然成功
	return v.(T), nil
}
