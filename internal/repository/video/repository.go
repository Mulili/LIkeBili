package video

import (
	videomodel "LikeBili/internal/models/video"
	"context"
	"fmt"

	"gorm.io/gorm"
)

// 封装视频数据库操作，不含任何业务逻辑
type VideoRepository struct {
	db *gorm.DB
}

// video实例
func NewVideoRepository(db *gorm.DB) *VideoRepository {
	return &VideoRepository{db: db}
}

// CRUD
// 增加video，将video的信息通过指针传入上下文写入到数据库之中
func (r *VideoRepository) Create(c context.Context, video *videomodel.Video) error {
	result := r.db.WithContext(c).Create(video)
	if result.Error != nil {
		//使用%w可以判断错误类型
		return fmt.Errorf("Method:video.Repository.Create: %w", result.Error)
	}
	return nil
}

// 根据videoid查找video
func (r *VideoRepository) FindByID(c context.Context, id uint) (*videomodel.Video, error) {
	var video videomodel.Video
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
func (r *VideoRepository) Update(c context.Context, video *videomodel.Video) error {
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
func (r *VideoRepository) FindList(c context.Context, page, pageSize uint, categoryId uint) ([]videomodel.Video, int64, error) {
	var videos []videomodel.Video
	var total int64
	//将所有status=1（审核通过）的视频查找出来。
	query := r.db.WithContext(c).Model(&videomodel.Video{}).Where("status = ?", 1)
	//如果category不为0则根据传入的类型进行查找，否则查找全表
	if categoryId > 0 {
		query = r.db.WithContext(c).Where("category = ?", categoryId)
	}
	//查到的所有视频的数量汇总到total之中，用于分页，统计
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("Method:video.repository.FindList: %w", err)
	}
	//分页
	//表示跳过多少条信息
	offset := (page - 1) * pageSize
	//拼接Sql语句
	if err := query.Preload("User").Order("create_at DESC").Offset(int(offset)).Limit(int(pageSize)).Find(&videos).Error; err != nil {
		return nil, 0, fmt.Errorf("Method:video.repository.FindList: %w", err)
	}
	//成功返回视频列表，总数，无错误
	return videos, total, nil
}

func (r *VideoRepository) FindListByUser(c context.Context, userid uint, page, pageSize int, status *uint8) ([]videomodel.Video, int64, error) {
	var video []videomodel.Video
	var total int64

	query := r.db.WithContext(c).Model(&videomodel.Video{}).Where("user_id = ?", userid)
	//不传参时默认不查询审核失败的视频
	query = query.Where("status != ?", 3)
	//若是传参则使用传入的参数进行判断
	if status != nil {
		query = query.Where("status = ?", *status)
	}

	return video, total, nil
}
