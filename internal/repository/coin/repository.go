package coin

import (
	modelsCoins "LikeBili/internal/models/coin"
	modelsVideo "LikeBili/internal/models/video"
	codeErrors "LikeBili/pkg/errors"
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// Repository 币模块的数据访问层：封装 user_coins（钱包）与 coins（投币流水）两张表的读写。
// 涉及：签到发币（原子条件更新，天然幂等）、余额查询；
// 投币（写流水 + 扣余额）待 service 完成后在此补充。
type Repository struct {
	db *gorm.DB
}

// NewRepository 构造币仓储，db 为 GORM 数据库连接。
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// SendDailyCoins 签到发币：仅"今天未签到"时给该用户 +2 币，并推进 last_coin_grant 到今天。
// 幂等关键：WHERE 带 last_coin_grant 条件（IS NULL 覆盖新用户 / < today 覆盖老用户），
// 今天已签到过则影响 0 行，无需先查后更，避免并发下重复发币。
// 返回值：始终返回最新钱包（service 直接取 Balance 构造响应，省一次回查）；
// count 为本次实际发放数（0 = 今天已签过，2 = 本次签到发放）。
func (r *Repository) SendDailyCoins(c context.Context, userID uint) (*modelsCoins.UserCoin, uint, error) {
	now := time.Now()
	// ① 今天零点：作为"是否已签"的边界，last_coin_grant < today 即代表今天未签
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)

	// ② 原子发币：balance 用 gorm.Expr 累加（防并发覆盖），last_coin_grant 一并更新
	result := r.db.WithContext(c).
		Model(&modelsCoins.UserCoin{}).
		Where("user_id = ? AND (last_coin_grant IS NULL OR last_coin_grant < ?)", userID, today).
		Updates(map[string]any{
			"balance":         gorm.Expr("balance + ?", modelsCoins.SendCoins),
			"last_coin_grant": today,
		})
	if result.Error != nil {
		return nil, 0, fmt.Errorf("Method:coin.repository.SendDailyCoins: %w", result.Error)
	}

	var userCoin modelsCoins.UserCoin
	if err := r.db.WithContext(c).Model(&modelsCoins.UserCoin{}).
		Where("user_id = ?", userID).First(&userCoin).Error; err != nil {
		return nil, 0, fmt.Errorf("Method:coin.repository.SendDailyCoins: %w", err)
	}
	// ③ 影响 0 行 = 今天已签到过，不重复发（由调用方区分是否提示"今日已领取"）
	if result.RowsAffected == 0 {
		return &userCoin, 0, nil
	}
	return &userCoin, modelsCoins.SendCoins, nil
}

// DropCoins 投币：扣投币者余额 → 给视频作者加币 → 维护投币流水（同一视频累计 ≤2）。
// 全程在一个数据库事务内，任一步失败整体回滚，杜绝"扣了没加 / 加了没扣 / 流水和余额不一致"。
// 返回错误：ErrVideoNotFound（视频不存在）、ErrCodeCoinsMaxCount（已达上限）、
// ErrCodeCoinsLowBalance（余额不足或钱包不存在）。
// 前置约定：调用方（service）需先签到发币并确保投币者钱包已创建（GetOrCreate）。
func (r *Repository) DropCoins(c context.Context, videoID, userID uint, count uint8) error {
	return r.db.WithContext(c).Transaction(func(tx *gorm.DB) error {
		// ① 查视频作者：First 查不到才返回 ErrRecordNotFound（Pluck 查空不报错，不可用）
		var video modelsVideo.Video
		if err := tx.First(&video, videoID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return codeErrors.ErrVideoNotFound
			}
			return fmt.Errorf("Method:coin.repository.DropCoins: %w", err)
		}

		// ② 维护投币流水：首次投币 INSERT，补投先判上限再累加 Count。
		//    判定放在动余额之前（fail fast），超限时不产生任何余额变更。
		var coin modelsCoins.Coin
		err := tx.Where("user_id = ? AND video_id = ?", userID, videoID).First(&coin).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			// 首次投币：该用户对该视频无记录，直接插入（count ∈ {1,2} 已由 model 的 oneof 校验，必然 ≤2）
			if err := tx.Create(&modelsCoins.Coin{
				UserID:  userID,
				VideoID: videoID,
				Count:   count,
			}).Error; err != nil {
				return fmt.Errorf("Method:coin.repository.DropCoins: %w", err)
			}
		case err != nil:
			return fmt.Errorf("Method:coin.repository.DropCoins: %w", err)
		default:
			// 补投：累计（已有 + 本次）超过 2 拒绝；uint8 最大 2+1=3，无溢出风险
			if (coin.Count + count) > modelsCoins.MaxDropCoins {
				return fmt.Errorf("Method:coin.repository.DropCoins: %w", codeErrors.ErrCodeCoinsMaxCount)
			}
			// 累加 Count：gorm.Expr 原子自增，防并发覆盖
			if err := tx.Model(&modelsCoins.Coin{}).Where("user_id = ? AND video_id = ?", userID, videoID).
				Update("count", gorm.Expr("count + ?", count)).Error; err != nil {
				return fmt.Errorf("Method:coin.repository.DropCoins: %w", err)
			}
		}

		// ③ 扣投币者：WHERE balance >= count 条件扣减防超扣；
		//    RowsAffected==0 = 余额不足或钱包不存在（未签到/未创建），统一报"币不足"
		res := tx.Model(&modelsCoins.UserCoin{}).
			Where("user_id = ? AND balance >= ?", userID, count).
			Update("balance", gorm.Expr("balance - ?", count))
		if res.Error != nil {
			return fmt.Errorf("Method:coin.repository.DropCoins: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			return codeErrors.ErrCodeCoinsLowBalance
		}

		// ④ 作者收币：钱包不存在则创建（首次收币，初始余额=本次收到的币），存在则累加
		var authorWallet modelsCoins.UserCoin
		err = tx.Where("user_id = ?", video.UserID).First(&authorWallet).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			// 作者从未有钱包（新作者）：创建并直接入账，LastCoinGrant 留 null（首次签到照常发币）
			if err := tx.Create(&modelsCoins.UserCoin{
				UserID:  video.UserID,
				Balance: uint(count),
			}).Error; err != nil {
				return fmt.Errorf("Method:coin.repository.DropCoins: %w", err)
			}
		case err != nil:
			return fmt.Errorf("Method:coin.repository.DropCoins: %w", err)
		default:
			// 已有钱包：余额原子累加
			if err := tx.Model(&modelsCoins.UserCoin{}).
				Where("user_id = ?", video.UserID).
				Update("balance", gorm.Expr("balance + ?", count)).Error; err != nil {
				return fmt.Errorf("Method:coin.repository.DropCoins: %w", err)
			}
		}
		return nil
	})
}

// CountVideoCoins 统计某视频收到的投币总数（所有用户累计 count 之和）。
// 供前端视频页展示"已投 X 币"；COALESCE 兜底：该视频无人投币时返回 0 而非 NULL。
func (r *Repository) CountVideoCoins(c context.Context, videoID uint) (int64, error) {
	var total int64
	query := r.db.WithContext(c).Model(modelsCoins.Coin{}).Where("video_id = ?", videoID)
	if err := query.Pluck("COALESCE(SUM(count),0)", &total).Error; err != nil {
		return 0, fmt.Errorf("Method:coin.repository.CountVideoCoins: %w", err)
	}
	return total, nil
}

// FindUserDrop 查询"当前用户对该视频"的投币记录（含已投数量 Count）。
// 供前端 UI 判断"已投 X/2"（X=0 显示可投，X=2 置灰）；
// 查不到时返回 (nil, nil) 而非错误，由 service 层判空后按"未投过"处理。
func (r *Repository) FindUserDrop(c context.Context, videoID, userID uint) (*modelsCoins.Coin, error) {
	var coin modelsCoins.Coin
	if err := r.db.WithContext(c).Model(&modelsCoins.Coin{}).
		Where("user_id = ? AND video_id = ?", userID, videoID).First(&coin).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("Method:coin.repository.FindUserDrop: %w", err)
	}
	return &coin, nil
}

// GetOrCreateWallet 获取用户钱包；不存在则创建（余额 0、LastCoinGrant 留 null=从未签到）。
// 所有读写钱包的入口（签到发币、投币、查余额）前都应先调用，保证 user_coins 记录必然存在，
// 否则 SendDailyCoins 对无记录用户会误判"今天已签到"、DropCoins 会误报"币不足"。
// 返回：已存在则原样返回；首次访问则插入后返回新钱包。
func (r *Repository) GetOrCreateWallet(c context.Context, userID uint) (*modelsCoins.UserCoin, error) {
	var wallet modelsCoins.UserCoin
	// ① 先查：存在直接复用
	err := r.db.WithContext(c).Where("user_id = ?", userID).First(&wallet).Error
	if err == nil {
		return &wallet, nil
	}
	// ② 仅"记录不存在"才继续创建；其余错误（DB 故障等）原样返回
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("Method:coin.repository.GetOrCreateWallet: %w", err)
	}

	// ③ 首次访问：插入空钱包，Create 后自动回填主键
	wallet = modelsCoins.UserCoin{UserID: userID}
	if err := r.db.WithContext(c).Create(&wallet).Error; err != nil {
		return nil, fmt.Errorf("Method:coin.repository.GetOrCreateWallet: %w", err)
	}
	return &wallet, nil
}

// FindBalance 按钱包记录主键查询当前余额（userCoinID 是 user_coins 表的 id，不是 user_id）。
// 调用方需先通过 user_id 取得钱包记录拿到主键；查不到时返回 0 而非错误，由 service 判空。
func (r *Repository) FindBalance(c context.Context, userCoinID uint) (uint, error) {
	var balance uint
	if err := r.db.WithContext(c).
		Model(&modelsCoins.UserCoin{}).
		Where("id = ?", userCoinID).
		Pluck("balance", &balance).Error; err != nil {
		return 0, fmt.Errorf("Method:coin.repository.FindBalance: %w", err)
	}
	return balance, nil
}
