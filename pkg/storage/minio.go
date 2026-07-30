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
package storage

import (
	"context"
	"fmt"
	"log"

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
	// 这个字段不对外暴露，外部代码只能通过下面的 Upload / Delete / GetFileURL 方法间接使用。
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
	// 后续 Upload / Delete / GetFileURL 都会用到这些信息。
	return &MinIO{
		client:         client,
		bucketName:     cfg.MinioBucket,
		endpoint:       cfg.MinioEndpoint,
		publicEndpoint: cfg.MinioPublicEndpoint,
		useSSL:         cfg.MinioUseSSL,
	}, nil
}

// Upload 上传一个本地文件到 MinIO 的默认存储桶中。
//
// 参数说明：
//   - ctx:          上下文，可以控制超时（比如 30 秒上传超时）
//   - objectName:   文件在 MinIO 中的"路径名"，例如 "images/avatar/1.jpg"
//   - filePath:     本地文件的绝对路径，例如 "/tmp/upload_123.jpg"
//   - contentType:  文件的 MIME 类型，例如 "image/jpeg"、"video/mp4"
//
// 返回 minio.UploadInfo 包含上传后的信息（如 ETag、版本号等），通常不常用。
//
// 使用示例：
//
//	info, err := minioClient.Upload(ctx, "images/avatar/1.jpg", "/tmp/upload.jpg", "image/jpeg")
//	if err != nil {
//	    // 处理错误
//	}
//	// 上传成功后，通过 GetFileURL 获取可访问的 URL
//	url := minioClient.GetFileURL("images/avatar/1.jpg")
func (m *MinIO) Upload(ctx context.Context, objectName, filePath, contentType string) (minio.UploadInfo, error) {
	// FPutObject 是"从本地文件上传"的意思（F = File）。
	// 它会读取 filePath 指向的本地文件，上传到 MinIO。
	info, err := m.client.FPutObject(ctx, m.bucketName, objectName, filePath, minio.PutObjectOptions{
		// ContentType 告诉浏览器这个文件是什么类型，渲染时才能正确处理。
		// 比如上传一个 .jpg 但不设 ContentType，浏览器可能不会直接展示图片。
		ContentType: contentType,
	})
	if err != nil {
		return info, fmt.Errorf("failed to upload file to minio: %w", err)
	}
	return info, nil
}

// GetFileURL 生成文件对外访问的完整 URL。
//
// 为什么要有这个方法？
// MinIO 中的文件不能直接通过路径访问，需要拼接出完整的 URL。
// 这个 URL 的格式是：协议://公网地址/bucket名/对象路径
// 例如：http://192.168.11.100:9000/likebili/images/avatar/1.jpg
//
// 生成的 URL 可以直接返回给前端，前端用 <img src="..."> 就能展示。
func (m *MinIO) GetFileURL(objectName string) string {
	// 根据是否启用 SSL 决定使用 http 还是 https
	scheme := "http"
	if m.useSSL {
		scheme = "https"
	}
	// Sprintf 拼接完整的 URL
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
