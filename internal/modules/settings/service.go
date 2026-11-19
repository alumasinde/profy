// Package settings provides typed, cached site settings and feature flags (plan §4).
package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Known setting keys (plan §4.1). Values are validated by Phase 3 admin endpoints.
const (
	KeySiteName        = "site_name"
	KeySiteShortName   = "site_short_name"
	KeySiteTagline     = "site_tagline"
	KeySiteDescription = "site_description"
	KeySiteURL         = "site_url"
	KeyPrimaryColor    = "primary_color"
	KeySecondaryColor  = "secondary_color"
	KeyAccentColor     = "accent_color"
	KeyDefaultLocale   = "default_locale"
	KeyTermsVersion    = "terms_version"
	KeyPrivacyVersion  = "privacy_version"
	KeyOTPChannels     = "otp_channels"
	KeyMaintenance     = "maintenance_mode"

	KeyProviderTermsVersion     = "provider_terms_version"
	KeyListingEditModeration    = "listing_edit_moderation"
	KeyPortfolioModeration      = "portfolio_moderation"
	KeyDefaultContactVisibility = "default_contact_visibility"
	KeyPortfolioMaxItems        = "portfolio_max_items"
	KeyUploadMaxMB              = "upload_max_mb"

	// Discovery (Phase 5).
	KeyLandingMinProviders  = "landing_min_providers"
	KeyLandingHysteresisDay = "landing_hysteresis_days"
	KeyRankService          = "rank_weight_service"
	KeyRankLocation         = "rank_weight_location"
	KeyRankCompleteness     = "rank_weight_completeness"
	KeyRankRating           = "rank_weight_rating"
	KeyRankActivity         = "rank_weight_activity"
	KeyRankExploration      = "rank_exploration_points"
)

// defaults are safe bootstrap values used when the database has no row (§4.2).
var defaults = map[string]any{
	KeySiteName:        "HudumaLink",
	KeySiteShortName:   "HudumaLink",
	KeySiteTagline:     "",
	KeySiteDescription: "",
	KeySiteURL:         "",
	KeyPrimaryColor:    "#0f766e",
	KeySecondaryColor:  "#1e293b",
	KeyAccentColor:     "#b45309",
	KeyDefaultLocale:   "en",
	KeyTermsVersion:    "",
	KeyPrivacyVersion:  "",
	KeyOTPChannels:     []any{"sms", "email"},
	KeyMaintenance:     false,

	KeyProviderTermsVersion:     "",
	KeyListingEditModeration:    "post",
	KeyPortfolioModeration:      "post",
	KeyDefaultContactVisibility: "reveal_on_click",
	KeyPortfolioMaxItems:        30,
	KeyUploadMaxMB:              8,

	KeyLandingMinProviders:  3,
	KeyLandingHysteresisDay: 14,
	KeyRankService:          35,
	KeyRankLocation:         30,
	KeyRankCompleteness:     15,
	KeyRankRating:           10,
	KeyRankActivity:         10,
	KeyRankExploration:      5,
}

type snapshot struct {
	values   map[string]json.RawMessage
	public   map[string]bool
	flags    map[string]bool
	loadedAt time.Time
}

// Service caches settings in memory with explicit invalidation (§4.2).
type Service struct {
	db  *sql.DB
	ttl time.Duration

	mu   sync.RWMutex
	snap *snapshot
}

// New creates the service. ttl bounds staleness when another instance changed settings.
func New(db *sql.DB, ttl time.Duration) *Service { return &Service{db: db, ttl: ttl} }

// Invalidate drops the cache; call after any administrative change.
func (s *Service) Invalidate() {
	s.mu.Lock()
	s.snap = nil
	s.mu.Unlock()
}

func (s *Service) load(ctx context.Context) (*snapshot, error) {
	s.mu.RLock()
	snap := s.snap
	s.mu.RUnlock()
	if snap != nil && time.Since(snap.loadedAt) < s.ttl {
		return snap, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snap != nil && time.Since(s.snap.loadedAt) < s.ttl {
		return s.snap, nil
	}
	n := &snapshot{values: map[string]json.RawMessage{}, public: map[string]bool{}, flags: map[string]bool{}, loadedAt: time.Now()}
	rows, err := s.db.QueryContext(ctx, `SELECT setting_key, value_json, is_public FROM site_settings`)
	if err != nil {
		return nil, fmt.Errorf("load settings: %w", err)
	}
	for rows.Next() {
		var k string
		var v []byte
		var pub bool
		if err := rows.Scan(&k, &v, &pub); err != nil {
			rows.Close()
			return nil, err
		}
		n.values[k] = v
		n.public[k] = pub
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	frows, err := s.db.QueryContext(ctx, `SELECT flag_key, is_enabled FROM feature_flags`)
	if err != nil {
		return nil, fmt.Errorf("load feature flags: %w", err)
	}
	defer frows.Close()
	for frows.Next() {
		var k string
		var on bool
		if err := frows.Scan(&k, &on); err != nil {
			return nil, err
		}
		n.flags[k] = on
	}
	if err := frows.Err(); err != nil {
		return nil, err
	}
	s.snap = n
	return n, nil
}

// String returns a string setting, falling back to the default on error or absence.
func (s *Service) String(ctx context.Context, key string) string {
	if snap, err := s.load(ctx); err == nil {
		if raw, ok := snap.values[key]; ok {
			var v string
			if json.Unmarshal(raw, &v) == nil {
				return v
			}
		}
	}
	v, _ := defaults[key].(string)
	return v
}

// Bool returns a boolean setting.
func (s *Service) Bool(ctx context.Context, key string) bool {
	if snap, err := s.load(ctx); err == nil {
		if raw, ok := snap.values[key]; ok {
			var v bool
			if json.Unmarshal(raw, &v) == nil {
				return v
			}
		}
	}
	v, _ := defaults[key].(bool)
	return v
}

// Int returns an integer setting.
func (s *Service) Int(ctx context.Context, key string) int {
	if snap, err := s.load(ctx); err == nil {
		if raw, ok := snap.values[key]; ok {
			var v int
			if json.Unmarshal(raw, &v) == nil {
				return v
			}
		}
	}
	v, _ := defaults[key].(int)
	return v
}

// Strings returns a string-list setting.
func (s *Service) Strings(ctx context.Context, key string) []string {
	if snap, err := s.load(ctx); err == nil {
		if raw, ok := snap.values[key]; ok {
			var v []string
			if json.Unmarshal(raw, &v) == nil {
				return v
			}
		}
	}
	var out []string
	if d, ok := defaults[key].([]any); ok {
		for _, x := range d {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// Flag reports whether a feature flag is enabled. Unknown flags are off (fail closed).
func (s *Service) Flag(ctx context.Context, key string) bool {
	snap, err := s.load(ctx)
	if err != nil {
		return false
	}
	return snap.flags[key]
}

// Public returns settings marked public plus public feature flags, safe for the frontend.
func (s *Service) Public(ctx context.Context) (map[string]json.RawMessage, map[string]bool, error) {
	snap, err := s.load(ctx)
	if err != nil {
		return nil, nil, err
	}
	out := map[string]json.RawMessage{}
	for k, v := range snap.values {
		if snap.public[k] {
			out[k] = v
		}
	}
	flags := map[string]bool{}
	for k, v := range snap.flags {
		flags[k] = v
	}
	return out, flags, nil
}
