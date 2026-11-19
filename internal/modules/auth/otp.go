package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/alumasinde/profy/internal/database"
	"github.com/alumasinde/profy/internal/http/response"
	"github.com/alumasinde/profy/internal/modules/notifications"
	"github.com/alumasinde/profy/internal/modules/settings"
	"github.com/alumasinde/profy/internal/security"
	"github.com/alumasinde/profy/internal/shared/ids"
	"github.com/alumasinde/profy/internal/shared/reqctx"
)

var errInvalidCode = response.New(http.StatusUnauthorized, response.CodeInvalidCode, "The code is invalid or has expired.")

// destination is a normalized login channel.
type destination struct {
	channel string
	value   string
}

// parseDestination normalizes an email or Kenyan mobile number.
func parseDestination(input string) (destination, bool) {
	input = strings.TrimSpace(input)
	if strings.Contains(input, "@") {
		if e, ok := security.NormalizeEmail(input); ok {
			return destination{ChannelEmail, e}, true
		}
		return destination{}, false
	}
	if p, ok := security.NormalizeKenyanPhone(input); ok {
		return destination{ChannelSMS, p}, true
	}
	return destination{}, false
}

// channelEnabled checks the platform setting and that an adapter exists.
func (s *Service) channelEnabled(ctx context.Context, ch string) bool {
	if _, ok := s.senders[ch]; !ok {
		return false
	}
	return slices.Contains(s.settings.Strings(ctx, settings.KeyOTPChannels), ch)
}

// IssuedChallenge is returned to clients after sending a code.
type IssuedChallenge struct {
	ChallengeID       string `json:"challenge_id"`
	Channel           string `json:"channel"`
	DestinationMasked string `json:"destination_masked"`
	ExpiresIn         int    `json:"expires_in"`
	ResendAfter       int    `json:"resend_after"`
}

type challengeSpec struct {
	purpose  string
	dest     destination
	userID   *uint64 // nil for registration and decoys
	decoy    bool    // unknown/inactive destination: store an unverifiable challenge, send nothing
	metadata map[string]any
}

// issueChallenge enforces per-IP and per-destination limits, stores the HMAC of a fresh
// code and dispatches delivery asynchronously so response timing does not reveal whether
// an account exists (decoys are stored identically but never delivered).
func (s *Service) issueChallenge(ctx context.Context, spec challengeSpec) (*IssuedChallenge, error) {
	if ok, retry := s.limiter.Allow("otp_send:ip:"+reqctx.ClientIP(ctx), LimitOTPSendIP); !ok {
		return nil, response.RateLimited(retry)
	}
	now := time.Now().UTC()
	var daily, hourly int
	var last sql.NullTime
	err := s.db.QueryRowContext(ctx, `SELECT
			COUNT(*),
			COALESCE(SUM(created_at > ?), 0),
			MAX(created_at)
		FROM otp_challenges WHERE destination = ? AND created_at > ?`,
		now.Add(-time.Hour), spec.dest.value, now.Add(-24*time.Hour)).Scan(&daily, &hourly, &last)
	if err != nil {
		return nil, err
	}
	if daily >= OTPDailyCap {
		return nil, response.RateLimited(time.Until(last.Time.Add(24 * time.Hour)))
	}
	if last.Valid {
		if wait := cooldownFor(hourly) - now.Sub(last.Time); wait > 0 {
			return nil, response.RateLimited(wait)
		}
	}

	pub := ids.New()
	code := security.RandomDigits(OTPLength)
	hash := s.hasher.Hash(pub[:], code)
	if spec.decoy {
		hash = s.hasher.Hash(pub[:], security.RandomToken(16)) // can never match a 6-digit code
	}
	var meta any
	if spec.metadata != nil {
		b, err := json.Marshal(spec.metadata)
		if err != nil {
			return nil, err
		}
		meta = string(b)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO otp_challenges
		(public_id, purpose, channel, destination, user_id, code_hash, max_attempts, metadata, ip_address, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		pub, spec.purpose, spec.dest.channel, spec.dest.value, spec.userID, hash, OTPMaxAttempts, meta,
		nullable(reqctx.ClientIP(ctx)), now.Add(OTPTTL), now); err != nil {
		return nil, err
	}
	if !spec.decoy {
		s.deliver(ctx, spec.dest, code)
	}
	return &IssuedChallenge{
		ChallengeID: pub.String(), Channel: spec.dest.channel, DestinationMasked: security.MaskDestination(spec.dest.value),
		ExpiresIn: int(OTPTTL.Seconds()), ResendAfter: int(cooldownFor(hourly + 1).Seconds()),
	}, nil
}

// deliver sends the code in the background. The code exists only in memory and in the
// outgoing message; it is never logged (except by the local-only fake adapter).
func (s *Service) deliver(ctx context.Context, d destination, code string) {
	sender := s.senders[d.channel]
	locale := s.settings.String(ctx, settings.KeyDefaultLocale)
	siteName := s.settings.String(ctx, settings.KeySiteName)
	msg := notifications.Message{
		To:      d.value,
		Subject: s.i18n.T(locale, "otp.email.subject", "site_name", siteName),
		Body:    s.i18n.T(locale, "otp."+d.channel+".body", "code", code, "site_name", siteName, "minutes", int(OTPTTL.Minutes())),
	}
	bg := context.WithoutCancel(ctx)
	s.pending.Add(1)
	go func() {
		defer s.pending.Done()
		sendCtx, cancel := context.WithTimeout(bg, 30*time.Second)
		defer cancel()
		if err := sender.Send(sendCtx, msg); err != nil {
			s.log.ErrorContext(sendCtx, "otp delivery failed", "channel", d.channel, "error", err)
		}
	}()
}

// WaitDeliveries blocks until in-flight OTP deliveries finish (shutdown and tests).
func (s *Service) WaitDeliveries() { s.pending.Wait() }

// verifiedChallenge is a successfully consumed challenge.
type verifiedChallenge struct {
	id       uint64
	purpose  string
	dest     destination
	userID   *uint64
	metadata map[string]any
}

// consumeChallenge checks and consumes a challenge in its own short transaction, so failed
// attempts are always recorded. Follow-up work (user/session creation) runs afterwards; if it
// fails the user simply requests a new code.
func (s *Service) consumeChallenge(ctx context.Context, publicID, code string, allowed ...string) (*verifiedChallenge, error) {
	if ok, retry := s.limiter.Allow("otp_verify:ip:"+reqctx.ClientIP(ctx), LimitOTPVerifyIP); !ok {
		return nil, response.RateLimited(retry)
	}
	pub, err := ids.Parse(publicID)
	if err != nil || len(code) != OTPLength {
		return nil, errInvalidCode
	}
	var result *verifiedChallenge
	err = database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		var (
			c        verifiedChallenge
			hash     []byte
			attempts int
			maxAtt   int
			expires  time.Time
			consumed sql.NullTime
			userID   sql.NullInt64
			metaRaw  []byte
		)
		err := tx.QueryRowContext(ctx, `SELECT id, purpose, channel, destination, user_id, code_hash, attempts, max_attempts,
				expires_at, consumed_at, metadata
			FROM otp_challenges WHERE public_id = ? FOR UPDATE`, pub).
			Scan(&c.id, &c.purpose, &c.dest.channel, &c.dest.value, &userID, &hash, &attempts, &maxAtt, &expires, &consumed, &metaRaw)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if consumed.Valid || attempts >= maxAtt || !time.Now().Before(expires) || !slices.Contains(allowed, c.purpose) {
			return nil
		}
		match := security.Equal(hash, s.hasher.Hash(pub[:], code))
		var consumedAt any
		if match {
			consumedAt = time.Now().UTC()
		}
		if _, err := tx.ExecContext(ctx, `UPDATE otp_challenges SET attempts = attempts + 1, consumed_at = ? WHERE id = ?`,
			consumedAt, c.id); err != nil {
			return err
		}
		if !match {
			return nil
		}
		if userID.Valid {
			u := uint64(userID.Int64)
			c.userID = &u
		}
		if len(metaRaw) > 0 {
			if err := json.Unmarshal(metaRaw, &c.metadata); err != nil {
				return err
			}
		}
		result = &c
		return nil
	})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, errInvalidCode
	}
	return result, nil
}
