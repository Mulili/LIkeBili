package follow

import (
	modelsUser "LikeBili/internal/models/user"
	"time"
)

// ===============响应体======================
// 说明：关注/取关/回关均为动作接口，请求侧没有独立 DTO——
// 目标用户 ID 走 URL 路径参数（如 /users/:id/follow），操作者（当前登录用户）ID 从鉴权 token 取。
// 动作成功后返回空 data 即可；前端需要的新状态（是否互关）随列表行返回。

// FollowUserItemResp 关注/粉丝列表项（关注、粉丝两个列表共用同一行结构）。
// 一行代表"当前登录用户(me) 与 对端用户(u)"之间的一条单向关注关系：
//   - 关注列表：u 被我关注（行方向 me → u）
//   - 粉丝列表：u 关注了我（行方向 u → me）
type FollowUserItemResp struct {
	User        *modelsUser.UserBrief `json:"user"`         // 对端用户信息；用户已注销（软删除）时由 service 替换为官方统一"已注销"占位内容
	FollowedAt  time.Time             `json:"followed_at"`  // 关系建立时间：关注列表 = 我关注 u 的时刻；粉丝列表 = u 关注我的时刻
	IsFollowing bool                  `json:"is_following"` // 我是否已关注 u —— 粉丝列表填充：true 显示"已关注"，false 显示"回关"按钮
	IsFollowed  bool                  `json:"is_followed"`  // u 是否已关注我 —— 关注列表填充：true 表示互相关注
}

// FollowListResp 关注/粉丝分页列表响应体（两列表共用，direction 由请求路由区分）。
// 关注数/粉丝数即当前过滤方向下的 total，可复用于个人主页的统计展示。
type FollowListResp struct {
	Items    []FollowUserItemResp `json:"items"`     // 列表行（按关注时间倒序）
	Total    int64                `json:"total"`     // 该方向关系总数（关注数或粉丝数）
	Page     uint16               `json:"page"`      // 当前页码
	PageSize uint16               `json:"page_size"` // 每页数量
}
