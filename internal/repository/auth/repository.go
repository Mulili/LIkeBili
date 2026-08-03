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

// Create 创建新用户。
// 说明：注册前 Service 层虽会先查用户名/邮箱是否已存在，但"先查后插"在并发下存在竞态窗口——
// 两个请求可能同时通过预检查，此时由数据库唯一索引兜底，后插入的一方会触发 MySQL 错误码 1062。
// 这里把 1062 翻译成统一的业务错误（用户名或邮箱重复），而不是让前端收到 500。
func (r *Repository) Create(c context.Context, user *usermodel.User) error {
	if err := r.db.WithContext(c).Create(user).Error; err != nil {
		// errors.As 沿错误链逐层 Unwrap（fmt.Errorf %w → gorm 包装 → MySQL 驱动错误），
		// 直到取出最底层的 *mysql.MySQLError
		var mysqlErr *mysql.MySQLError
		// 1062 = ER_DUP_ENTRY（唯一索引/主键冲突），只对这种情况做业务翻译
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
			return codeErrors.ErrUsernameOrEmailExists
		}
		// 其他数据库错误（宕机、网络异常等）原样包裹，交由上层按 500 处理
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
