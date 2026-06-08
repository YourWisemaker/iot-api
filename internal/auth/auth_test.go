package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func testManager() *Manager {
	return NewManager(Config{Secret: "test-secret", TTL: time.Hour})
}

// expiredToken mints a token that expired in the past, signed with the
// manager's own secret, to exercise expiry validation.
func expiredToken(t *testing.T, m *Manager) string {
	t.Helper()
	past := time.Now().Add(-2 * time.Hour)
	claims := Claims{RegisteredClaims: jwt.RegisteredClaims{
		Subject:   "admin",
		Issuer:    m.issuer,
		IssuedAt:  jwt.NewNumericDate(past),
		ExpiresAt: jwt.NewNumericDate(past.Add(time.Hour)),
	}}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
	if err != nil {
		t.Fatalf("sign expired token: %v", err)
	}
	return signed
}

func TestNewManagerDisabledWithoutSecret(t *testing.T) {
	if NewManager(Config{}) != nil {
		t.Fatal("expected nil manager when no secret configured")
	}
}

func TestGenerateAndParse(t *testing.T) {
	m := testManager()
	token, err := m.Generate("user-1", []string{"admin", "viewer"})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	claims, err := m.Parse(token)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if claims.Subject != "user-1" {
		t.Fatalf("expected subject user-1, got %s", claims.Subject)
	}
	if len(claims.Roles) != 2 {
		t.Fatalf("unexpected roles %v", claims.Roles)
	}
}

func TestParseRejectsTamperedToken(t *testing.T) {
	m := testManager()
	token, _ := m.Generate("u", nil)
	if _, err := m.Parse(token + "x"); err == nil {
		t.Fatal("expected error for tampered token")
	}
}

func TestParseRejectsWrongSecret(t *testing.T) {
	m := testManager()
	token, _ := m.Generate("u", nil)
	other := NewManager(Config{Secret: "different"})
	if _, err := other.Parse(token); err == nil {
		t.Fatal("expected error for token signed with a different secret")
	}
}

func TestParseRejectsExpiredToken(t *testing.T) {
	m := testManager()
	if _, err := m.Parse(expiredToken(t, m)); err == nil {
		t.Fatal("expected expired token to be rejected")
	}
}

func TestParseEmptyToken(t *testing.T) {
	if _, err := testManager().Parse(""); err == nil {
		t.Fatal("expected error for empty token")
	}
}
