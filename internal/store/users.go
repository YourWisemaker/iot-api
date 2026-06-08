package store

import (
	"errors"

	"github.com/YourWisemaker/iot-api/internal/models"
)

// ErrDuplicateUser is returned when creating a user whose username exists.
var ErrDuplicateUser = errors.New("username already exists")

// UserStore persists users and refresh tokens. Both MemoryStore and MongoStore
// implement it.
type UserStore interface {
	CreateUser(u models.User) error
	GetUserByUsername(username string) (models.User, error)
	GetUserByID(id string) (models.User, error)
	ListUsers() []models.User
	UpdateUser(u models.User) error
	DeleteUser(id string) error

	StoreRefreshToken(rt models.RefreshToken) error
	GetRefreshToken(tokenHash string) (models.RefreshToken, error)
	DeleteRefreshToken(tokenHash string) error
	DeleteUserRefreshTokens(userID string) error
}
