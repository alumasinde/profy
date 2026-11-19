// Package analytics records marketplace usage events (plan §16.3). Events never contain
// OTPs, PINs, tokens, contact values or message bodies; properties are allow-listed.
package analytics

import (
	"context"
	"crypto/sha256"
	"encoding/json"

	"github.com/alumasinde/profy/internal/database"
	"github.com/alumasinde/profy/internal/shared/reqctx"
)

// Event types.
const (
	ProviderProfileView   = "provider_profile_view"
	ProviderPhoneReveal   = "provider_phone_reveal"
	ProviderWhatsAppClick = "provider_whatsapp_click"
	ProviderEmailReveal   = "provider_email_reveal"
)

var allowedProps = map[string]bool{"channel": true, "source": true}

// Record stores one event. The session hash is a truncated SHA-256 of the client IP and
// user agent: enough to de-duplicate, not enough to identify.
func Record(ctx context.Context, ex database.Execer, eventType string, providerID uint64, props map[string]string) error {
	clean := map[string]string{}
	for k, v := range props {
		if allowedProps[k] && len(v) <= 40 {
			clean[k] = v
		}
	}
	var propsJSON any
	if len(clean) > 0 {
		b, _ := json.Marshal(clean)
		propsJSON = string(b)
	}
	var user any
	if p := reqctx.PrincipalFrom(ctx); p != nil {
		user = p.UserID
	}
	h := sha256.Sum256([]byte(reqctx.ClientIP(ctx) + "|" + reqctx.UserAgent(ctx)))
	var prov any
	if providerID > 0 {
		prov = providerID
	}
	_, err := ex.ExecContext(ctx, `INSERT INTO analytics_events (event_type, provider_id, user_id, session_hash, properties)
		VALUES (?, ?, ?, ?, ?)`, eventType, prov, user, h[:16], propsJSON)
	return err
}
