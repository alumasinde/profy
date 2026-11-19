package providers

import (
	"context"
	"database/sql"
	"strconv"

	"github.com/alumasinde/profy/internal/database"
	"github.com/alumasinde/profy/internal/http/response"
	"github.com/alumasinde/profy/internal/modules/audit"
	"github.com/alumasinde/profy/internal/modules/locations"
)

// CoverageRef identifies a coverage location.
type CoverageRef struct {
	Type string `json:"type"` // area | town | county
	ID   string `json:"id"`
}

// PinInput is the provider's private map position.
type PinInput struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	AccuracyM *int    `json:"accuracy_m"`
}

// AreasInput replaces the full location set (§15.5 PUT /provider/service-areas).
type AreasInput struct {
	BaseAreaID      *string       `json:"base_area_id"`
	ServiceRadiusKm *int          `json:"service_radius_km"`
	Coverage        []CoverageRef `json:"coverage"`
	Pin             *PinInput     `json:"pin"` // null clears the pin
}

// SetAreas replaces base location, coverage, radius and pin. Locations must be active and
// selectable. The pin is private and validated inside Kenya; points are written with
// ST_SRID(POINT(lng, lat), 4326) (§10.3).
func (s *Service) SetAreas(ctx context.Context, t Target, in AreasInput) (*View, error) {
	var id uint64
	err := database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		r, err := s.resolve(ctx, tx, t, true)
		if err != nil {
			return err
		}
		if err := s.require(ctx, t, r, "provider_areas.manage_own", "provider_areas.manage_any"); err != nil {
			return err
		}
		if err := editable(r); err != nil {
			return err
		}
		id = r.id
		errs := map[string]string{}
		var base *uint64
		if in.BaseAreaID != nil && *in.BaseAreaID != "" {
			ref, err := s.locations.Resolve(ctx, tx, locations.Area, *in.BaseAreaID)
			if err != nil || ref.Status != "active" || !ref.Selectable {
				errs["base_area_id"] = "invalid"
			} else {
				base = &ref.InternalID
			}
		}
		if in.ServiceRadiusKm != nil && (*in.ServiceRadiusKm < 1 || *in.ServiceRadiusKm > MaxRadiusKm) {
			errs["service_radius_km"] = "out_of_range"
		}
		if len(in.Coverage) > MaxCoverage {
			errs["coverage"] = "too_many"
		}
		type cov struct {
			t  string
			id uint64
		}
		var resolved []cov
		seen := map[cov]bool{}
		for i, c := range in.Coverage {
			ref, err := s.locations.Resolve(ctx, tx, c.Type, c.ID)
			if err != nil || ref.Status != "active" || !ref.Selectable {
				errs["coverage."+strconv.Itoa(i)] = "invalid"
				continue
			}
			k := cov{c.Type, ref.InternalID}
			if !seen[k] {
				seen[k] = true
				resolved = append(resolved, k)
			}
		}
		if in.Pin != nil {
			for k, v := range locations.ValidatePoint(locations.Point{Latitude: in.Pin.Latitude, Longitude: in.Pin.Longitude}) {
				errs["pin."+k] = v
			}
			if in.Pin.AccuracyM != nil && (*in.Pin.AccuracyM < 0 || *in.Pin.AccuracyM > 100000) {
				errs["pin.accuracy_m"] = "out_of_range"
			}
		}
		if len(errs) > 0 {
			return response.Validation(errs)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE providers SET base_area_id = ?, service_radius_km = ? WHERE id = ?`, base, in.ServiceRadiusKm, r.id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM provider_service_areas WHERE provider_id = ?`, r.id); err != nil {
			return err
		}
		for _, c := range resolved {
			if _, err := tx.ExecContext(ctx, `INSERT INTO provider_service_areas (provider_id, location_type, location_id) VALUES (?, ?, ?)`,
				r.id, c.t, c.id); err != nil {
				return err
			}
		}
		if in.Pin == nil {
			if _, err := tx.ExecContext(ctx, `DELETE FROM provider_geo WHERE provider_id = ?`, r.id); err != nil {
				return err
			}
		} else {
			source := "provider_pin"
			if t.Staff {
				source = "admin"
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO provider_geo (provider_id, location, accuracy_m, source)
				VALUES (?, ST_SRID(POINT(?, ?), 4326), ?, ?) AS new
				ON DUPLICATE KEY UPDATE location = new.location, accuracy_m = new.accuracy_m, source = new.source`,
				r.id, in.Pin.Longitude, in.Pin.Latitude, in.Pin.AccuracyM, source); err != nil {
				return err
			}
		}
		if _, err := s.refresh(ctx, tx, r.id); err != nil {
			return err
		}
		// Coordinates are personal location data: the audit records that they changed, not where.
		return audit.Record(ctx, tx, audit.Entry{Action: "providers.areas.updated", EntityType: "provider", EntityID: r.publicID.String(),
			NewValues: map[string]any{"coverage_count": len(resolved), "radius_km": in.ServiceRadiusKm, "pin_set": in.Pin != nil}})
	})
	if err != nil {
		return nil, err
	}
	return s.viewByID(ctx, id, t.Staff)
}

// onLocationMerge re-points base areas and coverage when towns or areas are merged.
func (s *Service) onLocationMerge(ctx context.Context, tx *sql.Tx, typ string, src, dst uint64) error {
	if typ == locations.Area {
		if _, err := tx.ExecContext(ctx, `UPDATE providers SET base_area_id = ? WHERE base_area_id = ?`, dst, src); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT IGNORE INTO provider_service_areas (provider_id, location_type, location_id, created_at)
		SELECT provider_id, location_type, ?, created_at FROM provider_service_areas WHERE location_type = ? AND location_id = ?`, dst, typ, src); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM provider_service_areas WHERE location_type = ? AND location_id = ?`, typ, src)
	return err
}
