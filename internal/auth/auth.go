// Package auth provides JWT issuing/verification, password-based user
// authentication backed by the database, refresh tokens, and HTTP middleware
// for authentication and role-based authorization.
package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ErrInvalidToken is returned when a token cannot be validated.
var ErrInvalidToken = errors.New("invalid or expired token")

// Claims is the JWT access-token payload issued by the platform.
type Claims struct {
	Roles []string `json:"roles,omitempty"`
	jwt.RegisteredClaims
}

// Manager issues and validates access JWTs.
type Manager struct {
	secret []byte
	issuer string
	ttl    time.Duration
}

// Config configures the JWT Manager.
type Config struct {
	Secret string
	Issuer string
	TTL    time.Duration
}

// NewManager builds a Manager. It returns nil when no secret is provided,
// signalling that authentication is disabled.
func NewManager(cfg Config) *Manager {
	if cfg.Secret == "" {
		return nil
	}
	if cfg.TTL <= 0 {
		cfg.TTL = 15 * time.Minute
	}
	if cfg.Issuer == "" {
		cfg.Issuer = "iot-api"
	}
	return &Manager{
		secret: []byte(cfg.Secret),
		issuer: cfg.Issuer,
		ttl:    cfg.TTL,
	}
}

// TTL returns the configured access-token lifetime.
func (m *Manager) TTL() time.Duration { return m.ttl }

// Generate issues a signed access token for the subject with the given roles.
func (m *Manager) Generate(subject string, roles []string) (string, error) {
	now := time.Now()
	claims := Claims{
		Roles: roles,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   subject,
			Issuer:    m.issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(m.ttl)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(m.secret)
}

// Parse validates an access token string and returns its claims.
func (m *Manager) Parse(tokenStr string) (*Claims, error) {
	if tokenStr == "" {
		return nil, ErrInvalidToken
	}
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return m.secret, nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer(m.issuer))
	if err != nil || !token.Valid {
		return nil, ErrInvalidToken
	}
	return claims, nil
}
