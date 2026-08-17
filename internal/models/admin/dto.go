package admin

type AdminReq struct {
	Status uint8 `json:"status" validate:"oneof=1 2 3"` //由管理员申请时使用，1待审核，2审核成功，3，审核失败
}
