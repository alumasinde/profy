package providers

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/alumasinde/profy/internal/database"
	"github.com/alumasinde/profy/internal/http/response"
	"github.com/alumasinde/profy/internal/modules/audit"
	"github.com/alumasinde/profy/internal/modules/auth"
	"github.com/alumasinde/profy/internal/modules/notifications"
	"github.com/alumasinde/profy/internal/modules/settings"
	"github.com/alumasinde/profy/internal/security"
	"github.com/alumasinde/profy/internal/shared/reqctx"
)

// Claim error codes.
const (
	CodeClaimInvalid = "claim_invalid"
	CodeClaimed      = "already_claimed"
)

var errClaimInvalid = response.New(400, CodeClaimInvalid, "This claim link is invalid or has expired. Ask the team to send a new one.")

// AssistedInput is a staff-created draft listing (§7.6 assisted onboarding).
type AssistedInput struct {
	ProviderType     string `json:"provider_type"`
	DisplayName      string `json:"display_name"`
	ClaimChannel     string `json:"claim_channel"`     // sms | email; optional
	ClaimDestination string `json:"claim_destination"` // phone or email of the real provider
}

// InviteInput (re)issues a claim invitation.
type InviteInput struct {
	Channel     string `json:"channel"`
	Destination string `json:"destination"`
}

// deliveries tracks claim messages sent outside the request (secrets never enter the
// job queue, so invites are delivered directly).
var deliveries sync.WaitGroup

// WaitDeliveries blocks until in-flight claim invitations have been attempted.
func (s *Service) WaitDeliveries() { deliveries.Wait() }

func normalizeClaimDestination(channel, dest string) (string, bool) {
	switch channel {
	case auth.ChannelSMS:
		return security.NormalizeKenyanPhone(dest)
	case auth.ChannelEmail:
		return security.NormalizeEmail(dest)
	}
	return "", false
}

// CreateAssisted creates an unowned draft. Staff fill it in, then invite the provider to
// claim it. It cannot be submitted for review until claimed (the provider must accept the
// terms and prove control of the contact the invite was sent to).
func (s *Service) CreateAssisted(ctx context.Context, in AssistedInput) (*View, error) {
	p := reqctx.PrincipalFrom(ctx)
	if p == nil {
		return nil, response.ErrUnauthenticated
	}
	if !s.authz.Can(ctx, p, "providers.create") || !s.authz.Can(ctx, p, "providers.update_any") {
		return nil, response.ErrForbidden
	}
	name, errs := validateCreate(CreateInput{ProviderType: in.ProviderType, DisplayName: in.DisplayName})
	var dest string
	if in.ClaimChannel != "" || in.ClaimDestination != "" {
		d, ok := normalizeClaimDestination(in.ClaimChannel, in.ClaimDestination)
		if !ok {
			errs["claim_destination"] = "invalid"
		}
		dest = d
	}
	if len(errs) > 0 {
		return nil, response.Validation(errs)
	}
	var id uint64
	var token string
	err := database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		uid := p.UserID
		pid, pub, err := s.insertProvider(ctx, tx, in.ProviderType, name, nil, &uid)
		if err != nil {
			return err
		}
		id = pid
		if _, err := s.refresh(ctx, tx, pid); err != nil {
			return err
		}
		if dest != "" {
			if token, err = s.issueClaim(ctx, tx, pid, in.ClaimChannel, dest); err != nil {
				return err
			}
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "providers.assisted.created", EntityType: "provider", EntityID: pub.String(),
			NewValues: map[string]any{"display_name": name, "provider_type": in.ProviderType, "invite_channel": in.ClaimChannel}})
	})
	if err != nil {
		return nil, err
	}
	if token != "" {
		s.sendClaim(ctx, id, in.ClaimChannel, dest, token)
	}
	return s.viewByID(ctx, id, true)
}

// Invite (re)issues a claim invitation for an unclaimed listing; earlier invites expire.
func (s *Service) Invite(ctx context.Context, publicID string, in InviteInput) (*View, error) {
	p := reqctx.PrincipalFrom(ctx)
	if p == nil {
		return nil, response.ErrUnauthenticated
	}
	if !s.authz.Can(ctx, p, "providers.update_any") {
		return nil, response.ErrForbidden
	}
	dest, ok := normalizeClaimDestination(in.Channel, in.Destination)
	if !ok {
		return nil, response.Validation(map[string]string{"destination": "invalid"})
	}
	key := "claim-invite:" + publicID
	if ok, retry := s.limiter.Allow(key, security.Limit{Max: 5, Window: time.Hour}); !ok {
		return nil, response.RateLimited(retry)
	}
	var id uint64
	var token string
	err := database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		r, err := s.byPublic(ctx, tx, publicID, true)
		if err != nil {
			return err
		}
		if err := editable(r); err != nil {
			return err
		}
		if r.ownerUserID != nil {
			return response.Conflict(CodeClaimed, "This listing already has an owner.")
		}
		id = r.id
		if token, err = s.issueClaim(ctx, tx, r.id, in.Channel, dest); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "providers.claim.invited", EntityType: "provider", EntityID: r.publicID.String(),
			NewValues: map[string]any{"channel": in.Channel, "destination": security.MaskDestination(dest)}})
	})
	if err != nil {
		return nil, err
	}
	s.sendClaim(ctx, id, in.Channel, dest, token)
	return s.viewByID(ctx, id, true)
}

// issueClaim stores a new token hash and expires earlier unclaimed invites.
func (s *Service) issueClaim(ctx context.Context, tx *sql.Tx, pid uint64, channel, dest string) (string, error) {
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `UPDATE provider_claims SET expires_at = ? WHERE provider_id = ? AND claimed_at IS NULL AND expires_at > ?`,
		now, pid, now); err != nil {
		return "", err
	}
	token := security.RandomToken(32)
	var by any
	if p := reqctx.PrincipalFrom(ctx); p != nil {
		by = p.UserID
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO provider_claims (provider_id, channel, destination, token_hash, expires_at, created_by)
		VALUES (?, ?, ?, ?, ?, ?)`, pid, channel, dest, security.HashToken(token), now.Add(ClaimTTL), by)
	return token, err
}

// sendClaim delivers the claim link. The token travels in the URL fragment so it never
// reaches server logs or Referer headers.
func (s *Service) sendClaim(ctx context.Context, pid uint64, channel, dest, token string) {
	sender, ok := s.senders[channel]
	if !ok {
		s.log.WarnContext(ctx, "claim invite: no sender for channel", "channel", channel)
		return
	}
	r, err := s.byID(ctx, s.db, pid, false)
	if err != nil {
		return
	}
	locale := s.settings.String(ctx, settings.KeyDefaultLocale)
	link := s.siteURL(ctx) + "/provider/claim#token=" + token
	site := s.settings.String(ctx, settings.KeySiteName)
	msg := notifications.Message{To: dest,
		Subject: s.i18n.T(locale, "provider.claim.subject", "site", site),
		Body: s.i18n.T(locale, "provider.claim.body", "name", r.displayName, "site", site, "link", link,
			"days", int(ClaimTTL.Hours()/24))}
	reqID := reqctx.RequestID(ctx)
	deliveries.Add(1)
	go func() {
		defer deliveries.Done()
		c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := sender.Send(c, msg); err != nil {
			s.log.Error("claim invite delivery failed", "channel", channel, "request_id", reqID, "error", err)
		}
	}()
}

func (s *Service) siteURL(ctx context.Context) string {
	if u := strings.TrimRight(s.settings.String(ctx, settings.KeySiteURL), "/"); u != "" {
		return u
	}
	return s.baseURL
}

// ClaimPreview describes a claim token without consuming it.
type ClaimPreview struct {
	DisplayName string    `json:"display_name"`
	Channel     string    `json:"channel"`
	Destination string    `json:"destination"` // masked
	ExpiresAt   time.Time `json:"expires_at"`
}

type claimRow struct {
	id, providerID uint64
	channel, dest  string
	expiresAt      time.Time
	claimedAt      sql.NullTime
}

func (s *Service) claimByToken(ctx context.Context, ex database.Execer, token string, lock bool) (*claimRow, error) {
	token = strings.TrimSpace(token)
	if len(token) < 20 || len(token) > 100 {
		return nil, errClaimInvalid
	}
	q := `SELECT id, provider_id, channel, destination, expires_at, claimed_at FROM provider_claims WHERE token_hash = ?`
	if lock {
		q += ` FOR UPDATE`
	}
	var c claimRow
	err := ex.QueryRowContext(ctx, q, security.HashToken(token)).Scan(&c.id, &c.providerID, &c.channel, &c.dest, &c.expiresAt, &c.claimedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errClaimInvalid
	}
	if err != nil {
		return nil, err
	}
	if c.claimedAt.Valid || time.Now().After(c.expiresAt) {
		return nil, errClaimInvalid
	}
	return &c, nil
}

func (s *Service) claimLimited(ctx context.Context) error {
	key := "claim:" + reqctx.ClientIP(ctx)
	if p := reqctx.PrincipalFrom(ctx); p != nil {
		key = "claim:u" + p.PublicID
	}
	if ok, retry := s.limiter.Allow(key, security.Limit{Max: 10, Window: 15 * time.Minute}); !ok {
		return response.RateLimited(retry)
	}
	return nil
}

// PreviewClaim shows which listing a token refers to.
func (s *Service) PreviewClaim(ctx context.Context, token string) (*ClaimPreview, error) {
	if err := s.claimLimited(ctx); err != nil {
		return nil, err
	}
	c, err := s.claimByToken(ctx, s.db, token, false)
	if err != nil {
		return nil, err
	}
	r, err := s.byID(ctx, s.db, c.providerID, false)
	if err != nil || r.ownerUserID != nil || r.status == StatusArchived {
		return nil, errClaimInvalid
	}
	return &ClaimPreview{DisplayName: r.displayName, Channel: c.channel, Destination: security.MaskDestination(c.dest), ExpiresAt: c.expiresAt}, nil
}

// ClaimInput claims an assisted listing.
type ClaimInput struct {
	Token       string `json:"token"`
	AcceptTerms bool   `json:"accept_terms"`
}

// Claim transfers an assisted draft to the signed-in user. The user's OTP-verified phone or
// email must equal the invite destination, which proves they control it (§7.6).
func (s *Service) Claim(ctx context.Context, in ClaimInput) (*View, error) {
	p := reqctx.PrincipalFrom(ctx)
	if p == nil {
		return nil, response.ErrUnauthenticated
	}
	if !in.AcceptTerms {
		return nil, response.Validation(map[string]string{"accept_terms": "required"})
	}
	if err := s.claimLimited(ctx); err != nil {
		return nil, err
	}
	phone, email, err := s.auth.VerifiedDestinations(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	var id uint64
	err = database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		var lockUser uint64
		if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id = ? FOR UPDATE`, p.UserID).Scan(&lockUser); err != nil {
			return err
		}
		c, err := s.claimByToken(ctx, tx, in.Token, true)
		if err != nil {
			return err
		}
		mine := (c.channel == auth.ChannelSMS && c.dest == phone) || (c.channel == auth.ChannelEmail && c.dest == email)
		if !mine {
			return response.New(403, CodeClaimMismatch, "Sign in with the phone number or email address this invitation was sent to.")
		}
		var existing int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM providers WHERE owner_user_id = ? AND listing_status <> 'archived'`, p.UserID).Scan(&existing); err != nil {
			return err
		}
		if existing > 0 {
			return response.Conflict(CodeAlreadyHasOne, "You already have a provider profile.")
		}
		r, err := s.byID(ctx, tx, c.providerID, true)
		if err != nil {
			return err
		}
		if r.ownerUserID != nil || r.status == StatusArchived {
			return errClaimInvalid
		}
		id = r.id
		now := time.Now().UTC()
		if _, err := tx.ExecContext(ctx, `UPDATE providers SET owner_user_id = ? WHERE id = ?`, p.UserID, r.id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE provider_claims SET claimed_at = ?, claimed_by = ? WHERE id = ?`, now, p.UserID, c.id); err != nil {
			return err
		}
		if err := s.becomeOwner(ctx, tx, r.id, p.UserID); err != nil {
			return err
		}
		if err := s.acceptTerms(ctx, tx, r.id, p.UserID); err != nil {
			return err
		}
		// Contacts staff entered that match the claimant's verified channels become verified.
		if _, err := tx.ExecContext(ctx, `UPDATE provider_contacts SET verified_at = ? WHERE provider_id = ? AND verified_at IS NULL
			AND ((channel IN ('phone','whatsapp') AND value = ?) OR (channel = 'email' AND value = ?))`, now, r.id, phone, email); err != nil {
			return err
		}
		if err := s.syncContactBadges(ctx, tx, r.id); err != nil {
			return err
		}
		if _, err := s.refresh(ctx, tx, r.id); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "providers.claimed", EntityType: "provider", EntityID: r.publicID.String(),
			NewValues: map[string]any{"channel": c.channel}})
	})
	if err != nil {
		return nil, err
	}
	s.authz.Invalidate()
	return s.viewByID(ctx, id, false)
}
