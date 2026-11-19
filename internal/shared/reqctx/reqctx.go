// Package reqctx carries request-scoped values (request ID, client IP, principal) in a context.
package reqctx

import "context"

type key int

const (
	requestIDKey key = iota
	clientIPKey
	userAgentKey
	principalKey
	csrfKey
)

// WithCSRFToken stores the request's CSRF token so server-rendered forms can embed it.
func WithCSRFToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, csrfKey, token)
}

// CSRFToken returns the CSRF token for forms, or "".
func CSRFToken(ctx context.Context) string {
	v, _ := ctx.Value(csrfKey).(string)
	return v
}

// Principal is the authenticated identity attached to a request.
// It is resolved the same way for web sessions and (later) mobile bearer tokens.
type Principal struct {
	UserID          uint64
	PublicID        string
	SessionID       uint64
	SessionPublicID string
	Locale          string
}

// WithRequestID stores the request ID.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestID returns the request ID or "".
func RequestID(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey).(string)
	return v
}

// WithClient stores the client IP and user agent.
func WithClient(ctx context.Context, ip, ua string) context.Context {
	ctx = context.WithValue(ctx, clientIPKey, ip)
	return context.WithValue(ctx, userAgentKey, ua)
}

// ClientIP returns the client IP or "".
func ClientIP(ctx context.Context) string {
	v, _ := ctx.Value(clientIPKey).(string)
	return v
}

// UserAgent returns the (truncated) user agent or "".
func UserAgent(ctx context.Context) string {
	v, _ := ctx.Value(userAgentKey).(string)
	return v
}

// WithPrincipal stores the authenticated principal.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey, p)
}

// PrincipalFrom returns the principal, or nil when unauthenticated.
func PrincipalFrom(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalKey).(*Principal)
	return p
}
