package auth

import (
	"LikeBili/internal/repository/auth"

	"gorm.io/gorm"
)

type Service struct {
	repo *auth.Repository
	db   *gorm.DB
}
