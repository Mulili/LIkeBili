// Package transcode 提供视频转码的后台任务处理功能。
//
// 完整流水线（谁调用谁，一眼看清）：
//
//	上传落库 → ProcessVideo（本包唯一入口，由 main.go 注入闭包以 goroutine 调用）
//	          ├─ st.GetPresignedURL（pkg/storage）→ 拿到临时下载链接
//	          ├─ downloadFile           → 源视频拉到本地 /tmp/transcode/{videoID}/
//	          ├─ runFFProbe             → 元信息落 video_metas（saveMeta）
//	          ├─ runFFmpegHLS ×N档       → 每档 HLS 分片，编码进度经回调传回
//	          ├─ uploadFileToMinio ×N   → m3u8+ts 分片传回 MinIO
//	          ├─ saveQuality ×N档       → 档位信息落 video_qualities
//	          └─ runFFmpegCover（可选）  → 无封面时自动截第 1 秒为封面
//	所有状态变化统一走 publishAndPersist / failTask：写 transcode_tasks 表 + broker 广播。
//
// 进度模型（总体 0-100）：
//
//	下载 2% → ffprobe 10% → 每个档位均分 10~90 的区间 → 100% 完成
package transcode

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	modelsMeta "LikeBili/internal/models/meta"
	modelsQuality "LikeBili/internal/models/quality"
	modelsTrans "LikeBili/internal/models/transcode"
	"LikeBili/pkg/logger"
	"LikeBili/pkg/storage"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// transDir 是转码过程中本地临时文件的存放目录。
// 何时使用：源视频、各档 HLS 分片、封面图都先落在这里，上传完 MinIO 后整体清理。
// 按视频 ID 分子目录（见 ProcessVideo），并发转码互不覆盖。
// 注意：Windows 上绝对路径 /tmp/transcode 会解析到当前盘符根目录的 tmp 下，
// 部署到 Linux 时就是标准的 /tmp/transcode。务必保证该目录存在且有写权限。
const transDir = "/tmp/transcode"

// transcodeTargets 定义了需要转码的目标分辨率档位（从低到高）。
// 何时使用：ProcessVideo 筛选档位时遍历它，筛掉分辨率高于源视频的档位（转码不放大）。
// 只读数据：slice 无法声明为 const，约定只遍历、不得修改。
var transcodeTargets = []struct {
	Label  string
	Width  int
	Height int
}{
	{"360p", 640, 360},
	{"480p", 854, 480},
	{"720p", 1280, 720},
	{"1080p", 1920, 1080},
}

// ProcessVideo 视频转码主入口：一条完整流水线
// 查视频 → 预签名URL下载 → ffprobe 探测 → 多档 HLS 转码 → 分片上传 → 自动封面 → 完成。
// 任一步致命错误都会调 failTask：DB 置失败 + broker 广播失败后提前返回。
//
// 何时调用：上传视频落库后，由 main.go 注入的闭包包装并以 goroutine 异步调用
// （触发链：service.UploadVideo → triggerTranscode → transcodeLocal → 闭包 → 本函数）。
//
// 参数来源：
//   - videoID: 刚入库的 videos.id（repo.Create 后返回的自增 ID）
//   - db:      main.go 的全局 *gorm.DB，读写 transcode_tasks / video_metas / video_qualities / videos 四张表
//   - broker:  main.go 创建的全局 *ProgressBroker，把进度实时推给订阅者（前端）
//   - st:      main.go 创建的 *storage.MinIO，负责预签名URL下载与分片/封面上传
func ProcessVideo(videoID uint, db *gorm.DB, broker *ProgressBroker, st *storage.MinIO) {
	// 整个转码最多跑 30 分钟；ctx 传给所有网络/命令调用，超时会自动取消
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	logger.Info("转码开始", zap.Uint("video_id", videoID))

	// --- 检查 ffmpeg / ffprobe 是否可用（服务器没装就直接失败，别浪费时间） ---
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		logger.Error("ffmpeg 不存在，跳过转码", zap.Uint("video_id", videoID))
		updateStatus(db, ctx, videoID, modelsTrans.StatusFailed, 0, "ffmpeg 不可用")
		return
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		logger.Error("ffprobe 不存在，跳过转码", zap.Uint("video_id", videoID))
		updateStatus(db, ctx, videoID, modelsTrans.StatusFailed, 0, "ffprobe 不可用")
		return
	}

	// 工具可用 → 把任务状态置为"转码中"
	updateStatus(db, ctx, videoID, modelsTrans.StatusTranscode, 0, "")

	// --- 查出视频在 MinIO 里的对象名，生成 1 小时有效的预签名 URL ---
	var videoURL string
	db.Raw("SELECT video_url FROM videos WHERE id = ?", videoID).Scan(&videoURL)
	if videoURL == "" {
		failTask(db, broker, ctx, videoID, "视频不存在")
		return
	}
	inputURL, err := st.GetPresignedURL(ctx, videoURL, time.Hour)
	if err != nil {
		failTask(db, broker, ctx, videoID, fmt.Sprintf("生成预签名URL失败: %v", err))
		return
	}

	// --- 为本次转码创建独立临时目录（按视频ID隔离，避免并发转码互相覆盖） ---
	vidDir := filepath.Join(transDir, strconv.FormatUint(uint64(videoID), 10))
	if err := os.MkdirAll(vidDir, 0755); err != nil {
		failTask(db, broker, ctx, videoID, fmt.Sprintf("创建临时目录失败: %v", err))
		return
	}
	defer os.RemoveAll(vidDir) // 转码结束无论成败都清理临时目录

	// --- 下载原始视频到本地（对象名可能带预签名参数，先去掉 ? 后面部分再取扩展名） ---
	cleanPath := videoURL
	if idx := strings.Index(cleanPath, "?"); idx >= 0 {
		cleanPath = cleanPath[:idx]
	}
	ext := filepath.Ext(cleanPath)
	if ext == "" {
		ext = ".mp4" // 兜底：没扩展名就按 mp4 处理
	}
	inputFile := filepath.Join(vidDir, "input"+ext)

	broker.Publish(ProgressUpdate{VideoID: videoID, Status: modelsTrans.StatusTranscode, Progress: 2, Quality: "download"})
	if err := downloadFile(ctx, inputURL, inputFile); err != nil {
		failTask(db, broker, ctx, videoID, fmt.Sprintf("下载原始视频失败: %v", err))
		return
	}

	// --- ffprobe 提取元信息（失败不致命：用兜底分辨率继续转码） ---
	meta, err := runFFProbe(inputFile)
	if err != nil {
		logger.Warn("ffprobe 失败", zap.Uint("video_id", videoID), zap.Error(err))
		meta = &modelsMeta.VideoMeta{} // 空 meta，后面会走默认分辨率兜底
	} else {
		saveMeta(db, ctx, videoID, meta)
		// 同时把时长写进 videos 表（转码前的视频时长与源视频一致）
		db.WithContext(ctx).Table("videos").
			Where("id = ?", videoID).
			Update("duration", uint(meta.Duration))
	}

	// ffprobe 阶段完成，进度到 10%
	publishAndPersist(db, broker, ctx, videoID, modelsTrans.StatusTranscode, 10, "")

	// --- 决定实际转码档位：只转"不高于源视频分辨率"的档位（转码不放大） ---
	maxW, maxH := meta.Width, meta.Height
	if maxW == 0 || maxH == 0 {
		maxW, maxH = 1920, 1080 // ffprobe 失败时的默认兜底
	}

	var targets []struct {
		Label  string
		Width  int
		Height int
	}
	for _, t := range transcodeTargets {
		if uint(t.Height) <= maxH && uint(t.Width) <= maxW {
			targets = append(targets, t)
		}
	}
	// 源视频分辨率低于所有档位时，至少保留一个：按源分辨率转一份
	if len(targets) == 0 {
		targets = append(targets, struct {
			Label  string
			Width  int
			Height int
		}{Label: fmt.Sprintf("%dp", maxH), Width: int(maxW), Height: int(maxH)})
	}

	// --- 逐档位执行 HLS 转码 ---
	// 进度区间规划：10% 之后每档均分 80% 的进度（10%→90%），最后 100% 收尾
	totalSteps := len(targets)
	for i, target := range targets {
		// 每档一个子目录，文件名统一 index.m3u8 + seg_XXX.ts
		qualityDir := filepath.Join(vidDir, target.Label)
		if err := os.MkdirAll(qualityDir, 0755); err != nil {
			logger.Error("创建档位目录失败", zap.String("dir", qualityDir), zap.Error(err))
			continue
		}

		outputM3U8 := filepath.Join(qualityDir, "index.m3u8")
		segPattern := filepath.Join(qualityDir, "seg_%03d.ts")

		// ffmpeg 实时进度回调：把 ffmpeg 内部 0-100% 的编码进度映射到当前档位的区间
		onFfmpegProgress := func(ffmpegPct float64) {
			base := float64(10) + float64(i)*80/float64(totalSteps) // 本档位起始进度
			slotWidth := float64(80) / float64(totalSteps)          // 每档占的进度宽度
			overall := uint8(base + ffmpegPct/100.0*slotWidth)
			if overall > 99 { // 编码阶段永远不到 100，最后 100% 由完成事件发出
				overall = 99
			}
			broker.Publish(ProgressUpdate{
				VideoID:  videoID,
				Status:   modelsTrans.StatusTranscode,
				Progress: overall,
				Quality:  target.Label,
			})
		}

		// 执行 HLS 转码（同步阻塞直到该档位完成）
		if err := runFFmpegHLS(ctx, inputFile, outputM3U8, segPattern, target.Width, target.Height, meta.Duration, onFfmpegProgress); err != nil {
			logger.Error("ffmpeg HLS 转码失败", zap.Uint("video_id", videoID), zap.String("quality", target.Label), zap.Error(err))
			continue // 单档失败不中断整体流程，跳过该档继续后面的
		}

		// 把该档位的 m3u8 + 所有 ts 分片上传到 MinIO，路径规则：
		//   videos/{videoID}/{档位}/index.m3u8
		//   videos/{videoID}/{档位}/seg_000.ts ...
		entries, err := os.ReadDir(qualityDir)
		if err != nil {
			// 目录读不出来 = 该档产物不可用：不上传、不落库，避免产生"有记录无文件"的脏档位
			logger.Error("读取档位目录失败，跳过该档", zap.Uint("video_id", videoID), zap.String("dir", qualityDir), zap.Error(err))
			continue
		}
		var totalSize uint64
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			localPath := filepath.Join(qualityDir, entry.Name())
			minioObj := fmt.Sprintf("videos/%d/%s/%s", videoID, target.Label, entry.Name())
			info, err := entry.Info()
			if err != nil {
				// 单个文件信息都拿不到，上传没有意义，跳过该文件继续下一个
				logger.Warn("读取文件信息失败，跳过该文件", zap.String("file", localPath), zap.Error(err))
				continue
			}
			fileSize := info.Size()
			if err := uploadFileToMinio(ctx, st, localPath, minioObj, fileSize); err != nil {
				logger.Error("上传分片失败", zap.String("obj", minioObj), zap.Error(err))
			}
			if fileSize > 0 {
				totalSize += uint64(fileSize) // 累加该档位总大小，写入 video_qualities
			}
		}

		// 保存该档位的质量记录（m3u8 对象名 + 总大小），前端做清晰度切换用
		m3u8ObjName := fmt.Sprintf("videos/%d/%s/index.m3u8", videoID, target.Label)
		saveQuality(db, ctx, videoID, target.Label, m3u8ObjName, totalSize)

		// 该档位完成，广播子阶段进度
		qualProgress := uint8(10 + (i+1)*80/totalSteps)
		publishAndPersist(db, broker, ctx, videoID, modelsTrans.StatusTranscode, qualProgress, "")
	}

	// --- 自动封面：视频还没封面时，从第 1 秒截一帧作为封面 ---
	var coverURL string
	db.Raw("SELECT cover_url FROM videos WHERE id = ?", videoID).Scan(&coverURL)
	if coverURL == "" {
		coverPath := filepath.Join(vidDir, "cover.jpg")
		coverObjName := fmt.Sprintf("videos/%d/cover_auto_%d.jpg", videoID, videoID)
		if err := runFFmpegCover(ctx, inputFile, coverPath); err == nil {
			info, _ := os.Stat(coverPath)
			var sz int64
			if info != nil {
				sz = info.Size()
			}
			if err := uploadFileToMinio(ctx, st, coverPath, coverObjName, sz); err == nil {
				// 只更新 cover_url 为空的记录，避免覆盖用户手动上传的封面
				db.WithContext(ctx).Table("videos").
					Where("id = ? AND (cover_url = '' OR cover_url IS NULL)", videoID).
					Update("cover_url", coverObjName)
			}
		} else {
			logger.Warn("封面提取失败", zap.Uint("video_id", videoID), zap.Error(err))
		}
	}

	// --- 全部完成：落库 + 广播 100% ---
	publishAndPersist(db, broker, ctx, videoID, modelsTrans.StatusDone, 100, "")
	logger.Info("转码完成", zap.Uint("video_id", videoID))
}

// publishAndPersist 把转码状态同时写入数据库并广播给订阅者。
// 这是保证"数据库记录"与"实时推送"一致性的便捷方法，避免各处重复写这两步。
//
// 何时调用：ProcessVideo 各阶段结束时（探测完成10%、每档完成、最终100%）。
// 参数来源：status/progress 是调用方规划好的总体进度值（与包注释的进度模型对应），
// errMsg 成功时传 ""，失败信息由 failTask 单独处理。
func publishAndPersist(db *gorm.DB, broker *ProgressBroker, ctx context.Context, videoID uint, status uint8, progress uint8, errMsg string) {
	updateStatus(db, ctx, videoID, status, progress, errMsg)
	broker.Publish(ProgressUpdate{
		VideoID:  videoID,
		Status:   status,
		Progress: progress,
		ErrorMsg: errMsg,
	})
}

// updateStatus 更新（或创建）transcode_tasks 表中的任务状态记录。
// 以 video_id 为准：记录不存在则创建，存在则更新状态、进度、错误信息。
//
// 何时调用：本包唯一的落库公共点，被 publishAndPersist 和 failTask 调用（ProcessVideo 不直接碰它）。
// 参数来源：videoID 来自 ProcessVideo 的入参；status/progress/errMsg 由调用方传入。
func updateStatus(db *gorm.DB, ctx context.Context, videoID uint, status uint8, progress uint8, errMsg string) {
	existing := &modelsTrans.TranscodeTask{}
	if err := db.WithContext(ctx).Where("video_id = ?", videoID).First(existing).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		// 记录不存在 → 创建新任务（视频ID唯一索引保证不会重复）
		db.WithContext(ctx).Create(&modelsTrans.TranscodeTask{
			VideoID:    videoID,
			Status:     status,
			Progress:   progress,
			ErrMessage: errMsg,
		})
		return
	} else if err != nil {
		logger.Error("无法连接到数据库", zap.Uint("video_id", videoID), zap.Error(err))
		return
	}
	// 记录已存在 → 只更新这三个业务字段（不碰 ID/时间）
	db.WithContext(ctx).Model(&modelsTrans.TranscodeTask{}).
		Where("video_id = ?", videoID).Updates(map[string]any{
		"status":      status,
		"progress":    progress,
		"err_message": errMsg,
	})
}

// failTask 标记转码任务失败：记日志 + 更新数据库 + 广播失败状态。
//
// 何时调用：ProcessVideo 里任何致命错误提前 return 前（ffmpeg/ffprobe 缺失、
// 预签名URL生成失败、下载失败等）。
// 参数来源：videoID/ctx/db/broker 透传 ProcessVideo 的入参；errMsg 是调用方拼好的失败描述。
func failTask(db *gorm.DB, broker *ProgressBroker, ctx context.Context, videoID uint, errMsg string) {
	logger.Error("转码失败", zap.Uint("video_id", videoID), zap.String("error", errMsg))
	updateStatus(db, ctx, videoID, modelsTrans.StatusFailed, 0, errMsg)
	broker.Publish(ProgressUpdate{VideoID: videoID, Status: modelsTrans.StatusFailed, Progress: 0, ErrorMsg: errMsg})
}

// saveMeta 持久化视频元信息到 video_metas 表（一个视频至多一条）。
// 以 video_id 为准：已存在则更新，不存在则创建。
//
// 何时调用：ProcessVideo 在 ffprobe 成功时调用（ffprobe 失败会跳过，走默认分辨率兜底）。
// 参数来源：videoID 透传 ProcessVideo 入参；meta 来自 runFFProbe 的返回值。
func saveMeta(db *gorm.DB, ctx context.Context, videoID uint, meta *modelsMeta.VideoMeta) {
	var count int64
	db.WithContext(ctx).Model(&modelsMeta.VideoMeta{}).Where("video_id = ?", videoID).Count(&count)
	if count > 0 {
		// 已有记录：更新（只更新技术参数，不碰 ID/VideoID）
		db.WithContext(ctx).Model(&modelsMeta.VideoMeta{}).Where("video_id = ?", videoID).Updates(map[string]any{
			"duration": meta.Duration,
			"width":    meta.Width,
			"height":   meta.Height,
			"codec":    meta.Codec,
			"bitrate":  meta.Bitrate,
		})
		return
	}
	// 新记录：创建
	meta.VideoID = videoID
	db.WithContext(ctx).Create(meta)
}

// saveQuality 持久化单个清晰度档位的转码产物信息到 video_qualities 表（一档一条）。
// 记录 object_name（MinIO 中 m3u8 的路径）和 file_size（该档所有分片总大小）。
//
// 何时调用：ProcessVideo 每转完一个档位、分片全部上传完后调用。
// 参数来源：
//   - quality: 档位标签（如 "720p"，来自 transcodeTargets 或兜底档位）
//   - objectName: 本档 m3u8 的对象名 videos/{videoID}/{档位}/index.m3u8
//   - fileSize: 本档所有分片大小的累加（前端展示该档体积用）
func saveQuality(db *gorm.DB, ctx context.Context, videoID uint, quality string, objectName string, fileSize uint64) {
	var count int64
	db.WithContext(ctx).Model(&modelsQuality.VideoQuality{}).
		Where("video_id = ? AND quality = ?", videoID, quality).
		Count(&count)
	if count > 0 {
		// 已有记录：更新（重复转码时刷新产物信息）
		db.WithContext(ctx).Model(&modelsQuality.VideoQuality{}).
			Where("video_id = ? AND quality = ?", videoID, quality).
			Updates(map[string]any{
				"object_name": objectName,
				"file_size":   fileSize,
			})
		return
	}
	// 新记录：创建
	db.WithContext(ctx).Create(&modelsQuality.VideoQuality{
		VideoID:    videoID,
		Quality:    quality,
		ObjectName: objectName,
		FileSize:   fileSize,
	})
}

// ffprobeOutput 是 ffprobe JSON 输出的顶层结构。
// 何时使用：runFFProbe 用 json.Unmarshal 把 ffprobe 的 stdout 解析成该结构，再提取字段。
type ffprobeOutput struct {
	Streams []ffprobeStream `json:"streams"`
	Format  ffprobeFormat   `json:"format"`
}

// ffprobeStream 表示 ffprobe 返回的一条流信息（视频/音频/字幕等）。
// 一个视频通常有视频流 + 音频流多条，runFFProbe 只取 codec_type == "video" 的那条。
type ffprobeStream struct {
	CodecType string `json:"codec_type"`
	CodecName string `json:"codec_name"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
}

// ffprobeFormat 表示 ffprobe 返回的容器格式信息（整个文件的时长与总码率）。
type ffprobeFormat struct {
	Duration string `json:"duration"`
	BitRate  string `json:"bit_rate"`
}

// runFFProbe 调用 ffprobe 提取视频元信息（时长、分辨率、编码、码率）。
// 用 JSON 输出格式解析，避免人工解析文本。
//
// 何时调用：ProcessVideo 下载完源视频后调用一次，拿到"源视频的体检报告"。
// 参数来源：inputFile 是下载到本地的源视频路径（ProcessVideo 里拼的 /tmp/transcode/{id}/input.mp4）。
// 返回值：*VideoMeta 一方面传给 saveMeta 落库，另一方面 Width/Height 决定"转哪些档位"（转码不放大）。
func runFFProbe(inputFile string) (*modelsMeta.VideoMeta, error) {
	cmd := exec.Command("ffprobe",
		"-v", "quiet", // 抑制日志输出
		"-print_format", "json", // 输出 JSON
		"-show_format",  // 输出容器格式（含 duration/bit_rate）
		"-show_streams", // 输出流信息（含分辨率/编码）
		inputFile,
	)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe: %w", err)
	}

	var probe ffprobeOutput
	if err := json.Unmarshal(output, &probe); err != nil {
		return nil, fmt.Errorf("ffprobe 解析失败: %w", err)
	}

	meta := &modelsMeta.VideoMeta{}
	if d, err := strconv.ParseFloat(probe.Format.Duration, 64); err == nil {
		meta.Duration = d // 时长（秒）
	}
	if b, err := strconv.ParseUint(probe.Format.BitRate, 10, 64); err == nil {
		meta.Bitrate = uint(b / 1000) // ffprobe 给的是 bps，转成 kbps 存
	}
	for _, s := range probe.Streams {
		if s.CodecType == "video" { // 只取视频流，忽略音频/字幕流
			meta.Width = uint(s.Width)
			meta.Height = uint(s.Height)
			meta.Codec = s.CodecName
			break
		}
	}
	return meta, nil
}

// runFFmpegHLS 调用 ffmpeg 把输入视频转码为 HLS 分片（m3u8 + ts）。
// 通过 -progress pipe:1 让 ffmpeg 把进度写到 stdout，逐行解析 out_time
// 换算成 0-100% 的编码进度，实时回调 onProgress。
//
// 何时调用：ProcessVideo 的主循环里每个档位调一次，同步阻塞直到该档转完（见循环内调用点）。
// 参数来源：
//   - inputFile: 本地源视频路径（所有档位共用同一个）
//   - outputM3U8 / segPattern: ProcessVideo 按档位目录拼好的路径
//     （/tmp/transcode/{id}/{档位}/index.m3u8 与 seg_%03d.ts 模板）
//   - width/height: 该档目标分辨率（来自 transcodeTargets 或兜底档位）
//   - duration: ffprobe 得到的源视频时长（秒），仅用于把 out_time 换算成百分比
//   - onProgress: 上层传入的闭包，ffmpeg 每报一次进度就调用它（把编码进度映射到总体进度并广播）
func runFFmpegHLS(ctx context.Context, inputFile, outputM3U8, segPattern string, width, height int, duration float64, onProgress func(pct float64)) error {
	scaleFilter := fmt.Sprintf("scale=%d:%d", width, height)

	// 构建 ffmpeg 命令：H.264 视频 + AAC 音频，10 秒一分片输出 HLS
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-progress", "pipe:1", // 进度写到 stdout（供解析）
		"-nostats", // 关闭默认统计，减少 stdout 噪音
		"-i", inputFile,
		"-vf", scaleFilter, // 缩放滤镜
		"-c:v", "libx264",
		"-c:a", "aac",
		"-b:a", "128k",
		"-preset", "fast",
		"-crf", "23", // 恒定质量 23（越小越清晰越费时间）
		"-hls_time", "10", // 每个 ts 分片时长 10 秒
		"-hls_list_size", "0", // m3u8 里包含全部分片（0 = 不限制）
		"-hls_segment_filename", segPattern, // ts 分片的命名模板
		outputM3U8,
	)

	// 拿 stdout 管道用于解析进度；拿 stderr 管道防止其写满导致 ffmpeg 阻塞
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("ffmpeg stdout 管道: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("ffmpeg stderr 管道: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("ffmpeg 启动失败: %w", err)
	}

	// 协程1：逐行扫描 stdout，解析 out_time= 计算编码进度
	scanDone := make(chan struct{})
	go func() {
		defer close(scanDone) // 扫描结束必须关闭，主流程靠它同步
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if strings.HasPrefix(line, "out_time=") {
				ts := strings.TrimPrefix(line, "out_time=")
				outTime := parseTimeToSeconds(ts)
				if duration > 0 && outTime > 0 {
					pct := (outTime / duration) * 100.0
					if pct > 100 {
						pct = 100
					}
					onProgress(pct) // 回调给上层映射到总体进度
				}
			}
		}
	}()

	// 协程2：持续读空 stderr，防止管道缓冲区满导致 ffmpeg 卡死
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := stderr.Read(buf); err != nil {
				break // 读到 EOF（ffmpeg 结束）就退出
			}
		}
	}()

	// 等待 ffmpeg 进程结束；等进度扫描协程退出（保证 onProgress 不会再被调用）
	err = cmd.Wait()
	<-scanDone

	if err != nil {
		return fmt.Errorf("ffmpeg HLS: %w", err)
	}
	return nil
}

// parseTimeToSeconds 把 ffmpeg 的 "HH:MM:SS.microseconds" 时间戳转成浮点秒数。
// 形如 "00:01:23.456789"，解析失败时返回 0（调用方会忽略 0 值进度）。
//
// 何时调用：仅被 runFFmpegHLS 的扫描协程调用，解析每行 out_time= 的值。
// 参数来源：ts 是 stdout 里 out_time= 后面的原始字符串（去掉微秒部分后按 : 拆分）。
func parseTimeToSeconds(ts string) float64 {
	hms := strings.SplitN(ts, ".", 2)[0] // 去掉微秒部分
	parts := strings.Split(hms, ":")
	if len(parts) != 3 {
		return 0
	}
	h, _ := strconv.Atoi(parts[0])
	m, _ := strconv.Atoi(parts[1])
	s, _ := strconv.Atoi(parts[2])
	return float64(h*3600+m*60) + float64(s)
}

// runFFmpegCover 从输入视频的第 1 秒截取一帧作为封面图（jpg）。
//
// 何时调用：ProcessVideo 在视频没有封面时调用（cover_url 为空才触发，不覆盖用户手动传的封面）。
// 参数来源：inputFile 是本地源视频；outputFile 是封面临时路径（转完后上传 MinIO 再删除）。
func runFFmpegCover(ctx context.Context, inputFile, outputFile string) error {
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-i", inputFile,
		"-ss", "1", // 定位到第 1 秒（放在 -i 前是快速 seek，比解码到第 1 秒快）
		"-vframes", "1", // 只取 1 帧
		"-q:v", "2", // 高质量（2 是最低压缩，图最清晰）
		outputFile,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		outputStr := string(out)
		if len(outputStr) > 500 {
			outputStr = outputStr[:500] + "..." // 截断超长报错，避免刷屏
		}
		logger.Debug("ffmpeg 封面输出", zap.String("output", outputStr))
		return fmt.Errorf("ffmpeg 封面: %w", err)
	}
	return nil
}

// downloadFile 通过 HTTP GET 把远程文件下载到本地路径（支持上下文取消）。
//
// 何时调用：ProcessVideo 生成预签名 URL 后调用，把源视频从 MinIO 拉到本地磁盘
// （ffmpeg 只能读本地文件路径，不能直接吃 HTTP 流，所以必须落盘）。
// 参数来源：url 是 st.GetPresignedURL 的返回值（带签名的临时链接）；destPath 是本地目标路径。
func downloadFile(ctx context.Context, url, destPath string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("downloadFile 构造请求: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("downloadFile: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloadFile: HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("downloadFile 创建文件: %w", err)
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return fmt.Errorf("downloadFile 写入: %w", err)
	}
	return nil
}

// uploadFileToMinio 把本地文件上传到 MinIO 对象存储。
// 根据文件扩展名自动设置正确的 Content-Type（HLS 相关类型前端播放器依赖）。
// 注意：storage 客户端必须通过参数传入（不能直接访问 ProcessVideo 里的 st，
// 那是另一个函数的局部变量），这就是为什么第一个参数是 st *storage.MinIO。
//
// 何时调用：ProcessVideo 每转完一档，遍历档位目录把 m3u8+ts 逐个上传；
// 自动封面提取成功后也用它上传封面图。
// 参数来源：localPath 是本地文件路径；objectName 是 MinIO 对象名
// （如 videos/{id}/{档位}/seg_000.ts）；fileSize 是该文件字节数（来自 os.FileInfo）。
func uploadFileToMinio(ctx context.Context, st *storage.MinIO, localPath, objectName string, fileSize int64) error {
	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("打开文件: %w", err)
	}
	defer f.Close()

	// 按扩展名设置 MIME 类型：浏览器/播放器靠它识别内容
	contentType := "application/octet-stream" // 兜底
	if strings.HasSuffix(objectName, ".m3u8") {
		contentType = "application/vnd.apple.mpegurl"
	} else if strings.HasSuffix(objectName, ".ts") {
		contentType = "video/mp2t"
	} else if strings.HasSuffix(objectName, ".jpg") || strings.HasSuffix(objectName, ".jpeg") {
		contentType = "image/jpeg"
	}

	return st.UploadVideo(ctx, objectName, f, fileSize, contentType)
}
