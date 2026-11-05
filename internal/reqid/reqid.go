// Package reqid propagates request IDs through context and HTTP headers.
package reqid

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

// Header is the canonical request ID header propagated across services.
const Header = "X-Request-ID"

type ctxKey struct{}

// New generates a random hex request ID.
func New() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// FromContext returns the request ID stored in ctx, or "".
func FromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKey{}).(string); ok {
		return v
	}
	return ""
}

// WithContext returns a context carrying id.
func WithContext(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// Middleware extracts or generates a request ID, stores it in the request
// context and echoes it in the response header.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(Header)
		if id == "" {
			id = New()
		}
		w.Header().Set(Header, id)
		next.ServeHTTP(w, r.WithContext(WithContext(r.Context(), id)))
	})
}
