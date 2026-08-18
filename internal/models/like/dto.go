package like

//点赞响应体
type LikeResp struct {
	Liked bool `json:"liked"`
	Count uint `json:"count"`
}
