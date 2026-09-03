package video

import (
	modelsMeta "LikeBili/internal/models/meta"
	modelsQuality "LikeBili/internal/models/quality"
	modelsReview "LikeBili/internal/models/review"
	"LikeBili/internal/models/transcode"
	modelsVideo "LikeBili/internal/models/video"
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// 封装视频数据库操作，不含任何业务逻辑
type Repository struct {
	db *gorm.DB
}

// video实例
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// CRUD
// 增加video，将video的信息通过指针传入上下文写入到数据库之中
func (r *Repository) Create(c context.Context, video *modelsVideo.Video) error {
	result := r.db.WithContext(c).Create(video)
	if result.Error != nil {
		//使用%w可以判断错误类型
		return fmt.Errorf("Method:video.Repository.Create: %w", result.Error)
	}
	return nil
}

// 根据videoid查找video
func (r *Repository) FindByID(c context.Context, id uint) (*modelsVideo.Video, error) {
	var video modelsVideo.Video
	//先First找到主键id为当前id的第一个视频，然后通过Preload将User关联表一起查询随后一起返回
	result := r.db.WithContext(c).Preload("User").First(&video, id)
	if result.Error != nil {
		//判断错误类型，如果Error为找不到相关数据，并非一定是错误，也可能是数据不存在
		if result.Error == gorm.ErrRecordNotFound {
			return nil, nil
		}
		//如果连接不到数据库那么就说明一定是错误了，此时再输出原始错误排查
		return nil, fmt.Errorf("Method:video.repository.FindByID: %w", result.Error)
	}
	return &video, nil
}

// 更新视频字段
func (r *Repository) Update(c context.Context, video *modelsVideo.Video) error {
	/*
		注意，Save方法将进行全量更新，任何更改或未更改的条目都会被更新
		这意味着如果你如果不曾更改类似category，也会因为使用Save方法，
		导致变回默认值或零值
	*/
	result := r.db.WithContext(c).Save(video)
	if result.Error != nil {
		return fmt.Errorf("Method:video.Reposity.Update: %w", result.Error)
	}
	return nil
}

// 批量查找视频
func (r *Repository) FindList(c context.Context, page, pageSize uint, categoryId uint) ([]modelsVideo.Video, int64, error) {
	var videos []modelsVideo.Video
	var total int64
	// 审核通过（status=2）且公开（view_status=1）的视频才进入公开列表
	query := r.db.WithContext(c).Model(&modelsVideo.Video{}).Where("status = ?", 2).Where("view_status = ?", 1)
	//如果category不为0则根据传入的类型进行查找，否则查找全表
	if categoryId > 0 {
		query = query.Where("category_id = ?", categoryId)
	}
	//查到的所有视频的数量汇总到total之中，用于分页，统计
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("Method:video.repository.FindList: %w", err)
	}
	//分页
	//表示跳过多少条信息
	offset := (page - 1) * pageSize
	//拼接Sql语句
	if err := query.Preload("User").Order("created_at DESC").Offset(int(offset)).Limit(int(pageSize)).Find(&videos).Error; err != nil {
		return nil, 0, fmt.Errorf("Method:video.repository.FindList: %w", err)
	}
	//成功返回视频列表，总数，无错误
	return videos, total, nil
}

// 查询用户视频
func (r *Repository) FindListByUser(c context.Context, userid uint, page, pageSize int, status *uint8) ([]modelsVideo.Video, int64, error) {
	var videos []modelsVideo.Video
	var total int64

	query := r.db.WithContext(c).Model(&modelsVideo.Video{}).Where("user_id = ?", userid)

	//若是传参则使用传入的参数进行判断
	if status != nil {
		query = query.Where("status = ?", *status)
	} else {
		//不传参时默认不查询审核失败的视频
		query = query.Where("status != ?", 3)
	}
	//统计查询到的视频总数
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("Method:video.repository.FindListByUser: %w", err)
	}
	//分页查询
	offset := (page - 1) * pageSize
	query = query.Preload("User").Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&videos)
	if query.Error != nil {
		return nil, 0, fmt.Errorf("Method:video.repository.FindListByUser: %w", query.Error)
	}
	return videos, total, nil
}

// 按状态查询视频
func (r *Repository) ListByStatus(c context.Context, page, pageSize int, status uint8) ([]modelsVideo.Video, int64, error) {
	var videos []modelsVideo.Video
	var total int64
	//按状态查询，status为必填项
	query := r.db.WithContext(c).Model(&modelsVideo.Video{}).Where("status = ?", status)
	//统计
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("Method:video.repository.ListByStatus: %w", err)
	}
	//分页
	offset := (page - 1) * pageSize
	if err := query.Preload("User").Order("created_at Desc").Offset(offset).Limit(pageSize).Find(&videos).Error; err != nil {
		return nil, 0, fmt.Errorf("Method:video.repository.ListByStatus: %w", err)
	}
	return videos, total, nil
}

// 查询审核通过的公共视频
// 用于全站排行榜或订阅推送
func (r *Repository) ListPublic(ctx context.Context) ([]modelsVideo.Video, error) {
	var videos []modelsVideo.Video
	result := r.db.WithContext(ctx).Preload("User").Where("status = ?", 2).Where("view_status = ?", 1).Find(&videos)
	if result.Error != nil {
		return nil, fmt.Errorf("Method:video.repository.ListPublic: %w", result.Error)
	}
	return videos, nil
}

// 更新视频时长，用于异步上传视频
func (r *Repository) UpdateDuration(c context.Context, videoid uint, duration uint) error {
	result := r.db.WithContext(c).Model(&modelsVideo.Video{}).Where("id = ? and duration = 0").Update("duration", duration) //仅在时长为0时修改
	if result.Error != nil {
		return fmt.Errorf("Method:video.repository.UpdateDuration: %w", result.Error)
	}
	return nil
}

// 增加播放量
func (r *Repository) IncrementViews(c context.Context, videoID uint) error {
	if err := r.db.WithContext(c).
		Model(&modelsVideo.Video{}).
		Where("id = ?", videoID).
		UpdateColumn("views", gorm.Expr("views + 1")). //利用mysql的InnoDB实现行锁，秒级并发10000
		Error; err != nil {
		return fmt.Errorf("Method:video.repository.IncrementViews: %w", err)
	}
	return nil
}

// 发布视频前检查是否转码
func (r *Repository) FindTask(c context.Context, videoID uint) (*transcode.TranscodeTask, error) {
	var task transcode.TranscodeTask
	if err := r.db.WithContext(c).
		Model(&transcode.TranscodeTask{}).
		Where("video_id = ?", videoID).
		First(&task).
		Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("Method:video.repository.FindTask: %w", err)
	}
	return &task, nil
}

// 删除video(软删除)
func (r *Repository) DeleteVideo(c context.Context, video *modelsVideo.Video) error {
	if err := r.db.WithContext(c).Delete(video).Error; err != nil {
		return fmt.Errorf("Method:video.repository.DeleteVideo: %w", err)
	}
	return nil
}

// 当删除时间超过一定时限，由管理员调用硬删除
func (r *Repository) ListDeleteBefore(c context.Context, droptime time.Time) ([]modelsVideo.Video, error) {
	var videos []modelsVideo.Video
	if err := r.db.WithContext(c).
		Model(&modelsVideo.Video{}).
		Where("deleted_at IS NOT NULL AND deleted_at < ?", droptime).
		Find(&videos).Error; err != nil {
		return nil, fmt.Errorf("Method:video.repository.ListDeleteBefore: %w", err)
	}
	return videos, nil
}

// ListQualityObjects 查一批视频的所有转码档位对象名（m3u8 的 ObjectName）。
// 用途：硬删视频前收集 MinIO 删除目标；必须与硬删 DB 同一批视频，且先查询再删除。
func (r *Repository) ListQualityObjects(c context.Context, videoIDs []uint) ([]string, error) {
	var objects []string
	if len(videoIDs) == 0 {
		return objects, nil
	}
	if err := r.db.WithContext(c).
		Model(&modelsQuality.VideoQuality{}).
		Where("video_id IN ?", videoIDs).
		Pluck("object_name", &objects).Error; err != nil {
		return nil, fmt.Errorf("Method:video.repository.ListQualityObjects: %w", err)
	}
	return objects, nil
}

// HardDeleteExpiredTx 事务内硬删（Unscoped）一批软删超期视频，并级联删除关联表记录：
// video_reviews（审核流水）、transcode_tasks（转码任务）、video_metas（元信息）、
// video_qualities（清晰度档位）。保证"视频本体与关联数据"要么同时删除、要么全部回滚。
func (r *Repository) HardDeleteExpiredTx(c context.Context, videoIDs []uint) error {
	if len(videoIDs) == 0 {
		return nil
	}
	return r.db.WithContext(c).Transaction(func(tx *gorm.DB) error {
		// ① 硬删视频本体（Unscoped 忽略软删过滤，物理删除）
		if err := tx.Unscoped().Where("id IN ?", videoIDs).Delete(&modelsVideo.Video{}).Error; err != nil {
			return err
		}
		// ② 级联删除关联表（按 video_id 批量删）
		if err := tx.Where("video_id IN ?", videoIDs).Delete(&modelsReview.VideoReview{}).Error; err != nil {
			return err
		}
		if err := tx.Where("video_id IN ?", videoIDs).Delete(&transcode.TranscodeTask{}).Error; err != nil {
			return err
		}
		if err := tx.Where("video_id IN ?", videoIDs).Delete(&modelsMeta.VideoMeta{}).Error; err != nil {
			return err
		}
		if err := tx.Where("video_id IN ?", videoIDs).Delete(&modelsQuality.VideoQuality{}).Error; err != nil {
			return err
		}
		return nil
	})
}

// TopByViews 按播放量取 TopN 视频 ID（热门榜 Redis 冷启动时的 DB 兜底排序）。
// 只取审核成功（status=2）且公开（view_status=1）的视频，按播放量降序截取前 top 个。
// 兜底口径说明：DB 目前只有 Views 计数，无法复刻 Redis 的加权热度（播放/点赞/评论/投币），
// 作为冷启动过渡方案够用；Redis 有埋点数据后以 HotRank 结果为准。
func (r *Repository) TopByViews(c context.Context, top int) ([]uint, error) {
	if top <= 0 {
		return nil, nil
	}
	var ids []uint
	if err := r.db.WithContext(c).
		Model(&modelsVideo.Video{}).
		Where("status = ? AND view_status = ?", 2, 1).
		Order("views DESC").
		Limit(top).
		Pluck("id", &ids).Error; err != nil {
		return nil, fmt.Errorf("Method:video.repository.TopByViews: %w", err)
	}
	return ids, nil
}
