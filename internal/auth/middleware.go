package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type contextKey string

// subjectKey stores the authenticated subject in the request context.
const subjectKey contextKey = "auth.subject"

// PublicPaths are exempt from authentication.
var defaultPublicPaths = map[string]struct{}{
	"/health":          {},
	"/api/auth/login":  {},
}

// Middleware returns an http middleware that enforces a valid JWT on all
// non-public routes. Public paths (health, login) pass through untouched.
func (m *Manager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := defaultPublicPaths[r.URL.Path]; ok {
			next.ServeHTTP(w, r)
			return
		}
		claims, err := m.Parse(extractToken(r))
		if err != nil {
			writeUnauthorized(w)
			return
		}
		ctx := context.WithValue(r.Context(), subjectKey, claims.Subject)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// SubjectFrom returns the authenticated subject from the request context.
func SubjectFrom(ctx context.Context) (string, bool) {
	sub, ok := ctx.Value(subjectKey).(string)
	return sub, ok
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

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
}
