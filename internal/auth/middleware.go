package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type contextKey string

// claimsKey stores the authenticated claims in the request context.
const claimsKey contextKey = "auth.claims"

// defaultPublicPaths are exempt from authentication.
var defaultPublicPaths = map[string]struct{}{
	"/health":           {},
	"/api/auth/login":   {},
	"/api/auth/refresh": {},
	"/api/auth/logout":  {},
}

// Middleware enforces a valid JWT on all non-public routes and stores the
// resulting claims in the request context.
func (m *Manager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := defaultPublicPaths[r.URL.Path]; ok {
			next.ServeHTTP(w, r)
			return
		}
		claims, err := m.Parse(extractToken(r))
		if err != nil {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		ctx := context.WithValue(r.Context(), claimsKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireRole returns middleware that allows the request only if the
// authenticated user holds the required role. The admin role is a superuser
// and satisfies any role requirement.
func RequireRole(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := ClaimsFrom(r.Context())
			if !ok {
				writeJSONError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			if !hasRole(claims.Roles, role) && !hasRole(claims.Roles, "admin") {
				writeJSONError(w, http.StatusForbidden, "insufficient permissions")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ClaimsFrom returns the authenticated claims from the request context.
func ClaimsFrom(ctx context.Context) (*Claims, bool) {
	c, ok := ctx.Value(claimsKey).(*Claims)
	return c, ok
}

// SubjectFrom returns the authenticated subject (user ID) from the context.
func SubjectFrom(ctx context.Context) (string, bool) {
	if c, ok := ClaimsFrom(ctx); ok {
		return c.Subject, true
	}
	return "", false
}

func hasRole(roles []string, want string) bool {
	for _, r := range roles {
		if r == want {
			return true
		}
	}
	return false
}

// extractToken pulls a bearer token from the Authorization header, falling back
// to a `token` query parameter (used by browser WebSocket clients that cannot
// set custom headers).
func extractToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		if after, ok := strings.CutPrefix(h, "Bearer "); ok {
			return strings.TrimSpace(after)
		}
	}
	return r.URL.Query().Get("token")
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
