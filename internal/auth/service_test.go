package auth

import (
	"testing"
	"time"

	"github.com/YourWisemaker/iot-api/internal/models"
	"github.com/YourWisemaker/iot-api/internal/store"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	mgr := NewManager(Config{Secret: "test-secret", TTL: time.Hour})
	return NewService(store.NewMemoryStore(0), mgr, time.Hour)
}

func TestCreateUserHashesPassword(t *testing.T) {
	svc := newTestService(t)
	u, err := svc.CreateUser("alice", "supersecret", []string{models.RoleViewer})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if u.PasswordHash == "" || u.PasswordHash == "supersecret" {
		t.Fatal("password must be hashed, not stored in plaintext")
	}
	if u.ID == "" {
		t.Fatal("expected generated ID")
	}
}

func TestCreateUserRejectsWeakPassword(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.CreateUser("bob", "short", nil); err != ErrWeakPassword {
		t.Fatalf("expected ErrWeakPassword, got %v", err)
	}
}

func TestCreateUserDefaultsToViewer(t *testing.T) {
	svc := newTestService(t)
	u, err := svc.CreateUser("carol", "supersecret", nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(u.Roles) != 1 || u.Roles[0] != models.RoleViewer {
		t.Fatalf("expected default viewer role, got %v", u.Roles)
	}
}

func TestEnsureUserIdempotent(t *testing.T) {
	svc := newTestService(t)
	created, err := svc.EnsureUser("admin", "supersecret", []string{models.RoleAdmin})
	if err != nil || !created {
		t.Fatalf("expected user to be created: created=%v err=%v", created, err)
	}
	created, err = svc.EnsureUser("admin", "supersecret", []string{models.RoleAdmin})
	if err != nil || created {
		t.Fatalf("expected no-op on second call: created=%v err=%v", created, err)
	}
}

func TestLoginSuccessAndFailure(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.CreateUser("alice", "supersecret", []string{models.RoleViewer}); err != nil {
		t.Fatalf("create: %v", err)
	}

	pair, err := svc.Login("alice", "supersecret")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if pair.AccessToken == "" || pair.RefreshToken == "" {
		t.Fatal("expected both tokens to be issued")
	}

	// The access token must carry the user's roles.
	claims, err := svc.tokens.Parse(pair.AccessToken)
	if err != nil {
		t.Fatalf("parse access token: %v", err)
	}
	if len(claims.Roles) != 1 || claims.Roles[0] != models.RoleViewer {
		t.Fatalf("unexpected roles %v", claims.Roles)
	}

	if _, err := svc.Login("alice", "wrong"); err != ErrInvalidCredentials {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
	if _, err := svc.Login("ghost", "whatever"); err != ErrInvalidCredentials {
		t.Fatalf("expected ErrInvalidCredentials for unknown user, got %v", err)
	}
}

func TestRefreshRotatesToken(t *testing.T) {
	svc := newTestService(t)
	_, _ = svc.CreateUser("alice", "supersecret", nil)
	pair, _ := svc.Login("alice", "supersecret")

	next, err := svc.Refresh(pair.RefreshToken)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if next.RefreshToken == pair.RefreshToken {
		t.Fatal("refresh token should be rotated")
	}
	// The old refresh token must no longer be valid (rotation invalidates it).
	if _, err := svc.Refresh(pair.RefreshToken); err == nil {
		t.Fatal("expected old refresh token to be invalid after rotation")
	}
}

func TestLogoutRevokesRefreshToken(t *testing.T) {
	svc := newTestService(t)
	_, _ = svc.CreateUser("alice", "supersecret", nil)
	pair, _ := svc.Login("alice", "supersecret")

	if err := svc.Logout(pair.RefreshToken); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := svc.Refresh(pair.RefreshToken); err == nil {
		t.Fatal("expected refresh to fail after logout")
	}
}

func TestRefreshTokenIsNotStoredInPlaintext(t *testing.T) {
	st := store.NewMemoryStore(0)
	svc := NewService(st, NewManager(Config{Secret: "s", TTL: time.Hour}), time.Hour)
	_, _ = svc.CreateUser("alice", "supersecret", nil)
	pair, _ := svc.Login("alice", "supersecret")

	// The raw refresh token must not be directly retrievable; only its hash is.
	if _, err := st.GetRefreshToken(pair.RefreshToken); err == nil {
		t.Fatal("raw refresh token should not be found by its plaintext value")
	}
}

func TestGetUser(t *testing.T) {
	svc := newTestService(t)
	created, _ := svc.CreateUser("alice", "supersecret", []string{models.RoleViewer})

	got, err := svc.GetUser(created.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if got.Username != "alice" {
		t.Fatalf("unexpected user %+v", got)
	}
	if _, err := svc.GetUser("ghost"); err == nil {
		t.Fatal("expected error for missing user")
	}
}

func TestUpdateUserRoles(t *testing.T) {
	svc := newTestService(t)
	created, _ := svc.CreateUser("alice", "supersecret", []string{models.RoleViewer})

	updated, err := svc.UpdateUser(created.ID, []string{models.RoleAdmin}, "")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if len(updated.Roles) != 1 || updated.Roles[0] != models.RoleAdmin {
		t.Fatalf("roles not updated: %v", updated.Roles)
	}
}

func TestUpdateUserPasswordRevokesSessionsAndAllowsNewLogin(t *testing.T) {
	svc := newTestService(t)
	created, _ := svc.CreateUser("alice", "supersecret", nil)
	pair, _ := svc.Login("alice", "supersecret")

	if _, err := svc.UpdateUser(created.ID, nil, "newsupersecret"); err != nil {
		t.Fatalf("update password: %v", err)
	}

	// Existing refresh tokens are revoked.
	if _, err := svc.Refresh(pair.RefreshToken); err == nil {
		t.Fatal("expected refresh tokens to be revoked after password change")
	}
	// Old password no longer works; new one does.
	if _, err := svc.Login("alice", "supersecret"); err != ErrInvalidCredentials {
		t.Fatalf("old password should fail, got %v", err)
	}
	if _, err := svc.Login("alice", "newsupersecret"); err != nil {
		t.Fatalf("new password should work, got %v", err)
	}
}

func TestUpdateUserRejectsWeakPassword(t *testing.T) {
	svc := newTestService(t)
	created, _ := svc.CreateUser("alice", "supersecret", nil)
	if _, err := svc.UpdateUser(created.ID, nil, "short"); err != ErrWeakPassword {
		t.Fatalf("expected ErrWeakPassword, got %v", err)
	}
}
