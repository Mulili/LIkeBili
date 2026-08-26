package comment

import (
	modelsComments "LikeBili/internal/models/comments"
	modelsMessage "LikeBili/internal/models/message"
	repocomment "LikeBili/internal/repository/comment"
	codeErrors "LikeBili/pkg/errors"
	"LikeBili/pkg/toresp"
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// repliesPreviewLimit 根评论列表内嵌回复预览条数：回复超过该值只展示前几条，
// 更多回复由前端通过子评论分页接口（ReplyListResp）加载，避免列表响应被大回复树撑爆。
const repliesPreviewLimit = 5

// Notifier 将评论/回复行为转发给被回复人的接口。
// 由 message 模块的 Service 实现（SendNotification 签名与之一致），
// 向接收者（userID）写入一条"XX 回复了你的评论"的通知。
// 通知失败不阻塞评论主流程（调用处忽略错误）。
type Notifier interface {
	SendNotification(c context.Context, userID, fromUserID uint, msgType uint8, targetID uint, content string) error
}

// Service 评论模块业务层：发表评论/回复、拉取评论树、评论点赞，
// 以及评论后的热度埋点与作者通知（埋点待接入 rank 服务）。
type Service struct {
	repo     *repocomment.Repository      // 数据访问：评论/回复的增删查与点赞
	rdb      *redis.Client                // Redis：预留评论计数缓存
	notifier Notifier                     // 通知转发器（message 模块实现），可传 nil 跳过通知
	toresp   *toresp.UserBriefRespBuilder // 评论者信息转换器（头像 objKey → 完整 URL）
}

// NewService 构造评论服务，注入 Repository/Redis/通知器/用户简要信息转换器。
// notifier 可为 nil（不通知作者，不影响评论主流程）。
func NewService(repo *repocomment.Repository, rdb *redis.Client, notifier Notifier, toresp *toresp.UserBriefRespBuilder) *Service {
	return &Service{repo: repo, rdb: rdb, notifier: notifier, toresp: toresp}
}

// Create 发表评论/回复（根评论与楼中楼共用一条链路），以 MySQL 为权威数据源：
//   - 根评论：req.ParentID 为 nil/0 → RootID=0（自身即树根），不触发回复通知
//   - 回复：ParentID=直接父评论 ID → RootID 由父评论继承（见 resolveRootID），
//     并向"直接父评论"的作者发送"XX 回复了你的评论"通知（作者不能回复自己，不通知）
//
// 流程：校验视频存在 → 解析 RootID → 落库 → 重查补全评论者 User → 通知父评论作者 → 组装响应。
func (s *Service) Create(c context.Context, videoID, userID uint, req *modelsComments.CommentReq) (*modelsComments.CommentResp, error) {
	// ① 取直接父评论 ID：ParentID 指针为 nil 时视为 0（根评论）
	parent := uint(0)
	if req.ParentID != nil {
		parent = *req.ParentID
	}

	// ② 校验视频存在：不存在拒绝评论，避免孤儿评论
	ok, err := s.repo.FindVideoExist(c, videoID)
	if err != nil {
		return nil, fmt.Errorf("Method:comment.service.Create: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("Method:comment.service.Create: %w", codeErrors.ErrVideoNotFound)
	}

	// ③ 回复时解析树根与直接父评论（校验父评论存在）；根评论跳过
	var rootID uint
	var parentComm *modelsComments.VideoComments
	var parentErr error
	if parent > 0 {
		parentComm, rootID, parentErr = s.resolveRootID(c, parent)
		if parentErr != nil {
			return nil, fmt.Errorf("Method:comment.service.Create: %w", parentErr)
		}
	}

	// ④ 组装评论实体并落库
	comm := &modelsComments.VideoComments{
		VideoID:  videoID,
		ParentID: parent,
		UserID:   userID,
		RootID:   rootID,
		Content:  req.Content,
	}

	if err := s.repo.Create(c, comm); err != nil {
		return nil, fmt.Errorf("Method:comment.service.Create: %w", err)
	}

	// ⑤ Create 落库后只回填了 ID，重查一次以通过 Preload 获得评论者 User（头像/昵称）；
	// 查询失败时回退到 comm（User 为空，响应里 user 字段不返回，不影响主流程）
	created, _ := s.repo.FindByID(c, comm.ID)
	if created == nil {
		created = comm
	}

	// ⑥ 回复通知：发给"直接父评论"的作者；父评论不存在/回复自己/通知器为 nil 时跳过
	if parent > 0 && s.notifier != nil {
		if parentErr == nil && parentComm != nil && parentComm.UserID != userID {
			// 发送者展示名：昵称优先，空则回退用户名，再兜底"用户"（created.User 即发送者本人）
			replyName := "用户"
			if created.User.ID != 0 {
				replyName = created.User.Nickname
				if replyName == "" {
					replyName = created.User.Username
				}
			}
			replyContent := fmt.Sprintf("%s回复了你的评论: %s", replyName, s.cutReply(comm.Content, 32))
			_ = s.notifier.SendNotification(c, parentComm.UserID, userID, modelsMessage.MsgTypeComment, videoID, replyContent)
		}
	}

	return s.toCommentResp(c, created), nil
}

// GetComments 视频根评论列表分页查询（每条根评论附带第一页回复预览与回复总数）。
// 排序：sort="hot" 按点赞数降序，其他值按创建时间倒序（最新在前）。
// userID 为当前登录用户 ID（游客传 0）：用于批量填充 IsLiked（当前用户是否已赞该评论），
// 游客不做点赞关系查询、IsLiked 恒为 false；回复总数用全量 len，预览超过 repliesPreviewLimit
// 条时只内嵌前几条，更多回复由前端通过子评论分页接口（ReplyListResp）加载。
func (s *Service) GetComments(c context.Context, videoID, userID uint, page, pageSize int, sort string) (*modelsComments.CommentListResp, error) {
	// ① 分页参数兜底：page 至少 1，pageSize 限制在 [1,64]
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 64 {
		pageSize = 16
	}

	// ② 根评论分页（parent_id=0 过滤子评论），返回列表与根评论总数
	list, total, err := s.repo.FindRootComments(c, videoID, sort, page, pageSize)
	if err != nil {
		return nil, fmt.Errorf("Method:comment.service.GetComments: %w", err)
	}

	// ③ 收集根评论 ID，供批量拉取回复与 IsLiked 查询复用
	rootIDs := make([]uint, 0, len(list))
	for _, r := range list {
		rootIDs = append(rootIDs, r.ID)
	}

	// ④ 一次 IN 查询拉回所有根评论下的全部回复（时间升序，含深层楼中楼平铺），
	//    后续组装回复预览与统计回复总数，避免逐条根评论的 N+1 查询
	var commList []modelsComments.VideoComments
	if len(list) > 0 {
		commList, err = s.repo.FindRepliesByRootIDs(c, rootIDs)
		if err != nil {
			return nil, fmt.Errorf("Method:comment.service.GetComments: %w", err)
		}
	}

	// ⑤ IsLiked 批量填充：一次 IN 查询 comment_likes 收齐当前用户已赞的评论 ID（游客跳过）
	likedSet := make(map[uint]bool)
	if userID > 0 {
		ids := make([]uint, 0, len(rootIDs)+len(commList))
		ids = append(ids, rootIDs...)
		for i := range commList {
			ids = append(ids, commList[i].ID)
		}
		liked, err := s.repo.FindLikedIDs(c, userID, ids)
		if err != nil {
			return nil, fmt.Errorf("Method:comment.service.GetComments: %w", err)
		}
		for _, id := range liked {
			likedSet[id] = true
		}
	}

	// ⑥ 按 RootID 分组组装回复（此刻 likedSet 已就绪，直接打标 IsLiked）
	replyMap := make(map[uint][]modelsComments.CommentResp, len(rootIDs))
	for i := range commList {
		resp := s.toCommentResp(c, &commList[i])
		resp.IsLiked = likedSet[resp.ID]
		replyMap[resp.RootID] = append(replyMap[resp.RootID], *resp)
	}

	// ⑦ 组装根评论：内嵌回复预览（截断）+ 回复总数 + IsLiked
	items := make([]modelsComments.CommentResp, 0, len(list))
	for _, l := range list {
		r := s.toCommentResp(c, &l)
		r.IsLiked = likedSet[l.ID]
		replies := replyMap[l.ID]
		r.ReplyTotal = uint(len(replies)) // 全量拉取下 len 即该根评论的回复总数
		// 内嵌预览只保留前 repliesPreviewLimit 条，更多回复走子评论分页接口加载
		if len(replies) > repliesPreviewLimit {
			replies = replies[:repliesPreviewLimit]
		}
		r.Replies = replies
		items = append(items, *r)
	}
	return &modelsComments.CommentListResp{
		List:     items,
		Total:    uint(total),
		Page:     uint16(page),
		PageSize: uint16(pageSize),
	}, nil
}

// GetReplies 某根评论下的子评论分页查询（楼中楼"加载更多"，对应子评论分页接口 ReplyListResp）。
// 与 GetComments 的内嵌预览互补：列表接口只内嵌前 repliesPreviewLimit 条回复，
// 超过部分由前端按 root_id 调用本接口分页加载。
// userID 为当前登录用户 ID（游客传 0）：用于批量填充 IsLiked，游客恒为 false。
func (s *Service) GetReplies(c context.Context, rootID, userID uint, page, pageSize int) (*modelsComments.ReplyListResp, error) {
	// ① 分页参数兜底：page 至少 1，pageSize 限制在 [1,64]
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 64 {
		pageSize = 16
	}

	// ② 按根评论 ID 分页拉取回复（root_id=?，时间升序，含深层楼中楼平铺），返回列表与回复总数
	list, total, err := s.repo.FindRepliesByRootID(c, rootID, page, pageSize)
	if err != nil {
		return nil, fmt.Errorf("Method:comment.service.GetReplies: %w", err)
	}

	// ③ IsLiked 批量填充：一次 IN 查询 comment_likes 收齐当前用户已赞的评论 ID（游客跳过）
	items := make([]modelsComments.CommentResp, 0, len(list))
	likedSet := make(map[uint]bool)
	if userID > 0 {
		ids := make([]uint, 0, len(list))
		for i := range list {
			ids = append(ids, list[i].ID)
		}
		liked, err := s.repo.FindLikedIDs(c, userID, ids)
		if err != nil {
			return nil, fmt.Errorf("Method:comment.service.GetReplies: %w", err)
		}
		for _, id := range liked {
			likedSet[id] = true
		}
	}

	// ④ 组装响应：转 CommentResp 并打标 IsLiked（前端按 ParentID 递归组装楼中楼树）
	for i := range list {
		resp := s.toCommentResp(c, &list[i])
		resp.IsLiked = likedSet[list[i].ID]
		items = append(items, *resp)
	}

	return &modelsComments.ReplyListResp{
		List:     items,
		Total:    uint(total),
		Page:     uint16(page),
		PageSize: uint16(pageSize),
	}, nil
}

//================辅助方法===================

// resolveRootID 解析回复的树根 RootID 与直接父评论。
// 只查一次直接父评论即可：每条评论落库时已冗余 RootID，无需沿父链上溯（O(1)）：
//   - 父是根评论（RootID==0）→ 新回复的 RootID = 父评论自身 ID（父即树根）
//   - 父是子评论（RootID!=0）→ 新回复的 RootID = 父的 RootID（沿用树根，不改变归属）
//
// 返回值：直接父评论（作通知接收者）+ 继承得到的 RootID；父评论不存在时返回 ErrCommentNotFound。
func (s *Service) resolveRootID(c context.Context, parentID uint) (*modelsComments.VideoComments, uint, error) {
	// ① 查直接父评论（Preload User 供通知文案兜底使用）
	parent, err := s.repo.FindByID(c, parentID)
	if err != nil {
		return nil, 0, fmt.Errorf("Method:comment.service.resolveRootID: %w", err)
	}
	if parent == nil {
		return nil, 0, fmt.Errorf("Method:comment.service.resolveRootID: %w", codeErrors.ErrCommentNotFound)
	}
	// ② RootID 继承：父是根评论（RootID==0）→ 取父自身 ID；父是子评论 → 沿用父的 RootID
	rootID := parent.RootID
	if rootID == 0 {
		rootID = parentID
	}
	return parent, rootID, nil
}

// cutReply 截断通知文案中的评论内容：全部展示不美观，也存在被长评论刷屏的可能。
// 按 rune 截断，保证中文/emoji 等多字节字符不被切坏；超过 maxLen 时以"..."结尾。
func (s *Service) cutReply(content string, maxLen int) string {
	runes := []rune(content)
	if len(runes) <= maxLen {
		return content
	}
	return string(runes[:maxLen]) + "..."
}

// toCommentResp 将评论实体转换为响应 DTO（读方向基础字段，IsLiked 由点赞查询另行补充）。
// 评论者 User 存在（未软删，Preload 命中）时经 toresp 转成 UserBrief（头像 objKey → 完整 URL）。
func (s *Service) toCommentResp(c context.Context, comm *modelsComments.VideoComments) *modelsComments.CommentResp {
	resp := &modelsComments.CommentResp{
		ID:        comm.ID,
		VideoID:   comm.VideoID,
		UserID:    comm.UserID,
		ParentID:  comm.ParentID,
		RootID:    comm.RootID,
		Content:   comm.Content,
		Likes:     comm.Likes,
		CreatedAt: comm.CreatedAt,
	}
	if comm.User.ID != 0 {
		resp.User = s.toresp.ToUserBriefResp(&comm.User)
	}
	return resp
}
