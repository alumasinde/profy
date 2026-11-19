package providers

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/alumasinde/profy/internal/database"
	"github.com/alumasinde/profy/internal/http/response"
	"github.com/alumasinde/profy/internal/modules/audit"
	"github.com/alumasinde/profy/internal/shared/ids"
	"github.com/alumasinde/profy/internal/shared/text"
)

// OfferInput adds or edits an offered service.
type OfferInput struct {
	ServiceID   *string `json:"service_id"` // create only
	CustomTitle *string `json:"custom_title"`
	Description *string `json:"description"`
	PricingType *string `json:"pricing_type"`
	PriceFrom   *int    `json:"price_from"`
	PriceTo     *int    `json:"price_to"`
	Status      *string `json:"status"` // active | hidden
}

// validatePricing enforces the pricing-type rules (whole KES).
func validatePricing(pt string, from, to *int, errs map[string]string) {
	inRange := func(p *int) bool { return p != nil && *p >= 0 && *p <= MaxPrice }
	switch pt {
	case PriceFixed, PriceFrom:
		if !inRange(from) {
			errs["price_from"] = "required"
		}
		if to != nil {
			errs["price_to"] = "invalid"
		}
	case PriceRange:
		if !inRange(from) {
			errs["price_from"] = "required"
		}
		if !inRange(to) {
			errs["price_to"] = "required"
		} else if from != nil && *to <= *from {
			errs["price_to"] = "invalid"
		}
	case PriceOnQuote:
		if from != nil || to != nil {
			errs["price_from"] = "invalid"
		}
	default:
		errs["pricing_type"] = "invalid"
	}
}

func cleanOptional(v *string, max int, field string, errs map[string]string) any {
	if v == nil {
		return nil
	}
	c, ok := text.CleanText(*v, max)
	if !ok {
		errs[field] = "too_long"
	}
	if c == "" {
		return nil
	}
	return c
}

// AddService adds a published catalogue service to the provider (re-enters moderation on
// published listings under pre-moderation).
func (s *Service) AddService(ctx context.Context, t Target, in OfferInput) (*View, error) {
	var id uint64
	err := database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		r, err := s.resolve(ctx, tx, t, true)
		if err != nil {
			return err
		}
		if err := s.require(ctx, t, r, "provider_services.create_own", "provider_services.manage_any"); err != nil {
			return err
		}
		if err := editable(r); err != nil {
			return err
		}
		id = r.id
		errs := map[string]string{}
		if in.ServiceID == nil {
			return response.Validation(map[string]string{"service_id": "required"})
		}
		ref, err := s.services.Resolve(ctx, tx, *in.ServiceID)
		if err != nil || !ref.Live() {
			return response.Validation(map[string]string{"service_id": "invalid"})
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM provider_services WHERE provider_id = ?`, r.id).Scan(&count); err != nil {
			return err
		}
		if count >= MaxServices {
			return response.Validation(map[string]string{"service_id": "too_many"})
		}
		pt := PriceOnQuote
		if in.PricingType != nil {
			pt = *in.PricingType
		}
		validatePricing(pt, in.PriceFrom, in.PriceTo, errs)
		title := cleanOptional(in.CustomTitle, 120, "custom_title", errs)
		desc := cleanOptional(in.Description, 1000, "description", errs)
		if len(errs) > 0 {
			return response.Validation(errs)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO provider_services (public_id, provider_id, service_id, custom_title, description,
			pricing_type, price_from, price_to) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			ids.New(), r.id, ref.InternalID, title, desc, pt, in.PriceFrom, in.PriceTo)
		if database.IsDuplicate(err) {
			return response.Validation(map[string]string{"service_id": "taken"})
		}
		if err != nil {
			return err
		}
		if err := s.materialChange(ctx, tx, r, "services"); err != nil {
			return err
		}
		if _, err := s.refresh(ctx, tx, r.id); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "providers.service.added", EntityType: "provider", EntityID: r.publicID.String(),
			NewValues: map[string]any{"service": ref.Name, "pricing_type": pt}})
	})
	if err != nil {
		return nil, err
	}
	return s.viewByID(ctx, id, t.Staff)
}

func (s *Service) offerRow(ctx context.Context, tx *sql.Tx, pid uint64, offerPublic string) (uint64, string, *int, *int, error) {
	pub, err := ids.Parse(offerPublic)
	if err != nil {
		return 0, "", nil, nil, response.ErrNotFound
	}
	var id uint64
	var pt string
	var pf, pto sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT id, pricing_type, price_from, price_to FROM provider_services WHERE public_id = ? AND provider_id = ? FOR UPDATE`,
		pub, pid).Scan(&id, &pt, &pf, &pto)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", nil, nil, response.ErrNotFound
	}
	var f, to *int
	if pf.Valid {
		v := int(pf.Int64)
		f = &v
	}
	if pto.Valid {
		v := int(pto.Int64)
		to = &v
	}
	return id, pt, f, to, err
}

// UpdateService edits an offered service's title, description, pricing or visibility.
func (s *Service) UpdateService(ctx context.Context, t Target, offerID string, in OfferInput) (*View, error) {
	var id uint64
	err := database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		r, err := s.resolve(ctx, tx, t, true)
		if err != nil {
			return err
		}
		if err := s.require(ctx, t, r, "provider_services.update_own", "provider_services.manage_any"); err != nil {
			return err
		}
		if err := editable(r); err != nil {
			return err
		}
		id = r.id
		oid, pt, pf, pto, err := s.offerRow(ctx, tx, r.id, offerID)
		if err != nil {
			return err
		}
		errs := map[string]string{}
		if in.ServiceID != nil {
			errs["service_id"] = "read_only"
		}
		if in.PricingType != nil {
			pt, pf, pto = *in.PricingType, in.PriceFrom, in.PriceTo
		} else if in.PriceFrom != nil || in.PriceTo != nil {
			pf, pto = in.PriceFrom, in.PriceTo
		}
		validatePricing(pt, pf, pto, errs)
		sets, args := []string{"pricing_type = ?", "price_from = ?", "price_to = ?"}, []any{pt, pf, pto}
		if in.CustomTitle != nil {
			sets, args = append(sets, "custom_title = ?"), append(args, cleanOptional(in.CustomTitle, 120, "custom_title", errs))
		}
		if in.Description != nil {
			sets, args = append(sets, "description = ?"), append(args, cleanOptional(in.Description, 1000, "description", errs))
		}
		if in.Status != nil {
			if *in.Status != "active" && *in.Status != "hidden" {
				errs["status"] = "invalid"
			}
			sets, args = append(sets, "status = ?"), append(args, *in.Status)
		}
		if len(errs) > 0 {
			return response.Validation(errs)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE provider_services SET `+strings.Join(sets, ", ")+` WHERE id = ?`, append(args, oid)...); err != nil {
			return err
		}
		if _, err := s.refresh(ctx, tx, r.id); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "providers.service.updated", EntityType: "provider", EntityID: r.publicID.String(),
			NewValues: map[string]any{"offer": offerID, "pricing_type": pt}})
	})
	if err != nil {
		return nil, err
	}
	return s.viewByID(ctx, id, t.Staff)
}

// RemoveService deletes an offered service.
func (s *Service) RemoveService(ctx context.Context, t Target, offerID string) (*View, error) {
	var id uint64
	err := database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		r, err := s.resolve(ctx, tx, t, true)
		if err != nil {
			return err
		}
		if err := s.require(ctx, t, r, "provider_services.delete_own", "provider_services.manage_any"); err != nil {
			return err
		}
		if err := editable(r); err != nil {
			return err
		}
		id = r.id
		oid, _, _, _, err := s.offerRow(ctx, tx, r.id, offerID)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM provider_services WHERE id = ?`, oid); err != nil {
			return err
		}
		if _, err := s.refresh(ctx, tx, r.id); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "providers.service.removed", EntityType: "provider", EntityID: r.publicID.String(),
			OldValues: map[string]any{"offer": offerID}})
	})
	if err != nil {
		return nil, err
	}
	return s.viewByID(ctx, id, t.Staff)
}
