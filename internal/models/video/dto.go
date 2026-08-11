package video

import (
	usermodel "LikeBili/internal/models/user"
	"time"
)

// ===============请求体======================
// 创建一条新的视频数据
type VideoReq struct {
	Title       string `json:"title" validate:"required,max=64"`
	Description string `json:"description" validate:"required,max=2048"`
	Category    uint32 `json:"category"`
}

// 修改视频
type UpdateVideoReq struct {
	Title       *string `json:"title" validate:"omitempty,max=64"`
	Description *string `json:"description" validate:"omitempty,max=2048"`
	CategoryID  *uint32 `json:"category_id"`
	ViewStatus  *uint8  `json:"status" validate:"oneof=1 2"`
}

// 上传时，显示进度
type UpdateLoadReq struct {
	UpLoad int64  `json:"upload"` //已完成的字节
	Total  int64  `json:"total"`  //总量
	ERROR  string `json:"error,omitempty"`
}

// ===============响应体======================
type VideoResp struct {
	ID          uint      `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	CoverURL    string    `json:"cover_url"`
	Duration    uint      `json:"duration"`
	FileSize    uint64    `json:"file_size"`
	CategoryID  uint32    `json:"category_id"` //类型id，建立索引，否则如果以后查找某一个类型就得全表查询
	Status      uint8     `json:"status"`      //视频状态：1待审核2审核成功3审核失败4隐藏
	Views       uint32    `json:"views"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`

	User *usermodel.UserBrief `json:"user"` //发布者简单信息
}

type ListVideo struct {
	List     []VideoResp `json:"list"`      //显示的视频列表
	Total    uint        `json:"total"`     //视频的总个数
	Page     uint16      `json:"page"`      //当前的页码
	PageSize uint16      `json:"page_size"` //每页显示的个数
}
