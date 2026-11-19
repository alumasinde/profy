package providers

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/alumasinde/profy/internal/http/response"
	"github.com/alumasinde/profy/internal/jobsqueue"
	"github.com/alumasinde/profy/internal/modules/auth"
	"github.com/alumasinde/profy/internal/modules/notifications"
	"github.com/alumasinde/profy/internal/modules/settings"
)

var noticeEvents = map[string]bool{
	"listing_approved": true, "listing_rejected": true, "listing_suspended": true, "listing_restored": true,
	"verification_approved": true, "verification_rejected": true,
}

// RegisterJobs registers the owner-notification handler. Messages go to the owner's
// OTP-verified phone (SMS) or, failing that, verified email; unowned listings are skipped.
func (s *Service) RegisterJobs(q *jobsqueue.Queue) {
	q.Handle(notifications.JobProviderNotify, func(ctx context.Context, payload json.RawMessage) error {
		var n notifications.ProviderNotice
		if err := json.Unmarshal(payload, &n); err != nil {
			return nil // malformed payloads are not retried
		}
		if !noticeEvents[n.Event] {
			return nil
		}
		return s.notifyOwner(ctx, n)
	})
}

func (s *Service) notifyOwner(ctx context.Context, n notifications.ProviderNotice) error {
	r, err := s.byID(ctx, s.db, n.ProviderID, false)
	if errors.Is(err, response.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if r.ownerUserID == nil {
		return nil
	}
	u, err := s.users.ByID(ctx, s.db, *r.ownerUserID)
	if err != nil {
		return err
	}
	phone, email, err := s.auth.VerifiedDestinations(ctx, u.ID)
	if err != nil {
		return err
	}
	locale := u.PreferredLocale
	if !s.i18n.Has(locale) {
		locale = s.settings.String(ctx, settings.KeyDefaultLocale)
	}
	site := s.settings.String(ctx, settings.KeySiteName)
	reason := strings.TrimSpace(n.Reason)
	if reason == "" {
		reason = "-"
	}
	body := s.i18n.T(locale, "provider.notice."+n.Event, "name", r.displayName, "detail", n.Detail, "reason", reason,
		"site", site, "link", s.siteURL(ctx)+"/provider")
	channel, to := auth.ChannelSMS, phone
	if to == "" || s.senders[channel] == nil {
		channel, to = auth.ChannelEmail, email
	}
	sender := s.senders[channel]
	if to == "" || sender == nil {
		s.log.WarnContext(ctx, "provider notice: no verified destination", "event", n.Event)
		return nil
	}
	return sender.Send(ctx, notifications.Message{To: to, Subject: s.i18n.T(locale, "provider.notice.subject", "site", site), Body: body})
}
