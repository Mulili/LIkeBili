// Package storage 封装 MinIO 对象存储客户端。
//
// MinIO 是什么？
// MinIO 是一个开源的对象存储服务，兼容 Amazon S3 协议。
// 你可以把它理解成"自己搭建的阿里云OSS/腾讯云COS"。
// 它用来存文件（图片、视频、用户头像等），而不适合存结构化数据（那是数据库的事）。
//
// 工作流程：
//  1. 文件从客户端传到你的 Go 后端
//  2. Go 后端通过本包把文件上传到 MinIO 服务器
//  3. MinIO 返回一个文件 URL，后端把这个 URL 存到数据库里
//  4. 前端直接通过这个 URL 展示图片/视频
//
// 为什么项目要用"对象存储"而不是存本地磁盘？
//   - 本地磁盘扩容困难，且服务重启/迁移时文件容易丢失
//   - MinIO 天生支持海量文件、可以横向扩展（多台机器组成集群）
//   - 文件和数据库解耦：数据库只存一个 URL 字符串，不存二进制数据
package storage

import (
	"context"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"LikeBili/pkg/config"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// MinIO 这个结构体是 MinIO 客户端的"包装器"。
//
// 为什么需要这个结构体？
// 因为项目中多个地方（用户头像、视频封面等）都需要上传/下载文件，
// 如果每个地方都自己创建 MinIO 客户端，代码会大量重复。
// 所以把客户端实例存在这个结构体里，项目启动时创建一次，到处使用。
//
// 类比：就像 *gorm.DB 是数据库的"连接池"，这个 MinIO 结构体就是 MinIO 的"客户端句柄"。
type MinIO struct {
	// client 是 MinIO 官方 SDK 提供的客户端对象。
	// 它负责真正地跟 MinIO 服务器通信（发 HTTP 请求）。
	// 这个字段不对外暴露，外部代码只能通过下面的方法间接使用。
	client *minio.Client

	// bucketName 是"存储桶"的名字。
	// 存储桶（bucket）可以理解成"一个文件夹"或"一个命名空间"。
	// 所有文件都必须归属于某个 bucket。比如你可以建一个叫 "likebili" 的 bucket，项目所有文件都放里面。
	// 如果用阿里云 OSS 类比，bucket 就是 OSS 里的 Bucket。
	bucketName string

	// endpoint 是 MinIO 服务器的内网地址，格式为 "ip:端口"。
	// 因为你的 Go 后端和 MinIO 通常部署在同一台机器或同一个内网，所以用内网地址通信（更快、更安全）。
	// 示例值： "192.168.11.100:9000"
	endpoint string

	// publicEndpoint 是 MinIO 的公网可访问地址。
	// 区别于上面的 endpoint（内网通信用），publicEndpoint 用来生成文件的公开 URL，
	// 前端浏览器通过这个 URL 直接加载图片/视频。
	// 如果 MinIO 和内网地址一致，这两个值可以相同。
	// 示例值： "192.168.11.100:9000"
	publicEndpoint string

	// useSSL 表示是否使用 HTTPS 连接 MinIO。
	// 本地开发通常 false（HTTP），生产环境建议 true（HTTPS）。
	useSSL bool
}

// New 是 MinIO 结构体的"构造函数"。
//
// 做什么：
//  1. 根据配置创建 minio.Client（相当于连接 MinIO 服务器）
//  2. 检查 config.yml 里配置的 bucket 是否存在
//  3. 如果 bucket 不存在，自动创建它（省得你去 Web 控制台手动创建）
//
// 参数 cfg：项目的全局配置，里面包含了 MinIO 的连接信息（endpoint、key、bucket 等）。
//
// 返回值：
//   - *MinIO：创建成功的客户端包装器
//   - error：如果连接失败或 bucket 创建失败，返回错误
//
// 使用方式（在 main.go 中）：
//
//	cfg := config.InitConfig()
//	minioClient, err := storage.New(cfg)
//	if err != nil {
//	    log.Fatalf("初始化 MinIO 失败: %v", err)
//	}
func New(cfg *config.Config) (*MinIO, error) {
	// --- 第一步：创建 MinIO SDK 客户端 ---

	// minio.New() 是官方 SDK 提供的函数，作用是根据地址和认证信息创建一个客户端。
	// 第一个参数是 MinIO 服务器的地址（内网），第二个参数是配置选项。
	client, err := minio.New(cfg.MinioEndpoint, &minio.Options{
		// Creds 填用户名和密码，相当于告诉 MinIO "我是谁，我有权限操作"。
		// NewStaticV4 表示使用固定的密钥对（AccessKey + SecretKey）。
		// 第三个参数是 token，通常不用填（留空字符串即可）。
		Creds: credentials.NewStaticV4(cfg.MinioAccessKey, cfg.MinioSecretKey, ""),

		// Secure 表示是否使用 HTTPS。
		// 如果 cfg.MinioUseSSL 是 true，SDK 会用 https:// 连接 MinIO。
		Secure: cfg.MinioUseSSL,
	})
	if err != nil {
		// %w 是 Go 的错误包装语法，保留原始错误信息的同时加上上下文说明。
		return nil, fmt.Errorf("failed to create minio client: %w", err)
	}

	// --- 第二步：检查 bucket 是否存在，不存在则自动创建 ---

	// context.Background() 创建一个空的上下文。
	// 这里没有超时控制，因为 bucket 检查是初始化阶段的操作，一般很快完成。
	ctx := context.Background()

	// BucketExists 查询 MinIO 服务器上是否已经存在叫 cfg.MinioBucket 的 bucket。
	// 返回 true 表示 bucket 已存在，false 表示不存在。
	exists, err := client.BucketExists(ctx, cfg.MinioBucket)
	if err != nil {
		// 如果查询失败（比如 MinIO 服务器没启动、网络不通），直接返回错误。
		return nil, fmt.Errorf("failed to check bucket existence: %w", err)
	}

	if !exists {
		// bucket 不存在，我们主动创建它。
		// MakeBucketOptions{} 是创建 bucket 的额外选项，这里全部用默认值。
		err = client.MakeBucket(ctx, cfg.MinioBucket, minio.MakeBucketOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to create bucket %s: %w", cfg.MinioBucket, err)
		}
		// 创建成功后打印一条日志，方便你在控制台看到。
		log.Printf("[MinIO] Bucket '%s' created successfully", cfg.MinioBucket)
	}

	// --- 第三步：返回包装好的 MinIO 实例 ---

	// 把 SDK 客户端和配置信息打包到 MinIO 结构体中返回。
	// 后续所有上传/删除/URL 生成方法都会用到这些信息。
	return &MinIO{
		client:         client,
		bucketName:     cfg.MinioBucket,
		endpoint:       cfg.MinioEndpoint,
		publicEndpoint: cfg.MinioPublicEndpoint,
		useSSL:         cfg.MinioUseSSL,
	}, nil
}

// UploadFile 将"内存中的数据流"上传到 MinIO 的默认存储桶。
//
// 和之前的 FPutObject（从本地文件上传）不同：
// FPutObject 需要先把文件存到服务器磁盘，再传给 MinIO，多了一步磁盘 IO。
// 而 PutObject 直接接收一个 io.Reader（数据流），
// 可以在不落盘的情况下直接把请求体里的字节流转发给 MinIO，性能更好。
//
// 参数说明：
//   - ctx:         上下文，可以控制超时（比如 30 秒上传超时）
//   - objectName:  文件在 MinIO 中的"路径名"，例如 "images/avatar/1.jpg"
//   - reader:      文件内容的读取器。Gin 框架中可以用 c.Request.Body 直接传入
//   - size:        文件字节数。Gin 可以用 c.Request.ContentLength 获取
//   - contentType: 文件的 MIME 类型，例如 "image/jpeg"、"video/mp4"
//
// 为什么这个项目用 reader 而不是本地文件路径？
// 因为本项目是 Web 服务，文件是"客户端直接上传到后端"的，后端拿到的本来就是一个数据流，
// 没必要先写到磁盘再读出来，直接用 PutObject 转发最合理。
func (m *MinIO) UploadFile(ctx context.Context, objectName string, reader io.Reader, size int64, contentType string) error {
	// PutObject 是 SDK 的上传方法，第一个参数是 bucket，第二个是对象路径，
	// 第三个是数据流（reader），第四个是文件大小。
	_, err := m.client.PutObject(ctx, m.bucketName, objectName, reader, size, minio.PutObjectOptions{
		// ContentType 告诉浏览器这个文件是什么类型，渲染时才能正确处理。
		// 比如上传一个 .jpg 但不设 ContentType，浏览器可能不会直接展示图片。
		ContentType: contentType,
	})
	if err != nil {
		return fmt.Errorf("failed to upload file to minio: %w", err)
	}
	return nil
}

// GetPresignedURL 生成一个"预签名 URL"，允许别人在指定时间内临时访问私有文件。
//
// 为什么要预签名？
// 有些文件不希望永久公开（比如用户的私有视频、草稿），
// 但直接访问又需要认证，此时可以签发一个带签名的临时链接：
// 链接里包含了访问凭证，拿到链接的人无需登录就能在有效期内下载。
// 过期后链接自动失效，既安全又方便。
//
// 使用场景：
//   - 用户分享一个"仅 7 天内有效"的视频链接
//   - 前端下载私有文件（不公开的文件）
//
// 参数说明：
//   - ctx:     上下文
//   - objectName: 文件在 MinIO 中的路径名
//   - expiry:  有效期，例如 7*24*time.Hour 表示 7 天
//
// 返回值：
//   - string：带签名的完整 URL，直接给前端即可
//   - error：生成失败时返回错误
func (m *MinIO) GetPresignedURL(ctx context.Context, objectName string, expiry time.Duration) (string, error) {
	// PresignedGetObject 是 SDK 生成预签名 URL 的方法。
	// nil 是"请求参数"（如响应头），一般用不到。
	url, err := m.client.PresignedGetObject(ctx, m.bucketName, objectName, expiry, nil)
	if err != nil {
		return "", fmt.Errorf("storage.GetPresignedURL: %w", err)
	}
	return url.String(), nil
}

// GetObjectURL 生成文件的"永久公开"访问 URL。
//
// 和 GetPresignedURL 的区别：
//   - GetObjectURL:     永久有效，适合公开内容（头像、视频封面、公开视频）
//   - GetPresignedURL:  临时有效，适合私有内容（私密视频、未发布草稿）
//
// 前提：这个 bucket 必须配置了公开读取（readonly）权限，
// 否则即使生成了 URL，浏览器访问也会被 MinIO 拒绝。
// 在 MinIO Web 控制台 → Bucket → Access Rules 中设置 readonly。
func (m *MinIO) GetObjectURL(objectName string) string {
	// 根据是否启用 SSL 决定使用 http 还是 https
	scheme := "http"
	if m.useSSL {
		scheme = "https"
	}
	// URL 格式：协议://公网地址/bucket名/对象路径
	// 例如：http://192.168.11.100:9000/likebili/images/avatar/1.jpg
	return fmt.Sprintf("%s://%s/%s/%s", scheme, m.publicEndpoint, m.bucketName, objectName)
}

// Delete 从 MinIO 的默认存储桶中删除一个文件。
//
// 使用场景：
//   - 用户替换头像：删除旧头像文件
//   - 用户删除视频：删除关联的封面图或视频文件
//
// 参数 objectName：要删除的文件路径（与上传时的 objectName 对应）。
//
// 使用示例：
//
//	err := minioClient.Delete(ctx, "images/avatar/old_1.jpg")
func (m *MinIO) Delete(ctx context.Context, objectName string) error {
	// RemoveObject 是 SDK 提供的方法，删除指定 bucket 中的指定对象。
	err := m.client.RemoveObject(ctx, m.bucketName, objectName, minio.RemoveObjectOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete object from minio: %w", err)
	}
	return nil
}

// DeletePrefix 按对象名前缀批量删除（ListObjects 枚举 + RemoveObjects 批量删）。
//
// 为什么需要它：转码产物是"一个目录"——比如一个 720p 档位包含
// index.m3u8 + 几十个 ts 分片，它们都挂在同一个前缀（如 "videos/5/720p/"）下。
// 单独用 Delete 只能删一个对象，要删一整个目录必须按前缀枚举后批量删。
//
// 参数 prefix：对象名前缀，必须以 "/" 结尾（如 "videos/5/720p/"），
// 删除该前缀下的全部对象（含 m3u8 与所有 ts 分片）。
func (m *MinIO) DeletePrefix(ctx context.Context, prefix string) error {
	// ① 枚举：把前缀下的所有对象投递进 channel（ListObjects 支持前缀 + 递归）
	objectsCh := make(chan minio.ObjectInfo)
	go func() {
		defer close(objectsCh)
		for obj := range m.client.ListObjects(ctx, m.bucketName, minio.ListObjectsOptions{
			Prefix:    prefix,
			Recursive: true,
		}) {
			if obj.Err != nil {
				continue // 单个对象枚举出错跳过，不中断整体
			}
			objectsCh <- obj
		}
	}()
	// ② 批量删除：RemoveObjects 消费 channel，逐个删除并返回失败项
	for errInfo := range m.client.RemoveObjects(ctx, m.bucketName, objectsCh, minio.RemoveObjectsOptions{}) {
		if errInfo.Err != nil {
			return fmt.Errorf("storage.DeletePrefix(prefix=%s): %w", prefix, errInfo.Err)
		}
	}
	return nil
}

// UploadVideo 将视频数据流上传到 MinIO，专用于视频文件。
//
// 注意：当前实现与 UploadFile 完全一致，两者是重复代码。
// 建议后续要么删除本方法统一用 UploadFile，要么在这里扩展视频特有的逻辑
// （例如：视频转码回调、单独的视频桶、更大的超时时间等）。
func (m *MinIO) UploadVideo(ctx context.Context, objectName string, reader io.Reader, size int64, contentType string) error {
	_, err := m.client.PutObject(ctx, m.bucketName, objectName, reader, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return fmt.Errorf("failed to upload file to minio: %w", err)
	}
	return nil
}

// URL 把对象名（objKey）转换为可公开访问的完整 URL。
// 这是"判断是否为空 + GetObjectURL"的复用方法，调用方无需再写 if != "" 判断。
// 兼容两种输入：
//   - objKey（如 "avatar/1.jpg"）：走 GetObjectURL 拼接完整 URL
//   - 已是完整 URL（以 http:// 或 https:// 开头）：原样返回，避免二次包装成畸形 URL
//
// 空字符串直接返回空字符串。
func (m *MinIO) URL(objectKey string) string {
	if objectKey == "" {
		return ""
	}
	if strings.HasPrefix(objectKey, "http://") || strings.HasPrefix(objectKey, "https://") {
		return objectKey
	}
	return m.GetObjectURL(objectKey)
}
