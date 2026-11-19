// Package authz resolves effective permissions and enforces them. Every check fails closed (§5.2).
package authz

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/alumasinde/profy/internal/http/response"
	"github.com/alumasinde/profy/internal/shared/reqctx"
)

var known = func() map[string]bool {
	m := make(map[string]bool, len(Catalogue))
	for _, p := range Catalogue {
		m[p.Key] = true
	}
	return m
}()

// Known reports whether key is in the catalogue.
func Known(key string) bool { return known[key] }

type cached struct {
	perms   map[string]bool
	version uint64
	at      time.Time
}

// Service resolves and caches effective permissions per user.
// Single-instance invalidation is in-process; the TTL bounds staleness otherwise (§5.6).
type Service struct {
	db  *sql.DB
	ttl time.Duration

	mu      sync.Mutex
	version uint64
	cache   map[uint64]cached
}

// New creates the service.
func New(db *sql.DB, ttl time.Duration) *Service {
	return &Service{db: db, ttl: ttl, cache: map[uint64]cached{}}
}

// Invalidate drops all cached permissions; call after any role or assignment change.
func (s *Service) Invalidate() {
	s.mu.Lock()
	s.version++
	s.cache = map[uint64]cached{}
	s.mu.Unlock()
}

// Permissions returns the user's effective permission set. On error it returns an empty set.
func (s *Service) Permissions(ctx context.Context, userID uint64) (map[string]bool, error) {
	s.mu.Lock()
	c, ok := s.cache[userID]
	version := s.version
	s.mu.Unlock()
	if ok && c.version == version && time.Since(c.at) < s.ttl {
		return c.perms, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT p.permission_key
		FROM user_roles ur
		JOIN users u            ON u.id = ur.user_id AND u.status = 'active'
		JOIN role_permissions rp ON rp.role_id = ur.role_id
		JOIN permissions p      ON p.id = rp.permission_id
		WHERE ur.user_id = ?`, userID)
	if err != nil {
		return map[string]bool{}, err
	}
	defer rows.Close()
	perms := map[string]bool{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return map[string]bool{}, err
		}
		if known[k] { // ignore stale keys no longer in code
			perms[k] = true
		}
	}
	if err := rows.Err(); err != nil {
		return map[string]bool{}, err
	}
	s.mu.Lock()
	if s.version == version {
		s.cache[userID] = cached{perms: perms, version: version, at: time.Now()}
	}
	s.mu.Unlock()
	return perms, nil
}

// Can reports whether the principal holds perm. Unknown keys, a nil principal and lookup
// errors all deny.
func (s *Service) Can(ctx context.Context, p *reqctx.Principal, perm string) bool {
	if p == nil || !known[perm] {
		return false
	}
	perms, err := s.Permissions(ctx, p.UserID)
	if err != nil {
		slog.ErrorContext(ctx, "authz lookup failed; denying", "error", err)
		return false
	}
	return perms[perm]
}

// CanOwnOrAny implements the _own/_any rule (§5.5): the actor needs base+"_any", or
// base+"_own" AND to be the owner. ownerUserID == 0 means "no owner" and never matches.
func (s *Service) CanOwnOrAny(ctx context.Context, p *reqctx.Principal, base string, ownerUserID uint64) bool {
	if p == nil {
		return false
	}
	if s.Can(ctx, p, base+"_any") {
		return true
	}
	return ownerUserID != 0 && ownerUserID == p.UserID && s.Can(ctx, p, base+"_own")
}

// RequireAuth rejects unauthenticated requests with 401.
func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if reqctx.PrincipalFrom(r.Context()) == nil {
			response.Fail(w, r, response.ErrUnauthenticated)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Require returns middleware enforcing a permission: 401 without a principal, 403 without
// the permission. It panics at startup if perm is not in the catalogue.
func (s *Service) Require(perm string) func(http.Handler) http.Handler {
	if !known[perm] {
		panic("authz: unknown permission " + perm)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := reqctx.PrincipalFrom(r.Context())
			if p == nil {
				response.Fail(w, r, response.ErrUnauthenticated)
				return
			}
			if !s.Can(r.Context(), p, perm) {
				response.Fail(w, r, response.ErrForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
