package coin

import (
	modelsCoins "LikeBili/internal/models/coin"
	repocoin "LikeBili/internal/repository/coin"
	"LikeBili/internal/service/rank"
	codeErrors "LikeBili/pkg/errors"
	"LikeBili/pkg/logger"
	"context"
	"fmt"

	"go.uber.org/zap"
)

// Service 币模块的业务逻辑层：编排签到发币、投币、余额与投币状态查询。
// 职责边界：只做"调仓库 + 组装响应"，事务、幂等、上限校验等原子性逻辑全部下沉到 Repository。
type Service struct {
	repo *repocoin.Repository // 币数据访问层：user_coins（钱包）与 coins（投币流水）
	rank *rank.Service
}

// NewService 构造币服务，注入币仓储。
func NewService(repo *repocoin.Repository, rank *rank.Service) *Service {
	return &Service{repo: repo, rank: rank}
}

// SendCoins 签到发币：登录/注册时触发，确保钱包存在后执行"当天签到发放"。
// 幂等由仓库层保证：当天已签到过则不重复发放。
// 返回：发放成功返回最新余额；当天已领过返回 (nil, nil)（前端不展示签到信息）；
// 发币数异常（≠2 且 ≠0）视为内部错误上报。
func (s *Service) SendCoins(c context.Context, userID uint) (*modelsCoins.CoinBalanceResp, error) {
	// ① 确保钱包存在：注册/首次登录用户无 user_coins 记录，先建空钱包
	//    （否则 SendDailyCoins 的条件 UPDATE 影响 0 行，币发不出去）
	_, err := s.repo.GetOrCreateWallet(c, userID)
	if err != nil {
		return nil, fmt.Errorf("Method:coin.service.SendCoins: %w", err)
	}
	// ② 签到发币：返回最新钱包与本次实际发放数（0=今天已签过，2=本次发放）
	userCoin, count, err := s.repo.SendDailyCoins(c, userID)
	if err != nil {
		return nil, fmt.Errorf("Method:coin.service.SendCoins: %w", err)
	}
	// ③ 发放数为 0：今天已签到过，不返回任何信息给前端
	if count == 0 {
		return nil, nil
	} else if count != modelsCoins.SendCoins { // 发币数异常：防御性兜底，正常只会是 0 或 2
		return nil, fmt.Errorf("Method:coin.service.SendCoins: %w", codeErrors.New(codeErrors.Internal, "硬币数错误"))
	}
	return &modelsCoins.CoinBalanceResp{Balance: userCoin.Balance}, nil
}

// DropCoins 投币：扣投币者余额 → 给视频作者加币 → 维护投币流水（同一视频累计 ≤2）。
// 事务与上限/余额校验在仓库层 DropCoins 内完成；本层负责钱包兜底、结果回查与响应组装。
// 返回：该用户对该视频的累计投币数 + 投币后的最新余额。
func (s *Service) DropCoins(c context.Context, userID uint, req *modelsCoins.CoinReq) (*modelsCoins.CoinResp, error) {
	// ① 确保投币者钱包存在：否则仓库层条件扣减会因无记录误报"币不足"
	wallet, err := s.repo.GetOrCreateWallet(c, userID)
	if err != nil {
		return nil, fmt.Errorf("Method:coin.service.DropCoins: %w", err)
	}
	// ② 执行投币事务（失败时错误已在仓库层翻译为业务码：视频不存在/币不足/已达上限）
	if err := s.repo.DropCoins(c, req.VideoID, userID, req.Count); err != nil {
		return nil, fmt.Errorf("Method:coin.service.DropCoins: %w", err)
	}
	//投币埋点，计算投币热度
	if err := s.rank.Incr(c, req.VideoID, rank.DeltaCoin); err != nil {
		logger.Warn("投币热度埋点失败", zap.Uint("video_id", req.VideoID), zap.Error(err))
	}
	// ③ 投币后该用户对该视频的累计投币数（刚投过必有记录，不会为 nil）
	coin, err := s.repo.FindUserDrop(c, req.VideoID, userID)
	if err != nil {
		return nil, fmt.Errorf("Method:coin.service.DropCoins: %w", err)
	}
	// ④ 投币后的最新余额：wallet 是投币前快照（旧值），必须重查
	balance, err := s.repo.FindBalance(c, wallet.ID)
	if err != nil {
		return nil, fmt.Errorf("Method:coin.service.DropCoins: %w", err)
	}
	resp := &modelsCoins.CoinResp{
		VideoID:   req.VideoID,
		CoinCount: coin.Count,
		Balance:   balance,
	}
	return resp, nil
}

// CountVideoCoins 统计某视频收到的投币总数（所有用户累计 count 之和）。
// 供前端视频页展示"已投 X 币"；无人投币时返回 0。
func (s *Service) CountVideoCoins(c context.Context, videoID uint) (int64, error) {
	count, err := s.repo.CountVideoCoins(c, videoID)
	if err != nil {
		return 0, fmt.Errorf("Method:coin.service.CountVideoCoins: %w", err)
	}
	return count, nil
}

// FindUserDrop 查询当前用户对该视频还可投几个币，供前端控制投币按钮。
// 返回：canDrop（true=还可投币、按钮可用；false=已投满 2 个、按钮置灰）和剩余可投数。
func (s *Service) FindUserDrop(c context.Context, videoID, userID uint) (*modelsCoins.FindUserDropResp, error) {
	coins, err := s.repo.FindUserDrop(c, videoID, userID)
	if err != nil {
		return nil, fmt.Errorf("Method:coin.service.FindUserDrop: %w", err)
	}
	// 剩余可投数：未投过=2，已投 n=2-n，已满=0（coins==nil 是"未投过"，不是上限）
	var remain uint8 = modelsCoins.MaxDropCoins
	if coins != nil {
		remain = modelsCoins.MaxDropCoins - coins.Count
	}
	resp := &modelsCoins.FindUserDropResp{
		CanDrop:     remain > 0,
		RemainCount: remain,
	}

	return resp, nil
}

// FindBalance 查询用户当前余额（进入个人中心展示用）。
// 先确保钱包存在（新用户可能从未签到/收币），再按钱包主键读最新余额；无记录时返回 0。
func (s *Service) FindBalance(c context.Context, userID uint) (*modelsCoins.CoinBalanceResp, error) {
	// ① 确保钱包存在：新用户可能没有 user_coins 记录，先建空钱包
	wallet, err := s.repo.GetOrCreateWallet(c, userID)
	if err != nil {
		return nil, fmt.Errorf("Method:coin.service.FindBalance: %w", err)
	}
	// ② 按钱包主键查询余额（wallet.ID 为 GetOrCreateWallet 返回的主键）
	balance, err := s.repo.FindBalance(c, wallet.ID)
	if err != nil {
		return nil, fmt.Errorf("Method:coin.service.FindBalance: %w", err)
	}
	resp := &modelsCoins.CoinBalanceResp{Balance: balance}
	return resp, nil
}
