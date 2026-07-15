package server

import (
	"context"
	"net/http"
	"strings"
)

// authCtxKey scopes the binding stash so it cannot collide with caller-provided
// values. Unexported per Go context conventions.
type authCtxKey struct{}

// BindingFromContext retrieves the ScopeBinding the auth middleware stashed on
// the request context. Handlers MUST call this rather than re-parsing the
// Authorization header — the middleware has already validated the token.
func BindingFromContext(ctx context.Context) (ScopeBinding, bool) {
	b, ok := ctx.Value(authCtxKey{}).(ScopeBinding)
	return b, ok
}

// AuthMiddleware enforces bearer-token auth backed by the registry. Returns 401
// INVALID_TOKEN for missing or unknown tokens, with the standard error
// envelope. On success, stashes the resolved ScopeBinding on the request
// context for downstream handlers.
func AuthMiddleware(registry *Registry) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerFromHeader(r.Header.Get("Authorization"))
			if !ok {
				WriteErrorEnvelope(w, http.StatusUnauthorized, ErrCodeInvalidToken,
					"missing or malformed Authorization header (expected: Bearer <token>)", nil)
				return
			}
			binding, ok := registry.LookupByToken(token)
			if !ok {
				WriteErrorEnvelope(w, http.StatusUnauthorized, ErrCodeInvalidToken,
					"unknown bearer token", nil)
				return
			}
			ctx := context.WithValue(r.Context(), authCtxKey{}, binding)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// bearerFromHeader extracts the token from "Bearer <token>". Tolerates extra
// whitespace and case-insensitive "bearer"; rejects empty tokens.
func bearerFromHeader(h string) (string, bool) {
	h = strings.TrimSpace(h)
	if h == "" {
		return "", false
	}
	parts := strings.SplitN(h, " ", 2)
	if len(parts) != 2 {
		return "", false
	}
	if !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	tok := strings.TrimSpace(parts[1])
	if tok == "" {
		return "", false
	}
	return tok, true
}
