package auth

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/alumasinde/profy/internal/authz"
	"github.com/alumasinde/profy/internal/config"
	"github.com/alumasinde/profy/internal/database"
	"github.com/alumasinde/profy/internal/http/response"
	"github.com/alumasinde/profy/internal/i18n"
	"github.com/alumasinde/profy/internal/modules/audit"
	"github.com/alumasinde/profy/internal/modules/notifications"
	"github.com/alumasinde/profy/internal/modules/settings"
	"github.com/alumasinde/profy/internal/modules/users"
	"github.com/alumasinde/profy/internal/security"
	"github.com/alumasinde/profy/internal/shared/ids"
	"github.com/alumasinde/profy/internal/shared/reqctx"
)

// Service implements the authentication flows.
type Service struct {
	db       *sql.DB
	cfg      *config.Config
	log      *slog.Logger
	settings *settings.Service
	i18n     *i18n.Catalogue
	authz    *authz.Service
	limiter  *security.Limiter
	hasher   *security.OTPHasher
	senders  map[string]notifications.Sender
	users    users.Repository
	cookies  Cookies
	pending  sync.WaitGroup

	dummyPINHash string
}

// Deps are the service dependencies.
type Deps struct {
	DB       *sql.DB
	Config   *config.Config
	Log      *slog.Logger
	Settings *settings.Service
	I18n     *i18n.Catalogue
	Authz    *authz.Service
	Limiter  *security.Limiter
	Senders  []notifications.Sender
}

// NewService builds the auth service.
func NewService(d Deps) *Service {
	s := &Service{
		db: d.DB, cfg: d.Config, log: d.Log, settings: d.Settings, i18n: d.I18n, authz: d.Authz,
		limiter: d.Limiter, hasher: security.NewOTPHasher(d.Config.OTPHMACKey),
		senders: map[string]notifications.Sender{}, cookies: Cookies{Secure: d.Config.SecureCookies()},
	}
	for _, snd := range d.Senders {
		s.senders[snd.Channel()] = snd
	}
	// Used to equalize PIN-login timing when the account or credential does not exist.
	s.dummyPINHash, _ = security.HashSecret(security.RandomDigits(6))
	return s
}

// Cookies exposes cookie naming (used by the app shell).
func (s *Service) Cookies() Cookies { return s.cookies }

// ---- Registration and OTP login -------------------------------------------------------

// RegisterInput is the registration request.
type RegisterInput struct {
	FirstName   string  `json:"first_name"`
	MiddleName  *string `json:"middle_name"`
	LastName    string  `json:"last_name"`
	Destination string  `json:"destination"` // email or Kenyan mobile number
	AcceptTerms bool    `json:"accept_terms"`
	Locale      string  `json:"locale"`
}

// Register validates input and sends a verification code. If the destination already has an
// account, the same response is returned and the code signs the user in instead, so the
// response never reveals whether an account exists (§11.4).
func (s *Service) Register(ctx context.Context, in RegisterInput) (*IssuedChallenge, error) {
	names, errs := users.NormalizeNames(in.FirstName, in.MiddleName, in.LastName)
	dest, ok := parseDestination(in.Destination)
	if !ok {
		errs["destination"] = "invalid"
	}
	if !in.AcceptTerms {
		errs["accept_terms"] = "required"
	}
	locale := in.Locale
	if locale == "" || !s.i18n.Has(locale) {
		locale = i18n.DefaultLocale
	}
	if len(errs) > 0 {
		return nil, response.Validation(errs)
	}
	if !s.channelEnabled(ctx, dest.channel) {
		return nil, response.New(http.StatusUnprocessableEntity, response.CodeChannelDisabled,
			"Verification by this channel is not available. Use another email or phone number.")
	}
	existing, err := s.users.ByDestination(ctx, s.db, dest.channel, dest.value)
	switch {
	case err == nil:
		if existing.Status != users.StatusActive {
			return s.issueChallenge(ctx, challengeSpec{purpose: PurposeLogin, dest: dest, decoy: true})
		}
		id := existing.ID
		return s.issueChallenge(ctx, challengeSpec{purpose: PurposeLogin, dest: dest, userID: &id})
	case errors.Is(err, users.ErrNotFound):
	default:
		return nil, err
	}
	meta := map[string]any{
		"first_name": names.First, "last_name": names.Last, "locale": locale,
		"terms_version":   s.settings.String(ctx, settings.KeyTermsVersion),
		"privacy_version": s.settings.String(ctx, settings.KeyPrivacyVersion),
	}
	if names.Middle != nil {
		meta["middle_name"] = *names.Middle
	}
	return s.issueChallenge(ctx, challengeSpec{purpose: PurposeRegistration, dest: dest, metadata: meta})
}

// SendOTPInput requests a login code, or a re-verification code for the signed-in user.
type SendOTPInput struct {
	Destination string `json:"destination"`
	Purpose     string `json:"purpose"`
	Channel     string `json:"channel"` // sensitive_change only: which verified channel to use
}

// SendOTP issues a login or sensitive_change challenge.
func (s *Service) SendOTP(ctx context.Context, in SendOTPInput) (*IssuedChallenge, error) {
	switch in.Purpose {
	case PurposeLogin, "":
		return s.sendToExisting(ctx, in.Destination, PurposeLogin)
	case PurposeSensitiveChange:
		p := reqctx.PrincipalFrom(ctx)
		if p == nil {
			return nil, response.ErrUnauthenticated
		}
		u, err := s.users.ByID(ctx, s.db, p.UserID)
		if err != nil {
			return nil, err
		}
		dest, ok := verifiedDestination(u, in.Channel)
		if !ok || !s.channelEnabled(ctx, dest.channel) {
			return nil, response.New(http.StatusUnprocessableEntity, response.CodeChannelDisabled,
				"No verified channel is available for re-verification.")
		}
		id := u.ID
		return s.issueChallenge(ctx, challengeSpec{purpose: PurposeSensitiveChange, dest: dest, userID: &id})
	default:
		return nil, response.Validation(map[string]string{"purpose": "invalid"})
	}
}

// verifiedDestination picks a verified channel, preferring the requested one.
func verifiedDestination(u *users.User, prefer string) (destination, bool) {
	var opts []destination
	if u.PhoneE164 != nil && u.PhoneVerifiedAt != nil {
		opts = append(opts, destination{ChannelSMS, *u.PhoneE164})
	}
	if u.Email != nil && u.EmailVerifiedAt != nil {
		opts = append(opts, destination{ChannelEmail, *u.Email})
	}
	for _, o := range opts {
		if o.channel == prefer {
			return o, true
		}
	}
	if len(opts) > 0 && prefer == "" {
		return opts[0], true
	}
	return destination{}, false
}

// sendToExisting issues a challenge for an existing, active account with that verified
// destination; otherwise an identical-looking decoy.
func (s *Service) sendToExisting(ctx context.Context, raw, purpose string) (*IssuedChallenge, error) {
	dest, ok := parseDestination(raw)
	if !ok {
		return nil, response.Validation(map[string]string{"destination": "invalid"})
	}
	if !s.channelEnabled(ctx, dest.channel) {
		return nil, response.New(http.StatusUnprocessableEntity, response.CodeChannelDisabled,
			"Sign-in by this channel is not available. Use your other email or phone number.")
	}
	u, err := s.users.ByDestination(ctx, s.db, dest.channel, dest.value)
	if err != nil && !errors.Is(err, users.ErrNotFound) {
		return nil, err
	}
	verified := u != nil && ((dest.channel == ChannelSMS && u.PhoneVerifiedAt != nil) || (dest.channel == ChannelEmail && u.EmailVerifiedAt != nil))
	if u == nil || u.Status != users.StatusActive || !verified {
		return s.issueChallenge(ctx, challengeSpec{purpose: purpose, dest: dest, decoy: true})
	}
	id := u.ID
	return s.issueChallenge(ctx, challengeSpec{purpose: purpose, dest: dest, userID: &id})
}

// LoginResult carries what the handler needs to set cookies.
type LoginResult struct {
	Session     *issuedSession
	DeviceToken string
	NewUser     bool
}

// VerifyInput verifies a code.
type VerifyInput struct {
	ChallengeID string `json:"challenge_id"`
	Code        string `json:"code"`
	DeviceName  string `json:"device_name"`
}

// VerifyOTP completes registration, login or re-verification.
// For sensitive_change it returns (nil, nil) after marking the session re-authenticated.
func (s *Service) VerifyOTP(ctx context.Context, r *http.Request, in VerifyInput) (*LoginResult, error) {
	c, err := s.consumeChallenge(ctx, in.ChallengeID, strings.TrimSpace(in.Code), PurposeRegistration, PurposeLogin, PurposeSensitiveChange)
	if err != nil {
		return nil, err
	}
	if c.purpose == PurposeSensitiveChange {
		p := reqctx.PrincipalFrom(ctx)
		if p == nil || c.userID == nil || *c.userID != p.UserID {
			return nil, errInvalidCode
		}
		_, err := s.db.ExecContext(ctx, `UPDATE user_sessions SET reauthenticated_at = ? WHERE id = ? AND user_id = ?`,
			time.Now().UTC(), p.SessionID, p.UserID)
		return nil, err
	}
	deviceToken, _ := s.deviceToken(r)
	result := &LoginResult{DeviceToken: deviceToken}
	err = database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		var userID uint64
		switch c.purpose {
		case PurposeRegistration:
			id, created, err := s.createRegisteredUser(ctx, tx, c)
			if err != nil {
				return err
			}
			userID, result.NewUser = id, created
		case PurposeLogin:
			if c.userID == nil {
				return errInvalidCode
			}
			u, err := s.users.ByID(ctx, tx, *c.userID)
			if err != nil || u.Status != users.StatusActive {
				return errInvalidCode
			}
			userID = u.ID
		}
		if err := s.users.MarkVerified(ctx, tx, userID, c.dest.channel); err != nil {
			return err
		}
		return s.finishLogin(ctx, tx, userID, deviceToken, in.DeviceName, MethodOTP, true, result)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// createRegisteredUser creates the user from challenge metadata. If the destination was
// claimed concurrently, the verified owner of that destination is signed in instead.
func (s *Service) createRegisteredUser(ctx context.Context, tx *sql.Tx, c *verifiedChallenge) (uint64, bool, error) {
	str := func(k string) string { v, _ := c.metadata[k].(string); return v }
	var middle *string
	if m := str("middle_name"); m != "" {
		middle = &m
	}
	names, errs := users.NormalizeNames(str("first_name"), middle, str("last_name"))
	if len(errs) > 0 {
		return 0, false, errInvalidCode
	}
	n := users.NewUser{Names: names, Locale: str("locale")}
	if c.dest.channel == ChannelEmail {
		n.Email, n.EmailVerified = &c.dest.value, true
	} else {
		n.PhoneE164, n.PhoneVerified = &c.dest.value, true
	}
	id, pub, err := s.users.Create(ctx, tx, n)
	if database.IsDuplicate(err) {
		u, lerr := s.users.ByDestination(ctx, tx, c.dest.channel, c.dest.value)
		if lerr != nil || u.Status != users.StatusActive {
			return 0, false, errInvalidCode
		}
		return u.ID, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if err := s.users.AssignRole(ctx, tx, id, authz.RoleCustomer, nil); err != nil {
		return 0, false, err
	}
	ip := nullable(reqctx.ClientIP(ctx))
	for _, kv := range [][2]string{{"terms", str("terms_version")}, {"privacy", str("privacy_version")}} {
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_consents (user_id, consent_type, version, granted, ip_address)
			VALUES (?, ?, ?, 1, ?)`, id, kv[0], kv[1], ip); err != nil {
			return 0, false, err
		}
	}
	uid := id
	if err := audit.Record(ctx, tx, audit.Entry{ActorUserID: &uid, Action: "users.registered", EntityType: "user",
		EntityID: pub.String(), NewValues: map[string]any{"channel": c.dest.channel}}); err != nil {
		return 0, false, err
	}
	return id, true, nil
}

// finishLogin trusts the device (OTP/recovery only), creates a session and stamps the login.
func (s *Service) finishLogin(ctx context.Context, tx *sql.Tx, userID uint64, deviceToken, deviceName, method string, reauth bool, out *LoginResult) error {
	var deviceID *uint64
	if method != MethodPIN {
		id, err := s.trustDevice(ctx, tx, userID, deviceToken, deviceName)
		if err != nil {
			return err
		}
		deviceID = &id
		// A verified OTP unlocks PIN sign-in after lockout (§11.5).
		if _, err := tx.ExecContext(ctx, `UPDATE user_credentials SET failed_attempts = 0, blocked_until = NULL, locked_at = NULL
			WHERE user_id = ?`, userID); err != nil {
			return err
		}
	} else if id, err := s.trustedDeviceID(ctx, userID, deviceToken); err == nil && id != 0 {
		deviceID = &id
	}
	sess, err := s.createSession(ctx, tx, userID, deviceID, method, reauth)
	if err != nil {
		return err
	}
	out.Session = sess
	return s.users.TouchLogin(ctx, tx, userID)
}

// ---- PIN ------------------------------------------------------------------------------

var errInvalidCreds = response.New(http.StatusUnauthorized, response.CodeInvalidCreds, "The details you entered are not correct.")

// SetupPIN sets or replaces the PIN. Requires recent OTP re-verification (§11.6).
func (s *Service) SetupPIN(ctx context.Context, pin string) error {
	p := reqctx.PrincipalFrom(ctx)
	if p == nil {
		return response.ErrUnauthenticated
	}
	ok, _, err := s.recentlyReauthenticated(ctx, p.SessionID)
	if err != nil {
		return err
	}
	if !ok {
		return response.New(http.StatusForbidden, response.CodeReauthRequired, "Please confirm it's you with a verification code first.")
	}
	if reason := security.ValidatePIN(pin); reason != "" {
		return &response.Error{Status: http.StatusUnprocessableEntity, Code: response.CodeWeakPIN,
			Message: "Choose a less predictable PIN.", Fields: map[string]string{"pin": reason}}
	}
	return database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		if err := s.storePIN(ctx, tx, p.UserID, pin); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "auth.pin.set", EntityType: "user", EntityID: p.PublicID})
	})
}

func (s *Service) storePIN(ctx context.Context, ex database.Execer, userID uint64, pin string) error {
	h, err := security.HashSecret(pin)
	if err != nil {
		return err
	}
	_, err = ex.ExecContext(ctx, `INSERT INTO user_credentials (user_id, credential_type, secret_hash, failed_attempts)
		VALUES (?, 'pin', ?, 0) AS new
		ON DUPLICATE KEY UPDATE secret_hash = new.secret_hash, failed_attempts = 0, blocked_until = NULL, locked_at = NULL`,
		userID, h)
	return err
}

// PINLoginInput is a PIN login request.
type PINLoginInput struct {
	Destination string `json:"destination"`
	PIN         string `json:"pin"`
}

// LoginWithPIN signs in with a PIN on a device that previously completed OTP login.
func (s *Service) LoginWithPIN(ctx context.Context, r *http.Request, in PINLoginInput) (*LoginResult, error) {
	if ok, retry := s.limiter.Allow("pin_login:ip:"+reqctx.ClientIP(ctx), LimitPINLoginIP); !ok {
		return nil, response.RateLimited(retry)
	}
	if !s.settings.Flag(ctx, "pin_login") {
		return nil, errInvalidCreds
	}
	fail := func() (*LoginResult, error) {
		_, _ = security.VerifySecret(in.PIN, s.dummyPINHash) // equalize timing
		return nil, errInvalidCreds
	}
	dest, ok := parseDestination(in.Destination)
	if !ok || len(in.PIN) != 6 {
		return fail()
	}
	u, err := s.users.ByDestination(ctx, s.db, dest.channel, dest.value)
	if err != nil || u.Status != users.StatusActive {
		return fail()
	}
	deviceToken, isNew := s.deviceToken(r)
	if isNew {
		return fail()
	}
	deviceID, err := s.trustedDeviceID(ctx, u.ID, deviceToken)
	if err != nil {
		return nil, err
	}
	if deviceID == 0 {
		return fail()
	}
	var hash string
	var failed int
	var blocked, locked sql.NullTime
	err = s.db.QueryRowContext(ctx, `SELECT secret_hash, failed_attempts, blocked_until, locked_at
		FROM user_credentials WHERE user_id = ? AND credential_type = 'pin'`, u.ID).Scan(&hash, &failed, &blocked, &locked)
	if errors.Is(err, sql.ErrNoRows) {
		return fail()
	}
	if err != nil {
		return nil, err
	}
	// The caller holds a trusted device for this account, so lock state may be disclosed.
	if locked.Valid {
		return nil, response.New(http.StatusUnauthorized, "pin_locked", "PIN sign-in is locked. Sign in with a verification code.")
	}
	if blocked.Valid && time.Now().Before(blocked.Time) {
		return nil, response.RateLimited(time.Until(blocked.Time))
	}
	match, err := security.VerifySecret(in.PIN, hash)
	if err != nil {
		return nil, err
	}
	if !match {
		failed++
		var blockedUntil, lockedAt any
		if failed >= PINLockAfter {
			lockedAt = time.Now().UTC()
		} else if d := pinDelayFor(failed); d > 0 {
			blockedUntil = time.Now().UTC().Add(d)
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE user_credentials SET failed_attempts = ?, blocked_until = ?, locked_at = COALESCE(locked_at, ?)
			WHERE user_id = ? AND credential_type = 'pin'`, failed, blockedUntil, lockedAt, u.ID); err != nil {
			return nil, err
		}
		return nil, errInvalidCreds
	}
	result := &LoginResult{DeviceToken: deviceToken}
	err = database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE user_credentials SET failed_attempts = 0, blocked_until = NULL
			WHERE user_id = ? AND credential_type = 'pin'`, u.ID); err != nil {
			return err
		}
		return s.finishLogin(ctx, tx, u.ID, deviceToken, "", MethodPIN, false, result)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// ---- Recovery -------------------------------------------------------------------------

// RecoveryStart sends a recovery code (same non-enumerating behaviour as login).
func (s *Service) RecoveryStart(ctx context.Context, destinationInput string) (*IssuedChallenge, error) {
	return s.sendToExisting(ctx, destinationInput, PurposeRecovery)
}

// RecoveryCompleteInput completes recovery, optionally setting a new PIN.
type RecoveryCompleteInput struct {
	ChallengeID string  `json:"challenge_id"`
	Code        string  `json:"code"`
	NewPIN      *string `json:"new_pin"`
	DeviceName  string  `json:"device_name"`
}

// RecoveryComplete verifies the code, unlocks (and optionally replaces) the PIN, signs out
// every other session and signs in on this device.
func (s *Service) RecoveryComplete(ctx context.Context, r *http.Request, in RecoveryCompleteInput) (*LoginResult, error) {
	if in.NewPIN != nil {
		if reason := security.ValidatePIN(*in.NewPIN); reason != "" {
			return nil, &response.Error{Status: http.StatusUnprocessableEntity, Code: response.CodeWeakPIN,
				Message: "Choose a less predictable PIN.", Fields: map[string]string{"new_pin": reason}}
		}
	}
	c, err := s.consumeChallenge(ctx, in.ChallengeID, strings.TrimSpace(in.Code), PurposeRecovery)
	if err != nil {
		return nil, err
	}
	if c.userID == nil {
		return nil, errInvalidCode
	}
	deviceToken, _ := s.deviceToken(r)
	result := &LoginResult{DeviceToken: deviceToken}
	err = database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		u, err := s.users.ByID(ctx, tx, *c.userID)
		if err != nil || u.Status != users.StatusActive {
			return errInvalidCode
		}
		if in.NewPIN != nil {
			if err := s.storePIN(ctx, tx, u.ID, *in.NewPIN); err != nil {
				return err
			}
		} else if _, err := tx.ExecContext(ctx, `UPDATE user_credentials SET failed_attempts = 0, blocked_until = NULL, locked_at = NULL
			WHERE user_id = ?`, u.ID); err != nil {
			return err
		}
		if _, err := revokeSessions(ctx, tx, u.ID, 0, "recovery"); err != nil {
			return err
		}
		if err := s.finishLogin(ctx, tx, u.ID, deviceToken, in.DeviceName, MethodRecovery, true, result); err != nil {
			return err
		}
		uid := u.ID
		return audit.Record(ctx, tx, audit.Entry{ActorUserID: &uid, Action: "auth.recovery.completed", EntityType: "user",
			EntityID: u.PublicID.String(), NewValues: map[string]any{"pin_replaced": in.NewPIN != nil}})
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// ---- Sessions -------------------------------------------------------------------------

// SessionView is a session in the account security list.
type SessionView struct {
	ID         string    `json:"id"`
	AuthMethod string    `json:"auth_method"`
	DeviceName string    `json:"device_name"`
	UserAgent  string    `json:"user_agent"`
	IPAddress  string    `json:"ip_address"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	IsCurrent  bool      `json:"is_current"`
}

// ListSessions returns the signed-in user's active sessions.
func (s *Service) ListSessions(ctx context.Context) ([]SessionView, error) {
	p := reqctx.PrincipalFrom(ctx)
	if p == nil {
		return nil, response.ErrUnauthenticated
	}
	rows, err := s.db.QueryContext(ctx, `SELECT s.id, s.public_id, s.auth_method, COALESCE(d.name, ''), COALESCE(s.user_agent, ''),
			COALESCE(s.ip_address, ''), s.created_at, s.last_seen_at
		FROM user_sessions s LEFT JOIN user_devices d ON d.id = s.device_id
		WHERE s.user_id = ? AND s.revoked_at IS NULL AND s.absolute_expires_at > ? AND s.idle_expires_at > ?
		ORDER BY s.last_seen_at DESC LIMIT 100`, p.UserID, time.Now().UTC(), time.Now().UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SessionView{}
	for rows.Next() {
		var v SessionView
		var id uint64
		var pub ids.UUID
		if err := rows.Scan(&id, &pub, &v.AuthMethod, &v.DeviceName, &v.UserAgent, &v.IPAddress, &v.CreatedAt, &v.LastSeenAt); err != nil {
			return nil, err
		}
		v.ID, v.IsCurrent = pub.String(), id == p.SessionID
		out = append(out, v)
	}
	return out, rows.Err()
}

// RevokeSession revokes one of the caller's own sessions (ownership enforced in SQL).
func (s *Service) RevokeSession(ctx context.Context, publicID string) error {
	p := reqctx.PrincipalFrom(ctx)
	if p == nil {
		return response.ErrUnauthenticated
	}
	pub, err := ids.Parse(publicID)
	if err != nil {
		return response.ErrNotFound
	}
	res, err := s.db.ExecContext(ctx, `UPDATE user_sessions SET revoked_at = ?, revoked_reason = 'user'
		WHERE public_id = ? AND user_id = ? AND revoked_at IS NULL`, time.Now().UTC(), pub, p.UserID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return response.ErrNotFound
	}
	return nil
}

// RevokeOtherSessions signs out every other device.
func (s *Service) RevokeOtherSessions(ctx context.Context) (int64, error) {
	p := reqctx.PrincipalFrom(ctx)
	if p == nil {
		return 0, response.ErrUnauthenticated
	}
	return revokeSessions(ctx, s.db, p.UserID, p.SessionID, "user_others")
}

// Logout revokes the current session.
func (s *Service) Logout(ctx context.Context) error {
	p := reqctx.PrincipalFrom(ctx)
	if p == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `UPDATE user_sessions SET revoked_at = ?, revoked_reason = 'logout' WHERE id = ? AND revoked_at IS NULL`,
		time.Now().UTC(), p.SessionID)
	return err
}
