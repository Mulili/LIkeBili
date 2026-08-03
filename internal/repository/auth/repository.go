package auth

import (
	usermodel "LikeBili/internal/models/user"
	codeErrors "LikeBili/pkg/errors"
	"context"
	"errors"
	"fmt"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

// 封装注册登录的数据库操作
type Repository struct {
	db *gorm.DB
}

// 注册实例
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// 创建新用户，如果使用重复的Username和Email则会直接返回数据库错误
func (r *Repository) Create(c context.Context, user *usermodel.User) error {
	if err := r.db.WithContext(c).Create(user).Error; err != nil {
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
			return codeErrors.ErrUsernameOrEmailExists
		}
		return fmt.Errorf("Method:auth.repository.Create: %w", err)
	}
	return nil
}

// 根据用户名实现查询用户（登录）
func (r *Repository) FindByUsername(c context.Context, username string) (*usermodel.User, error) {
	var user usermodel.User
	//查询不到用户返回nil，nil
	if err := r.db.WithContext(c).Where("username = ?", username).First(&user).Error; err == gorm.ErrRecordNotFound {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("Method:auth.repository.FindByUsername: %w", err)
	}
	//使用&将对象地址解析为user对象
	return &user, nil
}

// 根据邮箱实现查询用户（登录）
func (r *Repository) FindByEmail(c context.Context, email string) (*usermodel.User, error) {
	var user usermodel.User
	//查询不到用户返回nil，nil
	if err := r.db.WithContext(c).Where("email = ?", email).First(&user).Error; err == gorm.ErrRecordNotFound {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("Method:auth.repository.FindByEmail: %w", err)
	}
	//使用&将对象地址解析为user对象
	return &user, nil
}

// 登录时username或email匹配即可
func (r *Repository) FindByUsernameOrEmail(c context.Context, account string) (*usermodel.User, error) {
	var user usermodel.User
	if err := r.db.WithContext(c).Where("username = ? OR email = ?", account, account).First(&user).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("Method:auth.repository.FindByUsernameOrEmail: %w", err)
	}
	return &user, nil
}

// 根据用户ID查询用户（jwt）
func (r *Repository) FindByID(c context.Context, id uint) (*usermodel.User, error) {
	var user usermodel.User
	if err := r.db.WithContext(c).Where("id = ?", id).First(&user).Error; err == gorm.ErrRecordNotFound {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("Method:auth.repository.FindByID: %w", err)
	}
	return &user, nil
}
