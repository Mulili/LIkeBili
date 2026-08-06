// Package upload 提供文件上传前的通用校验工具。
//
// 设计思路：上传文件的安全问题不能信任"文件名扩展名"或"客户端声明的 Content-Type"，
// 这两者都可以被伪造。所以本包采用"嗅探真实内容"——读文件头 512 字节，按字节特征
// 判断真实类型——来把关；嗅探识别不出时（如部分 mp4），再回退到扩展名兜底。
//
// 纯工具设计：只做校验和格式识别，不写 HTTP 响应，错误由调用方（Handler）决定怎么返回。
package upload

import (
	codeErrors "LikeBili/pkg/errors"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
)

// Config 定义一组上传校验规则。
//   - MaxSize：允许的最大字节数，0 表示不限制（如 4<<20 = 4MB）
//   - AllowedMIME：嗅探识别出的"真实类型"白名单（如 image/jpeg、video/webm）
//   - AllowedExt：嗅探识别不出时，按扩展名兜底的白名单（如 mp4、mov）
type Config struct {
	MaxSize     int64
	AllowedMIME []string
	AllowedExt  []string
}

// UploadValidate 校验上传文件：先查大小，再嗅探文件头判断真实格式。
// 返回：
//   - contentType：校验通过后的真实 MIME 类型（嗅探结果），可直接作 storage 上传的 ContentType
//   - err：业务错误（ErrCodeFileTooLarge / ErrCodeFileFormatInvalid）或读取失败
//
// 注意：校验通过后会把文件指针 Seek 回起点，调用方可直接把整个 file 交给 storage 上传，一个字节不丢。
func Validate(file multipart.File, filename string, size int64, cfg Config) (contentType string, ext string, err error) {
	// 1. 大小校验：超过配置上限直接拒绝
	if cfg.MaxSize > 0 && size > cfg.MaxSize {
		return "", "", codeErrors.ErrCodeFileTooLarge
	}

	// 2. 嗅探：读文件头 512 字节（HTTP 嗅探规范），按字节特征识别真实类型
	buffer := make([]byte, 512)
	n, err := file.Read(buffer)
	if err != nil && err != io.EOF {
		return "", "", err
	}
	contentType = http.DetectContentType(buffer[:n])

	// 3. 格式校验
	// 从原始文件名中提取"纯扩展名"（小写、不带点），用于和下面的白名单匹配。
	//
	// 教学示例：假设前端传的文件名是 "my.photo.JPEG"（文件名含多个点、扩展名大写）
	//   ① filepath.Ext("my.photo.JPEG") → ".JPEG"
	//      filepath.Ext 取"最后一个点"及其后的部分；文件名里无论有多少个点，都只看最后那个点
	//   ② strings.TrimPrefix(".JPEG", ".") → "JPEG"
	//      删除开头的 "."，使扩展名不再带点，才能和白名单（不带点）比较
	//   ③ strings.ToLower("JPEG") → "jpeg"
	//      大写转小写，和白名单里的 "jpeg" 精确匹配
	//   结果：ext = "jpeg" ✅
	//
	// 三个方法的参数说明：
	//   filepath.Ext(filename)      filename = 上传文件的原始文件名（来自 fileHander.Filename）
	//   strings.TrimPrefix(s, ".")  s = ① 的结果；"." = 要删除的前缀
	//   strings.ToLower(s)          s = ② 的结果
	// 为什么去点 / 小写：白名单 cfg.AllowedExt 存的是不带点的小写扩展名（"jpg"），
	// 而 filepath.Ext 返回带点且大小写可能不统一，所以必须去点 + 小写后才能精确匹配。
	ext = strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), "."))
	switch {
	case contentType == "application/octet-stream":
		// 嗅探识别不出（部分 mp4/mov 会这样）：回退到扩展名白名单
		if !slices.Contains(cfg.AllowedExt, ext) {
			return "", "", codeErrors.ErrCodeFileFormatInvalid
		}
	case !slices.Contains(cfg.AllowedMIME, contentType):
		// 嗅探出了明确类型但不在白名单（如伪装成 .jpg 的可执行文件）→ 拒绝
		return "", "", codeErrors.ErrCodeFileFormatInvalid
	}

	// 4. 重置指针：嗅探读走的字节必须能重新读，否则上传的文件会缺头
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", "", err
	}

	return contentType, ext, nil
}
