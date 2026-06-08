// Package auth provides JWT issuing/verification and HTTP middleware for
// protecting the platform's API and WebSocket endpoints.
package auth

import (
	"crypto/subtle"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ErrInvalidToken is returned when a token cannot be validated.
var ErrInvalidToken = errors.New("invalid or expired token")

// Claims is the JWT payload issued by the platform.
type Claims struct {
	Roles []string `json:"roles,omitempty"`
	jwt.RegisteredClaims
}

// Manager issues and validates JWTs and authenticates login credentials.
type Manager struct {
	secret   []byte
	issuer   string
	ttl      time.Duration
	username string
	password string
}

// Config configures the auth Manager.
type Config struct {
	Secret   string
	Issuer   string
	TTL      time.Duration
	Username string
	Password string
}

// NewManager builds a Manager. It returns nil when no secret is provided,
// signalling that authentication is disabled.
func NewManager(cfg Config) *Manager {
	if cfg.Secret == "" {
		return nil
	}
	if cfg.TTL <= 0 {
		cfg.TTL = time.Hour
	}
	if cfg.Issuer == "" {
		cfg.Issuer = "iot-api"
	}
	return &Manager{
		secret:   []byte(cfg.Secret),
		issuer:   cfg.Issuer,
		ttl:      cfg.TTL,
		username: cfg.Username,
		password: cfg.Password,
	}
}

// TTL returns the configured token lifetime.
func (m *Manager) TTL() time.Duration { return m.ttl }

// LoginEnabled reports whether credential-based login is configured.
func (m *Manager) LoginEnabled() bool {
	return m.username != "" && m.password != ""
}

// ValidateCredentials checks a username/password pair in constant time.
func (m *Manager) ValidateCredentials(username, password string) bool {
	if !m.LoginEnabled() {
		return false
	}
	userOK := subtle.ConstantTimeCompare([]byte(username), []byte(m.username)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(password), []byte(m.password)) == 1
	return userOK && passOK
}

// Generate issues a signed token for the subject with the given roles.
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

// Parse validates a token string and returns its claims.
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
