package user

import (
	usermodel "LikeBili/internal/models/user"
	"context"
	"fmt"

	"gorm.io/gorm"
)

// 封装用户数据库操作，不含任何业务逻辑
type Repository struct {
	db *gorm.DB
}

// user实例
func NewRepository(db *gorm.DB) *Repository {
	return (&Repository{db: db})
}

func (r *Repository) FindById(c context.Context, userid uint) (*usermodel.User, error) {
	var user usermodel.User
	result := r.db.WithContext(c).Where("id = ?", userid).First(&user)
	//查询不到判断是否为用户不存在
	if result.Error == gorm.ErrRecordNotFound {
		return nil, nil
	} else if result.Error != nil {
		return nil, fmt.Errorf("Method:user.repository.FindById: %w", result.Error)
	}
	return &user, nil
}

// 更新用户信息，使用updates限制只能更新三个选项
func (r *Repository) Update(c context.Context, user *usermodel.User) error {
	//因为传入的user包含主键，且我们只需要对一个user进行修改，所以使用Models不需要使用零值结构体，使用user这个具体实例即可
	result := r.db.WithContext(c).Model(user).Updates(map[string]any{
		"nickname": user.Nickname,
		"avatar":   user.Avatar,
		"bio":      user.Bio,
	})
	if result.Error != nil {
		return fmt.Errorf("Method:user.repository.Update: %w", result.Error)
	}
	return nil
}
