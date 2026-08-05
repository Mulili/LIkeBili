// Package user 提供用户模块的业务逻辑层（Service）。
// 职责：查询用户信息、修改个人资料（昵称/简介）、上传头像。
// 调用链：Handler → Service → Repository →（GORM / MinIO）。
//
// 关于头像（avatar）的存储约定（重要，贯穿全模块）：
//   - 数据库 users.avatar 列只存 MinIO 的对象名（objKey），如 "avatar/1.jpg"
//   - 前端永远只拿到完整 URL（由 storage.URL() 在出口统一拼接，可直接 <img src>）
//   - 头像唯一的写入入口是 UploadAvatar；UpdateUser 不碰头像字段
package user

import (
	modelsUser "LikeBili/internal/models/user"
	userrepo "LikeBili/internal/repository/user"
	codeErrors "LikeBili/pkg/errors"
	"LikeBili/pkg/storage"
	"context"
	"fmt"
	"io"
)

// Service 封装用户相关的业务逻辑。
// 通过构造函数注入两个依赖：
//   - repo：数据库访问层（users 表的增删改查）
//   - storage：MinIO 对象存储（文件上传、URL 拼接）
type Service struct {
	repo    *userrepo.Repository
	storage *storage.MinIO
}

// NewService 创建用户服务实例。
// 参数：repo（用户数据仓库）、storage（对象存储客户端）。
func NewService(repo *userrepo.Repository, storage *storage.MinIO) *Service {
	return &Service{repo: repo, storage: storage}
}

// GetUser 查询指定用户的公开信息。
// 参数：
//   - userid: 目标用户 ID
//
// 返回：
//   - *UserInfoResp: 用户公开信息（不含邮箱、密码等敏感字段）
//   - error: 用户不存在时返回 ErrUserNotFound
//
// 注意：返回的 Avatar 是完整 URL（经 storage.URL 拼接），不是 objKey。
func (s *Service) GetUser(c context.Context, userid uint) (*modelsUser.UserInfoResp, error) {
	user, err := s.repo.FindById(c, userid)
	if err != nil {
		return nil, fmt.Errorf("Method:user.service.GetUser: %w", err)
	}
	if user == nil {
		return nil, fmt.Errorf("Method:user.service.GetUser: %w", codeErrors.ErrUserNotFound)
	}

	return &modelsUser.UserInfoResp{
		ID:        user.ID,
		Username:  user.Username,
		Nickname:  user.Nickname,
		Avatar:    s.storage.URL(user.Avatar), // 库里是 objKey，出口统一拼成完整 URL
		Bio:       user.Bio,
		CreatedAt: user.CreatedAt,
	}, nil
}

// UpdateUser 修改当前用户的个人资料，只支持昵称（Nickname）和简介（Bio）。
// 注意：头像不在这里修改，换头像请走 UploadAvatar 接口，
// 所以本方法里的 user.Avatar 始终是库里的原值，repo.Update 写回原值（等于没改）。
//
// req 用 *string 指针的原因：指针零值是 nil，
// 可以区分"前端没传该字段"（nil，跳过）和"前端传了空字符串"（非 nil，覆盖）。
func (s *Service) UpdateUser(c context.Context, userid uint, req *modelsUser.UpdateProfileReq) (*modelsUser.UserInfoResp, error) {
	user, err := s.repo.FindById(c, userid)
	if err != nil {
		return nil, fmt.Errorf("Method:user.service.UpdateUser: %w", err)
	}
	if user == nil {
		return nil, fmt.Errorf("Method:user.service.UpdateUser: %w", codeErrors.ErrUserNotFound)
	}

	// 局部更新：只覆盖前端传了的字段，没传的保持原值
	if req.Nickname != nil {
		user.Nickname = *req.Nickname
	}
	if req.Bio != nil {
		user.Bio = *req.Bio
	}
	if err := s.repo.Update(c, user); err != nil {
		return nil, fmt.Errorf("Method:user.service.UpdateUser: %w", err)
	}

	return &modelsUser.UserInfoResp{
		ID:        user.ID,
		Username:  user.Username,
		Nickname:  user.Nickname,
		Avatar:    s.storage.URL(user.Avatar),
		Bio:       user.Bio,
		CreatedAt: user.CreatedAt,
	}, nil
}

// UploadAvatar 上传新头像，是头像字段唯一的写入入口。
// 流程：
//  1. 文件流直接转发给 MinIO（不落本地磁盘），得到对象名 objKey
//  2. 把 objKey 写进 users.avatar 列（数据库只存 key，不存二进制、不存 URL）
//  3. 返回时用 storage.URL 把 objKey 拼成完整 URL，供前端直接展示
//
// 参数：
//   - reader: 文件内容流（handler 从 multipart 表单里打开）
//   - size: 文件字节数
//   - contentType: MIME 类型，如 "image/jpeg"
//   - ext: 文件扩展名，如 "jpg"
func (s *Service) UploadAvatar(c context.Context, id uint, reader io.Reader, size int64, contentType, ext string) (*modelsUser.UserInfoResp, error) {
	user, err := s.repo.FindById(c, id)
	if err != nil {
		return nil, fmt.Errorf("Method:user.service.UploadAvatar: %w", err)
	}
	if user == nil {
		return nil, fmt.Errorf("Method:user.service.UploadAvatar: %w", codeErrors.ErrUserNotFound)
	}

	// 对象名规则：avatar/{用户ID}.{扩展名}。
	// 同一扩展名重复上传会覆盖旧文件；换扩展名会生成新对象，旧文件不会被自动清理。
	objKey := fmt.Sprintf("avatar/%d.%s", id, ext)
	if err := s.storage.UploadFile(c, objKey, reader, size, contentType); err != nil {
		return nil, fmt.Errorf("Method:user.service.UploadAvatar: %w", err)
	}
	user.Avatar = objKey // 数据库只存 objKey
	if err := s.repo.Update(c, user); err != nil {
		return nil, fmt.Errorf("Method:user.service.UploadAvatar: %w", err)
	}

	return &modelsUser.UserInfoResp{
		ID:        user.ID,
		Username:  user.Username,
		Nickname:  user.Nickname,
		Avatar:    s.storage.URL(user.Avatar), // 出口统一拼完整 URL，前端可直接 <img src>
		Bio:       user.Bio,
		CreatedAt: user.CreatedAt,
	}, nil
}
