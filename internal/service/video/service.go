package video

import (
	modelsVideo "LikeBili/internal/models/video"
	rpvideo "LikeBili/internal/repository/video"
	"LikeBili/pkg/storage"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/redis/go-redis/v9"
)

type Service struct {
	repo    *rpvideo.Repository
	rdb     *redis.Client
	storage *storage.MinIO
	//降级路径
	transcodePublisher func(videoID uint) error
}

type UploadVideoInput struct {
	UserID      uint
	Title       string
	Description string
	CategoryID  uint32 // 模型里是 uint32，别用 uint

	File        io.Reader // 视频流（Handler 校验完指针已复位）
	FileSize    int64
	ContentType string // 嗅探出的真实类型
	Ext         string // 嗅探出的扩展名

	CoverFile        io.Reader // 封面，可选
	CoverSize        int64
	CoverContentType string
	CoverExt         string
}

type Option func(*Service)

func WithTranscodePublisher(fn func(videoID uint) error) Option {
	return func(s *Service) { s.transcodePublisher = fn }
}

func NewService(repo *rpvideo.Repository, rdb *redis.Client, storage *storage.MinIO, opts ...Option) *Service {
	s := &Service{repo: repo, rdb: rdb, storage: storage}
	for _, opt := range opts {
		opt(s)
	}
	return s
}
func (s *Service) UploadVideo(c context.Context, input *UploadVideoInput) (*modelsVideo.VideoResp, error) {
	objkey := fmt.Sprintf("videos/%d/%d.%s", input.UserID, time.Now().UnixNano(), input.Ext)
	s.storage.UploadVideo(c, objkey, input.File, input.FileSize, input.ContentType)

	return nil, nil
}
