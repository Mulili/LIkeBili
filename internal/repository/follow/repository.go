package follow

import (
	modelsFollow "LikeBili/internal/models/follow"
	"context"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// Follow 建立关注关系（关注、回关共用同一条链路，数据层无差别）。
// 幂等插入：命中联合唯一索引 uk_follower_followee 的重复行 DoNothing 跳过（重复点击/已关注不报错）。
// 返回是否本次新增：true=首次关注成功；false=该关系已存在。
// 【通知预留】service 仅在 true 时给 followeeID（被关注者）发通知，可天然避免重复点击造成的重复通知。
func (r *Repository) Follow(c context.Context, followerID, followeeID uint) (bool, error) {
	f := modelsFollow.Follow{FollowerID: followerID, FolloweeID: followeeID}
	result := r.db.WithContext(c).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "follower_id"}, {Name: "followee_id"}},
		DoNothing: true, // 已关注过：冲突时什么都不做，不更新 created_at
	}).Create(&f)
	if result.Error != nil {
		return false, fmt.Errorf("Method:follow.repository.Follow: %w", result.Error)
	}
	return result.RowsAffected == 1, nil
}

// Unfollow 解除关注关系（取关）。按唯一键定位删除，不存在该关系时删除 0 行也属正常（幂等），不报错。
func (r *Repository) Unfollow(c context.Context, followerID, followeeID uint) error {
	result := r.db.WithContext(c).
		Where("follower_id = ? AND followee_id = ?", followerID, followeeID).
		Delete(&modelsFollow.Follow{}) // Follow 无 DeletedAt 字段 → GORM 执行物理删除
	if result.Error != nil {
		return fmt.Errorf("Method:follow.repository.Unfollow: %w", result.Error)
	}
	return nil
}

// ListFollowing 分页查询"我关注了谁"（关注列表，按关注时间倒序）。
// 返回的每行挂 Followee（对端用户）：若对端已注销（users 软删），Preload 按默认 scope 过滤后 Followee.ID==0，
// service 据此替换为官方"已注销"占位；行本身保留，不影响分页与 total。
func (r *Repository) ListFollowing(c context.Context, userID uint, page, pageSize int) ([]modelsFollow.Follow, int64, error) {
	var list []modelsFollow.Follow
	var total int64
	// 先统计总数：我的关注数
	query := r.db.WithContext(c).Model(&modelsFollow.Follow{}).Where("follower_id = ?", userID)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("Method:follow.repository.ListFollowing: %w", err)
	}

	offset := (page - 1) * pageSize
	// Preload 对端用户信息；ORDER BY created_at DESC 由 idx_follower_created(follower_id, created_at) 支撑，无 filesort
	if err := query.Preload("Followee").
		Order("created_at DESC").
		Offset(offset).Limit(pageSize).
		Find(&list).Error; err != nil {
		return nil, 0, fmt.Errorf("Method:follow.repository.ListFollowing: %w", err)
	}
	return list, total, nil
}

// ListFollowers 分页查询"谁关注了我"（粉丝列表，按关注时间倒序）。
// 每行挂 Follower（粉丝用户）：粉丝已注销时同 ListFollowing，Follower.ID==0 交由 service 做占位。
func (r *Repository) ListFollowers(c context.Context, userID uint, page, pageSize int) ([]modelsFollow.Follow, int64, error) {
	var list []modelsFollow.Follow
	var total int64
	// 先统计总数：我的粉丝数
	query := r.db.WithContext(c).Model(&modelsFollow.Follow{}).Where("followee_id = ?", userID)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("Method:follow.repository.ListFollowers: %w", err)
	}

	offset := (page - 1) * pageSize
	// ORDER BY created_at DESC 由 idx_followee_created(followee_id, created_at) 支撑
	if err := query.Preload("Follower").
		Order("created_at DESC").
		Offset(offset).Limit(pageSize).
		Find(&list).Error; err != nil {
		return nil, 0, fmt.Errorf("Method:follow.repository.ListFollowers: %w", err)
	}
	return list, total, nil
}

// FindFollowingIDs 批量判断 userID 是否已关注 targetIDs 中的每一个（粉丝列表填充 IsFollowing 用）。
// 一次 IN 查询返回"已关注的那些 ID"，避免逐行 N+1；走 uk_follower_followee 唯一索引。
func (r *Repository) FindFollowingIDs(c context.Context, userID uint, targetIDs []uint) ([]uint, error) {
	if len(targetIDs) == 0 {
		return nil, nil
	}
	var ids []uint
	if err := r.db.WithContext(c).Model(&modelsFollow.Follow{}).
		Where("follower_id = ? AND followee_id IN ?", userID, targetIDs).
		Pluck("followee_id", &ids).Error; err != nil {
		return nil, fmt.Errorf("Method:follow.repository.FindFollowingIDs: %w", err)
	}
	return ids, nil
}

// FindFollowerIDs 批量判断 targetIDs 中哪些已关注 userID（关注列表填充 IsFollowed/互关标识用）。
// 反方向的一次 IN 查询，走同一唯一索引。
func (r *Repository) FindFollowerIDs(c context.Context, userID uint, targetIDs []uint) ([]uint, error) {
	if len(targetIDs) == 0 {
		return nil, nil
	}
	var ids []uint
	if err := r.db.WithContext(c).Model(&modelsFollow.Follow{}).
		Where("followee_id = ? AND follower_id IN ?", userID, targetIDs).
		Pluck("follower_id", &ids).Error; err != nil {
		return nil, fmt.Errorf("Method:follow.repository.FindFollowerIDs: %w", err)
	}
	return ids, nil
}

// FindNickname 查询用户的展示名（关注通知文案用）：昵称非空取昵称，否则回退用户名。
// 跨 users 表查询（与 like 模块 FindLikeUsername 同款 SQL）；
// 已注销（软删）用户查不到返回空串，由 service 回退为通用占位文案。
func (r *Repository) FindNickname(c context.Context, userID uint) (string, error) {
	var name string
	if err := r.db.WithContext(c).Table("users").
		Where("id = ? AND deleted_at IS NULL", userID).
		Pluck("COALESCE(NULLIF(nickname,''),username)", &name).Error; err != nil {
		return "", fmt.Errorf("Method:follow.repository.FindNickname: %w", err)
	}
	return name, nil
}

// CountFollowing 统计该用户关注了多少人（关注数），供个人中心聚合展示。
// 走 idx_follower_created(follower_id, created_at)，只扫索引不回表。
func (r *Repository) CountFollowing(c context.Context, userID uint) (int64, error) {
	var count int64
	if err := r.db.WithContext(c).Model(&modelsFollow.Follow{}).
		Where("follower_id = ?", userID).Count(&count).Error; err != nil {
		return 0, fmt.Errorf("Method:follow.repository.CountFollowing: %w", err)
	}
	return count, nil
}

// CountFollowers 统计该用户有多少粉丝（粉丝数），供个人中心聚合展示。
// 走 idx_followee_created(followee_id, created_at)。
func (r *Repository) CountFollowers(c context.Context, userID uint) (int64, error) {
	var count int64
	if err := r.db.WithContext(c).Model(&modelsFollow.Follow{}).
		Where("followee_id = ?", userID).Count(&count).Error; err != nil {
		return 0, fmt.Errorf("Method:follow.repository.CountFollowers: %w", err)
	}
	return count, nil
}
