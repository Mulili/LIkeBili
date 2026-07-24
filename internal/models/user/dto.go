package user

//==============Request===============
//新用户注册
type RegisterReq struct {
	Username string `json:"username" binding:"required,min=2,max=32"`
	Email    string `json:"email" binding:"required,email"` //按照邮箱格式进行校准:xxx@yy.ZZZ
	Password string `json:"password" binding:"required,min=8,max=32"`
}

//用户登录
type LoginReq struct {
	Account  string `json:"account" binding:"required,min=2,max=64"` //用户名或者邮箱
	Password string `json:"password" binding:"required,min=8,max=32"`
}

//修改个人主页
type UpdateProfileReq struct {
	Nickname *string `json:"nickname" binding:"omitempty,min=2,max=32"`
	Bio      *string `json:"bio" binding:"omitempty,min=0,max=512"`
	Avatar   *string `json:"avatar" binding:"omitempty,url,max=256"` //表示更换头像，如果没传值就不执行，传值判断是否符合url的格式
}

//===============Response=============
//登录或注册成功后返回用户信息--主页仅需显示用户昵称，头像，同时为了业务返回主键和用户名称
//确认你是谁
type LoginResp struct {
	ID       uint   `json:"id"`
	Username string `json:"username"`
	Nickname string `json:"nickname"`
	Avatar   string `json:"avatar"`
}

//详细信息
type UserInfoResp struct {
	ID        uint   `json:"id"`
	Username  string `json:"username"`
	Nickname  string `json:"nickname"`
	Avatar    string `json:"avatar"`
	Bio       string `json:"bio"`
	CreatedAt string `json:"created_at"`
}

//用于搜索列表，好友列表
type UserBrief struct {
	ID       uint   `json:"id"`
	Username string `json:"username"`
	Nickname string `json:"nickname"`
	Avatar   string `json:"avatar"`
}
