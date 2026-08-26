package comment

import (
	modelsComment "LikeBili/internal/models/comments"
	modelsVideo "LikeBili/internal/models/video"
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Repository 评论模块的数据访问层：封装 video_comments 表的读写操作。
// 涉及：评论的增删查、根评论分页、子评论批量拉取、评论点赞计数同步、
// 以及评论前对视频存在性的校验、评论后对视频作者 ID 的查询（通知用）。
type Repository struct {
	db *gorm.DB
}

// NewRepository 构造评论仓储，db 为 GORM 数据库连接。
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// Create 插入一条新评论。
// 注意：ParentID/RootID 的归属校验（父评论存在、RootID 从父评论继承）由 service 层负责，
// 本层只负责落库，不校验评论层级关系。
func (r *Repository) Create(c context.Context, comment *modelsComment.VideoComments) error {
	if err := r.db.WithContext(c).Create(comment).Error; err != nil {
		return fmt.Errorf("Method:comment.repository.Create: %w", err)
	}
	return nil
}

// FindByID 按主键查询单条评论，并 Preload 评论者用户信息。
// 查不到时返回 (nil, nil)，由 service 层判断"评论不存在"；其余错误原样包装返回。
func (r *Repository) FindByID(c context.Context, id uint) (*modelsComment.VideoComments, error) {
	var cmt modelsComment.VideoComments
	if err := r.db.WithContext(c).Preload("User").First(&cmt, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil // 评论不存在：返回 nil 而非错误
		}
		return nil, fmt.Errorf("Method:comment.repository.FindByID: %w", err)
	}
	return &cmt, nil
}

// FindRootComments 根评论分页查询（parent_id=0，即顶层评论）。
// 支持两种排序：sort="hot" 按点赞数降序；其他值（含空串）按创建时间倒序。
// 先 Count 总数再分页查询，返回 (列表, 总数, error)，供前端分页组件使用。
func (r *Repository) FindRootComments(c context.Context, videoID uint, sort string, page, pageSize int) ([]modelsComment.VideoComments, int64, error) {
	var total int64
	// ① 统计该视频的根评论总数（parent_id=0 过滤掉子评论，总数不含回复）
	query := r.db.WithContext(c).Model(&modelsComment.VideoComments{}).Where("video_id = ? AND parent_id = 0", videoID)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("Method:comment.repository.FindRootComments: %w", err)
	}

	// ② 按排序参数决定 ORDER BY：hot=点赞数降序，默认=时间倒序（最新在前）
	orderBy := "created_at DESC"
	if sort == "hot" {
		orderBy = "likes DESC"
	}

	// ③ 分页查询（offset=(page-1)*pageSize），并 Preload 评论者信息
	offset := (page - 1) * pageSize
	var list []modelsComment.VideoComments
	if err := query.Preload("User").
		Order(orderBy).Offset(offset).
		Limit(pageSize).Find(&list).Error; err != nil {
		return nil, 0, fmt.Errorf("Method:comment.repository.FindRootComments: %w", err)
	}
	return list, total, nil
}

// FindRepliesByRootIDs 按根评论 ID 批量拉取子评论（root_id IN ids，全量）。
// 用于根评论列表分页后一次性取回所有回复，在 service 层组装"每条根评论的第一页回复"，
// 避免逐条根评论 N+1 查询；若单根评论回复量极大，加载更多请走 FindRepliesByRootID 分页。
// 子评论按创建时间升序排列；ids 为空时直接返回 nil（不发起 SQL，避免 IN () 语法错误）。
func (r *Repository) FindRepliesByRootIDs(c context.Context, ids []uint) ([]modelsComment.VideoComments, error) {
	if len(ids) == 0 {
		return nil, nil // 空输入短路
	}

	var list []modelsComment.VideoComments
	if err := r.db.WithContext(c).Preload("User").
		Where("root_id IN ?", ids).
		Order("created_at ASC").Find(&list).Error; err != nil {
		return nil, fmt.Errorf("Method:comment.repository.FindRepliesByRootIDs: %w", err)
	}
	return list, nil
}

// FindRepliesByRootID 单条根评论下的子评论分页查询（root_id = ?）。
// 供子评论分页接口加载更多回复使用（GET /videos/:id/comments/:root_id/replies）。
// 先 Count 该根评论下的回复总数，再分页查询（时间升序），返回 (列表, 总数, error)。
func (r *Repository) FindRepliesByRootID(c context.Context, rootID uint, page, pageSize int) ([]modelsComment.VideoComments, int64, error) {
	var total int64
	// ① 统计该根评论下的回复总数（不包含根评论自身，根评论 root_id=0 不会命中）
	query := r.db.WithContext(c).Model(&modelsComment.VideoComments{}).Where("root_id = ?", rootID)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("Method:comment.repository.FindRepliesByRootID: %w", err)
	}

	// ② 分页查询（offset=(page-1)*pageSize），回复按时间升序
	offset := (page - 1) * pageSize
	var list []modelsComment.VideoComments
	if err := query.Preload("User").
		Order("created_at ASC").Offset(offset).
		Limit(pageSize).Find(&list).Error; err != nil {
		return nil, 0, fmt.Errorf("Method:comment.repository.FindRepliesByRootID: %w", err)
	}
	return list, total, nil
}

// Delete 删除评论（软删除）。
// VideoComments 含 gorm.DeletedAt 字段，Delete 实际执行 UPDATE deleted_at，
// 后续所有查询（Find/FindRootComments 等）会自动过滤已删评论。
func (r *Repository) Delete(c context.Context, id uint) error {
	if err := r.db.Delete(&modelsComment.VideoComments{}, id).Error; err != nil {
		return fmt.Errorf("Method:comment.repository.Delete: %w", err)
	}
	return nil
}

// UpdateVideoLikes 原子增减评论的点赞冗余计数。
// delta 语义：点赞传 +1、取消传 -1；通过 gorm.Expr("likes + ?") 在数据库层原子运算，
// 避免并发点赞时"绝对值覆盖"互相丢更新。
func (r *Repository) UpdateVideoLikes(c context.Context, id uint, delta int) error {
	if err := r.db.WithContext(c).
		Model(&modelsComment.VideoComments{}).
		Where("id = ?", id).
		Update("likes", gorm.Expr("likes + ?", delta)).Error; err != nil {
		return fmt.Errorf("Method:comment.repository.UpdateVideoLikes: %w", err)
	}
	return nil
}

// FindAuthorID 查询视频作者的用户 ID（有人评论时通知作者用）。
// Pluck 的 dest 传指针 &authorID（传值会运行时 panic）；
// 视频不存在或已软删时返回 gorm.ErrRecordNotFound，由 service 用 errors.Is 判断作者不存在。
func (r *Repository) FindAuthorID(c context.Context, videoID uint) (uint, error) {
	var authorID uint
	if err := r.db.WithContext(c).
		Table("videos").Where("id = ?", videoID).
		Pluck("user_id", &authorID).Error; err != nil {
		return 0, fmt.Errorf("Method:comment.repository.FindAuthorID: %w", err)
	}
	return authorID, nil
}

// FindVideoExist 评论前校验视频是否真实存在。
// First 的 dest 传非 nil 指针 &video；视频不存在（gorm.ErrRecordNotFound）时返回 (false, nil)，
// 由调用方据此拒绝评论；数据库其他错误原样包装返回。
func (r *Repository) FindVideoExist(c context.Context, videoID uint) (bool, error) {
	var video modelsVideo.Video
	err := r.db.WithContext(c).
		Table("videos").Where("id = ?", videoID).
		First(&video).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil // 视频不存在：返回 false 而非错误
	}
	if err != nil {
		return false, fmt.Errorf("Method:comment.repository.FindVideoExist: %w", err)
	}
	return true, nil
}

// ================ 以下为 comment_likes 表（评论点赞关系）的操作 ================

// InsertIgnore 幂等插入评论点赞记录（冲突时忽略，不报错）。
// 依赖 comment_likes 表的 uk_user_comment 联合唯一索引，同一用户对同一评论只能赞一次。
// 返回值：true=本次真正新增点赞；false=重复点赞被忽略。
// 供 service 区分新增/重复，新增时才递增冗余计数（UpdateVideoLikes +1）。
func (r *Repository) InsertIgnore(c context.Context, userID, commentID uint) (bool, error) {
	result := r.db.WithContext(c).
		Clauses(clause.Insert{Modifier: "IGNORE"}).
		Create(&modelsComment.CommentLikes{UserID: userID, CommentID: commentID})
	if result.Error != nil {
		return false, fmt.Errorf("Method:comment.repository.InsertIgnore: %w", result.Error)
	}
	return result.RowsAffected > 0, nil
}

// DeleteLike 取消评论点赞：按"用户 + 评论"删除点赞关系记录（物理删除，表无软删字段）。
func (r *Repository) DeleteLike(c context.Context, userID, commentID uint) error {
	if err := r.db.WithContext(c).
		Where("comment_id = ? AND user_id = ?", commentID, userID).
		Delete(&modelsComment.CommentLikes{}).Error; err != nil {
		return fmt.Errorf("Method:comment.repository.DeleteLike: %w", err)
	}
	return nil
}

// Exists 判断用户是否已点赞某条评论：true=已赞，false=未赞。
func (r *Repository) Exists(c context.Context, userID, commentID uint) (bool, error) {
	var likes modelsComment.CommentLikes
	if err := r.db.WithContext(c).
		Where("user_id = ? AND comment_id = ?", userID, commentID).
		Limit(1).Find(&likes).Error; err != nil {
		return false, fmt.Errorf("Method:comment.repository.Exists: %w", err)
	}
	return likes.ID != 0, nil
}

// FindLikedIDs 批量查询当前用户已点赞的评论 ID 集合（评论列表 IsLiked 填充用）。
// 一次 IN 查询收齐所有命中 ID，避免逐条 Exists 的 N+1 问题；
// 未登录（userID=0）或 ids 为空时直接返回空切片（不发起 SQL，避免无意义查询与 IN () 语法错误）。
func (r *Repository) FindLikedIDs(c context.Context, userID uint, ids []uint) ([]uint, error) {
	if userID == 0 || len(ids) == 0 {
		return nil, nil
	}
	var liked []uint
	if err := r.db.WithContext(c).
		Model(&modelsComment.CommentLikes{}).
		Where("user_id = ? AND comment_id IN ?", userID, ids).
		Pluck("comment_id", &liked).Error; err != nil {
		return nil, fmt.Errorf("Method:comment.repository.FindLikedIDs: %w", err)
	}
	return liked, nil
}
