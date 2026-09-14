package search

import (
	modelsSearch "LikeBili/internal/models/search"
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

const (
	// FulltextIndexName 全文索引名（创建与存在性检测共用）
	FulltextIndexName = "idx_ft_search"

	// fulltextColumns 检索列清单：MATCH 语句与索引 DDL 必须使用完全相同的列集合与顺序，
	// 否则 MySQL 不会使用该全文索引，查询会静默退化成全表扫描。
	fulltextColumns = "title, description, author_nickname, category_name"
)

type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// EnsureFulltextIndex 幂等确保 video_search 上的 ngram 全文索引存在。
// 刻意不走 AutoMigrate：GORM 的索引标签无法保证 `WITH PARSER ngram`，
// 若生成一个不带分词器的同名索引，中文检索会静默失效且不易察觉。
// 已存在同名索引时直接跳过（若要更换分词器需先手工 DROP 该索引）。
func (r *Repository) EnsureFulltextIndex(c context.Context) error {
	var count int64
	if err := r.db.WithContext(c).Raw(
		`SELECT COUNT(*) FROM information_schema.STATISTICS
		 WHERE table_schema = DATABASE() AND table_name = ? AND index_name = ?`,
		(modelsSearch.VideoSearch{}).TableName(), FulltextIndexName,
	).Scan(&count).Error; err != nil {
		return fmt.Errorf("Method:search.repository.EnsureFulltextIndex: %w", err)
	}
	if count > 0 {
		return nil
	}

	ddl := fmt.Sprintf("ALTER TABLE %s ADD FULLTEXT KEY %s (%s) WITH PARSER ngram",
		(modelsSearch.VideoSearch{}).TableName(), FulltextIndexName, fulltextColumns)
	if err := r.db.WithContext(c).Exec(ddl).Error; err != nil {
		return fmt.Errorf("Method:search.repository.EnsureFulltextIndex: %w", err)
	}
	return nil
}

// IsEmpty 判断检索表是否为空（决定是否需要全量回填）。
func (r *Repository) IsEmpty(c context.Context) (bool, error) {
	var count int64
	if err := r.db.WithContext(c).Model(&modelsSearch.VideoSearch{}).Count(&count).Error; err != nil {
		return false, fmt.Errorf("Method:search.repository.IsEmpty: %w", err)
	}
	return count == 0, nil
}

// RebuildSearchIndex 全量回填检索表（幂等）。
// 用一条 INSERT ... SELECT ... ON DUPLICATE KEY UPDATE 完成，避免逐行往返；
// 只回填未软删的视频；作者展示名取"昵称优先、回退用户名"，分类名取字典表名称（缺失为空串）。
func (r *Repository) RebuildSearchIndex(c context.Context) error {
	const sqlText = `
INSERT INTO video_search
  (video_id, user_id, category_id, title, description, author_nickname, category_name, status, view_status, created_at, updated_at)
SELECT v.id, v.user_id, v.category_id, v.title, v.description,
       COALESCE(NULLIF(u.nickname, ''), u.username, ''),
       COALESCE(cat.name, ''),
       v.status, COALESCE(v.view_status, 1), v.created_at, NOW()
FROM videos v
LEFT JOIN users u ON u.id = v.user_id
LEFT JOIN categories cat ON cat.id = v.category_id
WHERE v.deleted_at IS NULL
ON DUPLICATE KEY UPDATE
  user_id         = VALUES(user_id),
  category_id     = VALUES(category_id),
  title           = VALUES(title),
  description     = VALUES(description),
  author_nickname = VALUES(author_nickname),
  category_name   = VALUES(category_name),
  status          = VALUES(status),
  view_status     = VALUES(view_status),
  created_at      = VALUES(created_at),
  updated_at      = NOW()`
	if err := r.db.WithContext(c).Exec(sqlText).Error; err != nil {
		return fmt.Errorf("Method:search.repository.RebuildSearchIndex: %w", err)
	}
	return nil
}

// SearchHit 检索命中项：视频 ID + 相关度分数 + 视频创建时间（供最新排序与顺序重排使用）。
type SearchHit struct {
	VideoID   uint
	Score     float64
	CreatedAt time.Time
}

// SearchVideos 全文检索公开视频，返回本页命中的视频 ID（按 order 排序）与命中总数。
// order：relevance（默认，相关度优先、同分按创建时间倒序）/ latest（创建时间倒序）。
// 可见性过滤（status=2 且 view_status=1）在检索表内完成，因此 total 与列表口径一致、分页不会错乱。
func (r *Repository) SearchVideos(c context.Context, keyword string, categoryID uint, order string, page, pageSize int) ([]SearchHit, int64, error) {
	matchExpr := fmt.Sprintf("MATCH(%s) AGAINST (? IN NATURAL LANGUAGE MODE)", fulltextColumns)

	// ① 命中总数（与列表同一套过滤条件，保证 total 与分页一致）
	countQuery := r.db.WithContext(c).Model(&modelsSearch.VideoSearch{}).
		Where("status = ? AND view_status = ?", 2, 1).
		Where(matchExpr, keyword)
	if categoryID > 0 {
		countQuery = countQuery.Where("category_id = ?", categoryID)
	}
	var total int64
	if err := countQuery.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("Method:search.repository.SearchVideos: %w", err)
	}

	// ② 本页命中：SELECT 里带上相关度分数，用于排序与后续的顺序重排
	listQuery := r.db.WithContext(c).Model(&modelsSearch.VideoSearch{}).
		Select("video_id, "+matchExpr+" AS score, created_at", keyword).
		Where("status = ? AND view_status = ?", 2, 1).
		Where(matchExpr, keyword)
	if categoryID > 0 {
		listQuery = listQuery.Where("category_id = ?", categoryID)
	}
	if order == "latest" {
		listQuery = listQuery.Order("created_at DESC")
	} else {
		listQuery = listQuery.Order("score DESC").Order("created_at DESC")
	}

	var hits []SearchHit
	offset := (page - 1) * pageSize
	if err := listQuery.Offset(offset).Limit(pageSize).Find(&hits).Error; err != nil {
		return nil, 0, fmt.Errorf("Method:search.repository.SearchVideos: %w", err)
	}
	return hits, total, nil
}
