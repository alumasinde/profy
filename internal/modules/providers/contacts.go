package providers

import (
	"context"
	"database/sql"
	"slices"
	"strconv"
	"time"

	"github.com/alumasinde/profy/internal/database"
	"github.com/alumasinde/profy/internal/http/response"
	"github.com/alumasinde/profy/internal/modules/audit"
	"github.com/alumasinde/profy/internal/modules/auth"
	"github.com/alumasinde/profy/internal/modules/verification"
	"github.com/alumasinde/profy/internal/security"
	"github.com/alumasinde/profy/internal/shared/reqctx"
)

// ContactInput is one channel in a replace-all update.
type ContactInput struct {
	Channel    string `json:"channel"`
	Value      string `json:"value"`
	Visibility string `json:"visibility"`
}

var visibilities = []string{VisPublic, VisReveal, VisLoggedIn, VisHidden}

// normalizeContact validates and normalizes a value for its channel.
func normalizeContact(channel, value string) (string, bool) {
	switch channel {
	case ChannelPhone, ChannelWhatsApp:
		return security.NormalizeKenyanPhone(value)
	case ChannelEmail:
		return security.NormalizeEmail(value)
	}
	return "", false
}

// syncContactBadges keeps phone/email badges equal to verified contact values (§12.1).
func (s *Service) syncContactBadges(ctx context.Context, tx *sql.Tx, pid uint64) error {
	for ch, badge := range map[string]string{ChannelPhone: verification.PhoneVerified, ChannelEmail: verification.EmailVerified} {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM provider_contacts WHERE provider_id = ? AND channel = ? AND verified_at IS NOT NULL`,
			pid, ch).Scan(&n); err != nil {
			return err
		}
		if err := s.verification.SetContactBadge(ctx, tx, pid, badge, n > 0); err != nil {
			return err
		}
	}
	return nil
}

// SetContacts replaces the contact channels. A value that equals one of the owner's
// OTP-verified login channels is verified immediately; a changed value loses verification.
func (s *Service) SetContacts(ctx context.Context, t Target, list []ContactInput) (*View, error) {
	var id uint64
	err := database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		r, err := s.resolve(ctx, tx, t, true)
		if err != nil {
			return err
		}
		if err := s.require(ctx, t, r, "providers.update_own", "providers.update_any"); err != nil {
			return err
		}
		if err := editable(r); err != nil {
			return err
		}
		id = r.id
		errs := map[string]string{}
		seen := map[string]bool{}
		clean := make([]ContactInput, 0, len(list))
		for i, c := range list {
			k := "contacts." + strconv.Itoa(i)
			if seen[c.Channel] {
				errs[k+".channel"] = "taken"
				continue
			}
			seen[c.Channel] = true
			v, ok := normalizeContact(c.Channel, c.Value)
			if !ok {
				errs[k+".value"] = "invalid"
			}
			if !slices.Contains(visibilities, c.Visibility) {
				errs[k+".visibility"] = "invalid"
			}
			clean = append(clean, ContactInput{Channel: c.Channel, Value: v, Visibility: c.Visibility})
		}
		if len(errs) > 0 {
			return response.Validation(errs)
		}
		existing, err := s.contacts(ctx, tx, r.id)
		if err != nil {
			return err
		}
		var ownerPhone, ownerEmail string
		if r.ownerUserID != nil {
			if ownerPhone, ownerEmail, err = s.auth.VerifiedDestinations(ctx, *r.ownerUserID); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM provider_contacts WHERE provider_id = ?`, r.id); err != nil {
			return err
		}
		now := time.Now().UTC()
		for _, c := range clean {
			var verified any
			for _, e := range existing {
				if e.Channel == c.Channel && e.Value == c.Value && e.VerifiedAt != nil {
					verified = *e.VerifiedAt
				}
			}
			if verified == nil && ((c.Channel != ChannelEmail && c.Value == ownerPhone) || (c.Channel == ChannelEmail && c.Value == ownerEmail)) {
				verified = now
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO provider_contacts (provider_id, channel, value, visibility, verified_at)
				VALUES (?, ?, ?, ?, ?)`, r.id, c.Channel, c.Value, c.Visibility, verified); err != nil {
				return err
			}
		}
		if err := s.syncContactBadges(ctx, tx, r.id); err != nil {
			return err
		}
		if _, err := s.refresh(ctx, tx, r.id); err != nil {
			return err
		}
		chans := []string{}
		for _, c := range clean {
			chans = append(chans, c.Channel+":"+c.Visibility)
		}
		// Contact values are personal data: the audit records channels and visibility only.
		return audit.Record(ctx, tx, audit.Entry{Action: "providers.contacts.updated", EntityType: "provider", EntityID: r.publicID.String(),
			NewValues: map[string]any{"channels": chans}})
	})
	if err != nil {
		return nil, err
	}
	return s.viewByID(ctx, id, t.Staff)
}

// StartContactVerification sends a code to the owner's phone or email contact.
func (s *Service) StartContactVerification(ctx context.Context, channel string) (*auth.IssuedChallenge, error) {
	r, err := s.own(ctx, s.db, false)
	if err != nil {
		return nil, err
	}
	if err := s.require(ctx, Target{}, r, "providers.update_own", ""); err != nil {
		return nil, err
	}
	list, err := s.contacts(ctx, s.db, r.id)
	if err != nil {
		return nil, err
	}
	otpChannel := map[string]string{ChannelPhone: auth.ChannelSMS, ChannelEmail: auth.ChannelEmail}[channel]
	for _, c := range list {
		if c.Channel == channel && otpChannel != "" {
			if c.VerifiedAt != nil {
				return nil, response.Conflict(response.CodeConflict, "This contact is already verified.")
			}
			return s.auth.StartContactVerification(ctx, otpChannel, c.Value)
		}
	}
	return nil, response.Validation(map[string]string{"channel": "invalid"})
}

// ConfirmContactVerification marks the matching contact verified and grants its badge.
func (s *Service) ConfirmContactVerification(ctx context.Context, challengeID, code string) (*View, error) {
	otpChannel, value, err := s.auth.ConfirmContactVerification(ctx, challengeID, code)
	if err != nil {
		return nil, err
	}
	channel := map[string]string{auth.ChannelSMS: ChannelPhone, auth.ChannelEmail: ChannelEmail}[otpChannel]
	var id uint64
	err = database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		r, err := s.own(ctx, tx, true)
		if err != nil {
			return err
		}
		id = r.id
		res, err := tx.ExecContext(ctx, `UPDATE provider_contacts SET verified_at = ? WHERE provider_id = ? AND channel = ? AND value = ?`,
			time.Now().UTC(), r.id, channel, value)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return response.Conflict(response.CodeConflict, "This contact changed after the code was sent.")
		}
		if err := s.syncContactBadges(ctx, tx, r.id); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "providers.contact.verified", EntityType: "provider", EntityID: r.publicID.String(),
			NewValues: map[string]any{"channel": channel}, ActorUserID: &reqctx.PrincipalFrom(ctx).UserID})
	})
	if err != nil {
		return nil, err
	}
	return s.viewByID(ctx, id, false)
}
