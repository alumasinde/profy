package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/alumasinde/profy/internal/database"
	"github.com/alumasinde/profy/internal/security"
	"github.com/alumasinde/profy/internal/shared/ids"
	"github.com/alumasinde/profy/internal/shared/reqctx"
)

// Cookies holds cookie naming and attributes. The __Host- prefix needs Secure cookies.
type Cookies struct {
	Secure bool
}

// SessionName is the session cookie name.
func (c Cookies) SessionName() string {
	if c.Secure {
		return "__Host-hl_session"
	}
	return "hl_session"
}

// DeviceName is the device cookie name.
func (c Cookies) DeviceName() string {
	if c.Secure {
		return "__Host-hl_device"
	}
	return "hl_device"
}

func (c Cookies) set(w http.ResponseWriter, name, value string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: "/", Secure: c.Secure, HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Expires: expires,
	})
}

func (c Cookies) clear(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", Secure: c.Secure, HttpOnly: true,
		SameSite: http.SameSiteLaxMode, MaxAge: -1})
}

type issuedSession struct {
	id        uint64
	publicID  ids.UUID
	token     string
	expiresAt time.Time
}

// createSession inserts a new session (always a fresh token: rotation on every login).
func (s *Service) createSession(ctx context.Context, ex database.Execer, userID uint64, deviceID *uint64, method string, reauth bool) (*issuedSession, error) {
	now := time.Now().UTC()
	token := security.RandomToken(32)
	pub := ids.New()
	abs := now.Add(s.cfg.SessionAbsoluteTTL)
	idle := now.Add(s.cfg.SessionIdleTTL)
	var reauthAt any
	if reauth {
		reauthAt = now
	}
	res, err := ex.ExecContext(ctx, `INSERT INTO user_sessions
		(public_id, user_id, device_id, token_hash, auth_method, ip_address, user_agent, reauthenticated_at,
		 last_seen_at, idle_expires_at, absolute_expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		pub, userID, deviceID, security.HashToken(token), method, nullable(reqctx.ClientIP(ctx)),
		nullable(reqctx.UserAgent(ctx)), reauthAt, now, idle, abs, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &issuedSession{id: uint64(id), publicID: pub, token: token, expiresAt: abs}, nil
}

// trustDevice upserts the device for the user, marking it trusted (OTP-verified).
func (s *Service) trustDevice(ctx context.Context, ex database.Execer, userID uint64, deviceToken, name string) (uint64, error) {
	now := time.Now().UTC()
	if len(name) > 120 {
		name = name[:120]
	}
	_, err := ex.ExecContext(ctx, `INSERT INTO user_devices (public_id, user_id, device_token_hash, name, trusted_at, last_seen_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?) AS new
		ON DUPLICATE KEY UPDATE trusted_at = new.trusted_at, last_seen_at = new.last_seen_at, revoked_at = NULL,
			name = IF(new.name = '', user_devices.name, new.name)`,
		ids.New(), userID, security.HashToken(deviceToken), name, now, now, now)
	if err != nil {
		return 0, err
	}
	var id uint64
	err = ex.QueryRowContext(ctx, `SELECT id FROM user_devices WHERE user_id = ? AND device_token_hash = ?`,
		userID, security.HashToken(deviceToken)).Scan(&id)
	return id, err
}

// trustedDeviceID returns the trusted, non-revoked device ID for the token, or 0.
func (s *Service) trustedDeviceID(ctx context.Context, userID uint64, deviceToken string) (uint64, error) {
	if deviceToken == "" {
		return 0, nil
	}
	var id uint64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM user_devices
		WHERE user_id = ? AND device_token_hash = ? AND trusted_at IS NOT NULL AND revoked_at IS NULL`,
		userID, security.HashToken(deviceToken)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// deviceToken returns the device cookie value or a new token.
func (s *Service) deviceToken(r *http.Request) (token string, isNew bool) {
	if c, err := r.Cookie(s.cookies.DeviceName()); err == nil && len(c.Value) >= 40 && len(c.Value) <= 64 {
		return c.Value, false
	}
	return security.RandomToken(32), true
}

// sessionRow is the state needed to authenticate a request.
type sessionRow struct {
	id, userID      uint64
	publicID        ids.UUID
	userPublicID    ids.UUID
	locale, status  string
	lastSeen        time.Time
	idleExp, absExp time.Time
	reauthAt        sql.NullTime
}

func (s *Service) loadSession(ctx context.Context, token string) (*sessionRow, error) {
	var r sessionRow
	err := s.db.QueryRowContext(ctx, `SELECT s.id, s.public_id, s.user_id, u.public_id, u.preferred_locale, u.status,
			s.last_seen_at, s.idle_expires_at, s.absolute_expires_at, s.reauthenticated_at
		FROM user_sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ? AND s.revoked_at IS NULL`, security.HashToken(token)).
		Scan(&r.id, &r.publicID, &r.userID, &r.userPublicID, &r.locale, &r.status, &r.lastSeen, &r.idleExp, &r.absExp, &r.reauthAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// Middleware resolves the session cookie into a principal. Invalid cookies are cleared and
// the request continues anonymously; protected routes then reject it.
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(s.cookies.SessionName())
		if err != nil || c.Value == "" || len(c.Value) > 64 {
			next.ServeHTTP(w, r)
			return
		}
		row, err := s.loadSession(r.Context(), c.Value)
		now := time.Now().UTC()
		if err != nil || row.status != "active" || !now.Before(row.idleExp) || !now.Before(row.absExp) {
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				s.log.ErrorContext(r.Context(), "session lookup failed", "error", err)
			}
			s.cookies.clear(w, s.cookies.SessionName())
			next.ServeHTTP(w, r)
			return
		}
		if now.Sub(row.lastSeen) > time.Minute {
			idle := now.Add(s.cfg.SessionIdleTTL)
			if idle.After(row.absExp) {
				idle = row.absExp
			}
			if _, err := s.db.ExecContext(r.Context(), `UPDATE user_sessions SET last_seen_at = ?, idle_expires_at = ? WHERE id = ?`,
				now, idle, row.id); err != nil {
				s.log.WarnContext(r.Context(), "session touch failed", "error", err)
			}
		}
		p := &reqctx.Principal{UserID: row.userID, PublicID: row.userPublicID.String(), SessionID: row.id,
			SessionPublicID: row.publicID.String(), Locale: row.locale}
		next.ServeHTTP(w, r.WithContext(reqctx.WithPrincipal(r.Context(), p)))
	})
}

// recentlyReauthenticated reports whether the current session verified OTP within ReauthWindow.
func (s *Service) recentlyReauthenticated(ctx context.Context, sessionID uint64) (bool, time.Time, error) {
	var at sql.NullTime
	err := s.db.QueryRowContext(ctx, `SELECT reauthenticated_at FROM user_sessions WHERE id = ?`, sessionID).Scan(&at)
	if err != nil {
		return false, time.Time{}, err
	}
	if !at.Valid {
		return false, time.Time{}, nil
	}
	until := at.Time.Add(ReauthWindow)
	return time.Now().Before(until), until, nil
}

// revokeSessions revokes the user's sessions, optionally keeping one.
func revokeSessions(ctx context.Context, ex database.Execer, userID uint64, keep uint64, reason string) (int64, error) {
	res, err := ex.ExecContext(ctx, `UPDATE user_sessions SET revoked_at = ?, revoked_reason = ?
		WHERE user_id = ? AND revoked_at IS NULL AND id <> ?`, time.Now().UTC(), reason, userID, keep)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// RevokeAllForUser revokes every session of a user (used by suspension).
func RevokeAllForUser(ctx context.Context, ex database.Execer, userID uint64, reason string) error {
	_, err := revokeSessions(ctx, ex, userID, 0, reason)
	return err
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
