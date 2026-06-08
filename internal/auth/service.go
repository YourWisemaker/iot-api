package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/YourWisemaker/iot-api/internal/models"
	"github.com/YourWisemaker/iot-api/internal/store"
)

// Auth-related errors.
var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrWeakPassword       = errors.New("password must be at least 8 characters")
)

// TokenPair is returned on successful login or refresh.
type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

// Service manages users and issues/rotates tokens. It stores only bcrypt
// password hashes and SHA-256 hashes of refresh tokens.
type Service struct {
	users      store.UserStore
	tokens     *Manager
	refreshTTL time.Duration
	bcryptCost int
}

// NewService builds an auth Service. refreshTTL <= 0 defaults to 7 days.
func NewService(users store.UserStore, tokens *Manager, refreshTTL time.Duration) *Service {
	if refreshTTL <= 0 {
		refreshTTL = 7 * 24 * time.Hour
	}
	return &Service{
		users:      users,
		tokens:     tokens,
		refreshTTL: refreshTTL,
		bcryptCost: bcrypt.DefaultCost,
	}
}

// CreateUser hashes the password and persists a new user.
func (s *Service) CreateUser(username, password string, roles []string) (models.User, error) {
	if len(password) < 8 {
		return models.User{}, ErrWeakPassword
	}
	if len(roles) == 0 {
		roles = []string{models.RoleViewer}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), s.bcryptCost)
	if err != nil {
		return models.User{}, err
	}
	u := models.User{
		ID:           uuid.NewString(),
		Username:     username,
		PasswordHash: string(hash),
		Roles:        roles,
		CreatedAt:    time.Now().UTC(),
	}
	if err := s.users.CreateUser(u); err != nil {
		return models.User{}, err
	}
	return u, nil
}

// EnsureUser creates the user if the username does not already exist. It is
// used to seed an initial admin account at startup.
func (s *Service) EnsureUser(username, password string, roles []string) (bool, error) {
	if _, err := s.users.GetUserByUsername(username); err == nil {
		return false, nil // already exists
	} else if !errors.Is(err, store.ErrNotFound) {
		return false, err
	}
	if _, err := s.CreateUser(username, password, roles); err != nil {
		return false, err
	}
	return true, nil
}

// ListUsers returns all users.
func (s *Service) ListUsers() []models.User { return s.users.ListUsers() }

// GetUser returns a single user by ID.
func (s *Service) GetUser(id string) (models.User, error) {
	return s.users.GetUserByID(id)
}

// UpdateUser updates a user's roles and/or password. An empty password leaves
// the existing password unchanged; a non-empty password is validated, hashed,
// and all of the user's refresh tokens are revoked. A nil roles slice leaves
// roles unchanged.
func (s *Service) UpdateUser(id string, roles []string, password string) (models.User, error) {
	u, err := s.users.GetUserByID(id)
	if err != nil {
		return models.User{}, err
	}
	if roles != nil {
		u.Roles = roles
	}
	if password != "" {
		if len(password) < 8 {
			return models.User{}, ErrWeakPassword
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(password), s.bcryptCost)
		if err != nil {
			return models.User{}, err
		}
		u.PasswordHash = string(hash)
	}
	if err := s.users.UpdateUser(u); err != nil {
		return models.User{}, err
	}
	if password != "" {
		// Force re-authentication on all sessions after a password change.
		_ = s.users.DeleteUserRefreshTokens(u.ID)
	}
	return u, nil
}

// DeleteUser removes a user and revokes their refresh tokens.
func (s *Service) DeleteUser(id string) error { return s.users.DeleteUser(id) }

// authenticate verifies a username/password pair against the stored hash.
func (s *Service) authenticate(username, password string) (models.User, error) {
	u, err := s.users.GetUserByUsername(username)
	if err != nil {
		// Compare against a dummy hash to reduce username-enumeration timing.
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$invalidinvalidinvalidinvalidinvalidinvalidinvalidinv"), []byte(password))
		return models.User{}, ErrInvalidCredentials
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return models.User{}, ErrInvalidCredentials
	}
	return u, nil
}

// Login authenticates a user and returns an access/refresh token pair.
func (s *Service) Login(username, password string) (TokenPair, error) {
	u, err := s.authenticate(username, password)
	if err != nil {
		return TokenPair{}, err
	}
	return s.issueTokens(u)
}

// Refresh validates and rotates a refresh token, returning a new pair. The old
// refresh token is invalidated.
func (s *Service) Refresh(refreshToken string) (TokenPair, error) {
	hash := hashToken(refreshToken)
	rt, err := s.users.GetRefreshToken(hash)
	if err != nil {
		return TokenPair{}, ErrInvalidToken
	}
	// Rotate: invalidate the presented token regardless of outcome.
	_ = s.users.DeleteRefreshToken(hash)

	u, err := s.users.GetUserByID(rt.UserID)
	if err != nil {
		return TokenPair{}, ErrInvalidToken
	}
	return s.issueTokens(u)
}

// Logout revokes a single refresh token.
func (s *Service) Logout(refreshToken string) error {
	return s.users.DeleteRefreshToken(hashToken(refreshToken))
}

// issueTokens mints an access JWT and a persisted, hashed refresh token.
func (s *Service) issueTokens(u models.User) (TokenPair, error) {
	access, err := s.tokens.Generate(u.ID, u.Roles)
	if err != nil {
		return TokenPair{}, err
	}
	refresh, err := newOpaqueToken()
	if err != nil {
		return TokenPair{}, err
	}
	if err := s.users.StoreRefreshToken(models.RefreshToken{
		TokenHash: hashToken(refresh),
		UserID:    u.ID,
		ExpiresAt: time.Now().UTC().Add(s.refreshTTL),
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		return TokenPair{}, err
	}
	return TokenPair{
		AccessToken:  access,
		RefreshToken: refresh,
		TokenType:    "Bearer",
		ExpiresIn:    int(s.tokens.TTL().Seconds()),
	}, nil
}

// newOpaqueToken returns a cryptographically random, URL-safe token.
func newOpaqueToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken returns a hex-encoded SHA-256 of a token for safe storage/lookup.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
