package providers

import (
	"context"
	"database/sql"
	"html/template"
	"strings"
	"time"

	"github.com/alumasinde/profy/internal/database"
	"github.com/alumasinde/profy/internal/http/response"
	"github.com/alumasinde/profy/internal/modules/audit"
	"github.com/alumasinde/profy/internal/modules/locations"
	"github.com/alumasinde/profy/internal/modules/media"
	"github.com/alumasinde/profy/internal/modules/verification"
	"github.com/alumasinde/profy/internal/shared/ids"
	"github.com/alumasinde/profy/internal/shared/reqctx"
)

// MinReviewsForRating is the number of approved reviews before a rating is shown (§7.4,
// open decision 8). Reviews arrive in Phase 6; until then no rating is ever displayed.
const MinReviewsForRating = 3

// CardInput asks for one provider card. ServiceID selects the offering whose price is shown
// (0 = the provider's first live offering); Serves is a coverage note such as "Westlands".
type CardInput struct {
	ProviderID uint64
	ServiceID  uint64
	Serves     string
}

// Card is the public summary shown in search results and listings (§7.4). It contains only
// public information: contact values appear only for channels whose visibility is public.
type Card struct {
	ID            string               `json:"id"`
	Slug          string               `json:"slug"`
	Path          string               `json:"path"`
	Name          string               `json:"name"`
	Type          string               `json:"provider_type"`
	Photo         string               `json:"photo_url,omitempty"`
	PhotoSrcSet   string               `json:"-"`
	ServiceName   string               `json:"service_name"`
	Price         string               `json:"price,omitempty"`
	BaseArea      string               `json:"base_area,omitempty"`
	Serves        string               `json:"serves,omitempty"`
	Badges        []verification.Badge `json:"badges"`
	Excerpt       string               `json:"excerpt,omitempty"`
	Phone         string               `json:"phone,omitempty"`
	PhoneHref     template.URL         `json:"phone_href,omitempty"`
	WhatsAppHref  template.URL         `json:"whatsapp_href,omitempty"`
	RatingAverage *float64             `json:"rating_average,omitempty"`
	RatingCount   int                  `json:"rating_count,omitempty"`
	Featured      bool                 `json:"featured,omitempty"`
}

// Cards renders cards for published providers, preserving input order. Providers that are
// not published (or no longer exist) are skipped, so callers can never surface them.
func (s *Service) Cards(ctx context.Context, locale string, in []CardInput) ([]Card, error) {
	if len(in) == 0 {
		return []Card{}, nil
	}
	idList := make([]uint64, 0, len(in))
	for _, c := range in {
		idList = append(idList, c.ProviderID)
	}
	ph, args := placeholders(idList)
	rows, err := s.db.QueryContext(ctx, `SELECT id, public_id, slug, display_name, provider_type, COALESCE(description, ''),
		base_area_id, profile_media_id, rating_average, rating_count, featured_until
		FROM providers WHERE listing_status = 'published' AND id IN (`+ph+`)`, args...)
	if err != nil {
		return nil, err
	}
	type prow struct {
		card    Card
		base    *uint64
		photo   *uint64
		ratingN int
		rating  sql.NullFloat64
	}
	found := map[uint64]*prow{}
	var areaIDs, mediaIDs []uint64
	now := time.Now()
	for rows.Next() {
		var p prow
		var id uint64
		var pub ids.UUID
		var desc string
		var base, photo sql.NullInt64
		var featured sql.NullTime
		if err := rows.Scan(&id, &pub, &p.card.Slug, &p.card.Name, &p.card.Type, &desc, &base, &photo, &p.rating, &p.ratingN, &featured); err != nil {
			rows.Close()
			return nil, err
		}
		p.card.ID = pub.String()
		p.card.Path = "/professionals/" + p.card.Slug
		p.card.Excerpt = excerpt(desc, 160)
		p.card.Featured = featured.Valid && featured.Time.After(now)
		if base.Valid {
			v := uint64(base.Int64)
			p.base = &v
			areaIDs = append(areaIDs, v)
		}
		if photo.Valid {
			v := uint64(photo.Int64)
			p.photo = &v
			mediaIDs = append(mediaIDs, v)
		}
		found[id] = &p
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	areas, err := s.locations.ByInternal(ctx, s.db, locations.Area, areaIDs)
	if err != nil {
		return nil, err
	}
	ms, err := s.media.ByInternalIDs(ctx, s.db, mediaIDs)
	if err != nil {
		return nil, err
	}
	badges, err := s.verification.Badges(ctx, s.db, idList)
	if err != nil {
		return nil, err
	}
	offers, err := s.liveOffers(ctx, idList)
	if err != nil {
		return nil, err
	}
	phones, err := s.publicContacts(ctx, idList)
	if err != nil {
		return nil, err
	}
	out := make([]Card, 0, len(in))
	for _, req := range in {
		p := found[req.ProviderID]
		if p == nil {
			continue
		}
		c := p.card
		if p.base != nil {
			if a := areas[*p.base]; a != nil {
				c.BaseArea = a.Label
			}
		}
		if p.photo != nil {
			if m := ms[*p.photo]; m != nil && m.Status == media.StatusActive {
				c.Photo, c.PhotoSrcSet = pickURL(m, 160), srcset(m)
			}
		}
		c.Badges = badges[req.ProviderID]
		if c.Badges == nil {
			c.Badges = []verification.Badge{}
		}
		var chosen *Offered
		for i, o := range offers[req.ProviderID] {
			if req.ServiceID == 0 || o.serviceInternal == req.ServiceID {
				chosen = &offers[req.ProviderID][i]
				break
			}
		}
		if chosen != nil {
			c.ServiceName = chosen.ServiceName
			if chosen.CustomTitle != nil {
				c.ServiceName = *chosen.CustomTitle
			}
			c.Price = s.priceLabel(locale, *chosen)
		}
		if req.Serves != "" && !strings.HasPrefix(c.BaseArea, req.Serves) {
			c.Serves = req.Serves
		}
		for _, ct := range phones[req.ProviderID] {
			switch ct.Channel {
			case ChannelPhone:
				c.Phone, c.PhoneHref = displayValue(ct), template.URL(s.contactHref(locale, ct, c.Name))
			case ChannelWhatsApp:
				c.WhatsAppHref = template.URL(s.contactHref(locale, ct, c.Name))
			}
		}
		if p.ratingN >= MinReviewsForRating && p.rating.Valid {
			v := p.rating.Float64
			c.RatingAverage, c.RatingCount = &v, p.ratingN
		}
		out = append(out, c)
	}
	return out, nil
}

// liveOffers returns active offerings of published services per provider.
func (s *Service) liveOffers(ctx context.Context, idList []uint64) (map[uint64][]Offered, error) {
	all, err := s.offeredMany(ctx, s.db, idList)
	if err != nil {
		return nil, err
	}
	out := map[uint64][]Offered{}
	for id, list := range all {
		for _, o := range list {
			if o.Status == "active" && o.ServiceLive {
				out[id] = append(out[id], o)
			}
		}
	}
	return out, nil
}

// publicContacts returns contacts whose visibility is public (shown on cards).
func (s *Service) publicContacts(ctx context.Context, idList []uint64) (map[uint64][]Contact, error) {
	ph, args := placeholders(idList)
	rows, err := s.db.QueryContext(ctx, `SELECT provider_id, channel, value FROM provider_contacts
		WHERE visibility = 'public' AND channel IN ('phone', 'whatsapp') AND provider_id IN (`+ph+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uint64][]Contact{}
	for rows.Next() {
		var id uint64
		var c Contact
		if err := rows.Scan(&id, &c.Channel, &c.Value); err != nil {
			return nil, err
		}
		c.Visibility = VisPublic
		out[id] = append(out[id], c)
	}
	return out, rows.Err()
}

func placeholders(idList []uint64) (string, []any) {
	args := make([]any, len(idList))
	for i, id := range idList {
		args[i] = id
	}
	return strings.TrimSuffix(strings.Repeat("?,", len(idList)), ","), args
}

func excerpt(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		cut := string(r[:n])
		if i := strings.LastIndex(cut, " "); i > n/2 {
			cut = cut[:i]
		}
		return strings.TrimRight(cut, ",.;: ") + "…"
	}
	return s
}

// ---- Featured curation (§7.3) ---------------------------------------------------------

// MaxFeaturedDays bounds one featuring period.
const MaxFeaturedDays = 90

// SetFeatured features a published listing for a number of days, or removes it (days = 0).
// Featuring is an explicit, audited staff decision; it never changes search ranking.
func (s *Service) SetFeatured(ctx context.Context, publicID string, days int) (*View, error) {
	p := reqctx.PrincipalFrom(ctx)
	if p == nil {
		return nil, response.ErrUnauthenticated
	}
	perm := "providers.assign_featured"
	if days == 0 {
		perm = "providers.remove_featured"
	}
	if !s.authz.Can(ctx, p, perm) {
		return nil, response.ErrForbidden
	}
	if days < 0 || days > MaxFeaturedDays {
		return nil, response.Validation(map[string]string{"days": "out_of_range"})
	}
	var id uint64
	err := database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		r, err := s.byPublic(ctx, tx, publicID, true)
		if err != nil {
			return err
		}
		id = r.id
		if days > 0 && r.status != StatusPublished {
			return response.Conflict(CodeInvalidAction, "Only published listings can be featured.")
		}
		now := time.Now().UTC()
		if days == 0 {
			_, err = tx.ExecContext(ctx, `UPDATE providers SET featured_at = NULL, featured_until = NULL, featured_by = NULL WHERE id = ?`, r.id)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE providers SET featured_at = ?, featured_until = ?, featured_by = ? WHERE id = ?`,
				now, now.AddDate(0, 0, days), p.UserID, r.id)
		}
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "providers.featured.updated", EntityType: "provider", EntityID: r.publicID.String(),
			NewValues: map[string]any{"days": days}})
	})
	if err != nil {
		return nil, err
	}
	return s.viewByID(ctx, id, true)
}
