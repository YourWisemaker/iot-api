package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sub, ok := SubjectFrom(r.Context()); ok {
			w.Header().Set("X-Subject", sub)
		}
		w.WriteHeader(http.StatusOK)
	})
}

func TestMiddlewareAllowsPublicPaths(t *testing.T) {
	m := testManager()
	h := m.Middleware(okHandler())

	for _, path := range []string{"/health", "/api/auth/login"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("public path %s: expected 200, got %d", path, rec.Code)
		}
	}
}

func TestMiddlewareRejectsMissingToken(t *testing.T) {
	m := testManager()
	h := m.Middleware(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/api/devices", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestMiddlewareAcceptsBearerToken(t *testing.T) {
	m := testManager()
	token, _ := m.Generate("admin", nil)
	h := m.Middleware(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/devices", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Header().Get("X-Subject") != "admin" {
		t.Fatalf("expected subject in context, got %q", rec.Header().Get("X-Subject"))
	}
}

func TestMiddlewareAcceptsQueryToken(t *testing.T) {
	m := testManager()
	token, _ := m.Generate("admin", nil)
	h := m.Middleware(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/ws?token="+token, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for query-token auth, got %d", rec.Code)
	}
}

func TestMiddlewareRejectsExpiredToken(t *testing.T) {
	m := testManager()
	h := m.Middleware(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/devices", nil)
	req.Header.Set("Authorization", "Bearer "+expiredToken(t, m))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for expired token, got %d", rec.Code)
	}
}

func TestRequireRoleAllowsMatchingRole(t *testing.T) {
	m := testManager()
	token, _ := m.Generate("u", []string{"viewer"})
	h := m.Middleware(RequireRole("viewer")(okHandler()))

	req := httptest.NewRequest(http.MethodGet, "/api/devices", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestRequireRoleAdminIsSuperuser(t *testing.T) {
	m := testManager()
	token, _ := m.Generate("u", []string{"admin"})
	h := m.Middleware(RequireRole("viewer")(okHandler()))

	req := httptest.NewRequest(http.MethodGet, "/api/devices", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin should satisfy any role, got %d", rec.Code)
	}
}

func TestRequireRoleForbidsMissingRole(t *testing.T) {
	m := testManager()
	token, _ := m.Generate("u", []string{"viewer"})
	h := m.Middleware(RequireRole("admin")(okHandler()))

	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}
