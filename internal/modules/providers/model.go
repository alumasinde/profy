// Package providers owns provider listings: profiles, onboarding and completeness, offered
// services and pricing, base location and coverage, contacts and visibility, portfolio,
// the listing lifecycle and moderation, assisted onboarding, and the public profile page
// (plan §2.1, §7.4–7.6, §10.2, §12.2, §12.5, §13.2).
package providers

import (
	"database/sql"
	"errors"
	"time"

	"github.com/alumasinde/profy/internal/http/response"
	"github.com/alumasinde/profy/internal/shared/ids"
)

// Provider types.
const (
	TypeIndividual = "individual"
	TypeBusiness   = "business"
)

// Listing statuses (§12.2).
const (
	StatusDraft     = "draft"
	StatusPending   = "pending_review"
	StatusPublished = "published"
	StatusPaused    = "paused"
	StatusSuspended = "suspended"
	StatusArchived  = "archived"
)

// Contact channels and visibility (§12.5).
const (
	ChannelPhone    = "phone"
	ChannelWhatsApp = "whatsapp"
	ChannelEmail    = "email"

	VisPublic   = "public"
	VisReveal   = "reveal_on_click"
	VisLoggedIn = "logged_in_only"
	VisHidden   = "hidden"
)

// Pricing types.
const (
	PriceFixed   = "fixed"
	PriceFrom    = "from"
	PriceRange   = "range"
	PriceOnQuote = "on_quote"
)

// Code-defined limits (§8.2).
const (
	MaxServices     = 30
	MaxCoverage     = 50
	MaxRadiusKm     = 50
	MaxPrice        = 10_000_000 // KES
	MaxDescription  = 3000
	MinGoodDesc     = 80 // characters for the completeness item
	ClaimTTL        = 14 * 24 * time.Hour
	MaxYearsExpInfo = 70
)

// row is the providers table row.
type row struct {
	id              uint64
	publicID        ids.UUID
	ownerUserID     *uint64
	providerType    string
	displayName     string
	slug            string
	description     string
	yearsExperience *int
	baseAreaID      *uint64
	radiusKm        *int
	profileMediaID  *uint64
	coverMediaID    *uint64
	status          string
	completeness    int
	termsVersion    *string
	termsAcceptedAt *time.Time
	statusReason    *string
	metaTitle       *string
	metaDescription *string
	createdBy       *uint64
	submittedAt     *time.Time
	publishedAt     *time.Time
	createdAt       time.Time
	updatedAt       time.Time
}

const rowCols = `id, public_id, owner_user_id, provider_type, display_name, slug, COALESCE(description, ''), years_experience,
	base_area_id, service_radius_km, profile_media_id, cover_media_id, listing_status, completeness_score, terms_version,
	terms_accepted_at, status_reason, meta_title, meta_description, created_by, submitted_at, published_at, created_at, updated_at`

func scanRow(sc interface{ Scan(...any) error }) (*row, error) {
	var r row
	var owner, base, prof, cover, createdBy sql.NullInt64
	var years, radius sql.NullInt64
	var tv, reason, mt, md sql.NullString
	var ta, sa, pa sql.NullTime
	err := sc.Scan(&r.id, &r.publicID, &owner, &r.providerType, &r.displayName, &r.slug, &r.description, &years, &base, &radius,
		&prof, &cover, &r.status, &r.completeness, &tv, &ta, &reason, &mt, &md, &createdBy, &sa, &pa, &r.createdAt, &r.updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, response.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u64 := func(n sql.NullInt64) *uint64 {
		if !n.Valid {
			return nil
		}
		v := uint64(n.Int64)
		return &v
	}
	i := func(n sql.NullInt64) *int {
		if !n.Valid {
			return nil
		}
		v := int(n.Int64)
		return &v
	}
	s := func(n sql.NullString) *string {
		if !n.Valid {
			return nil
		}
		return &n.String
	}
	t := func(n sql.NullTime) *time.Time {
		if !n.Valid {
			return nil
		}
		return &n.Time
	}
	r.ownerUserID, r.baseAreaID, r.profileMediaID, r.coverMediaID, r.createdBy = u64(owner), u64(base), u64(prof), u64(cover), u64(createdBy)
	r.yearsExperience, r.radiusKm = i(years), i(radius)
	r.termsVersion, r.statusReason, r.metaTitle, r.metaDescription = s(tv), s(reason), s(mt), s(md)
	r.termsAcceptedAt, r.submittedAt, r.publishedAt = t(ta), t(sa), t(pa)
	return &r, nil
}

// transition describes one allowed lifecycle move (§12.2: code-defined state machine).
type transition struct {
	from         []string
	to           string
	ownerAllowed string // permission an owner needs ("" = owners may not)
	staffPerm    string // permission staff need ("" = staff may not)
	needsReason  bool
}

// Transitions are the allowed actions. Owners act on their own listing only; staff hold
// *_any / moderation permissions.
var Transitions = map[string]transition{
	"submit":  {from: []string{StatusDraft}, to: StatusPending, ownerAllowed: "providers.publish_own", staffPerm: "providers.update_any"},
	"approve": {from: []string{StatusPending}, to: StatusPublished, staffPerm: "providers.publish_any"},
	"reject":  {from: []string{StatusPending}, to: StatusDraft, staffPerm: "providers.publish_any", needsReason: true},
	"pause":   {from: []string{StatusPublished}, to: StatusPaused, ownerAllowed: "providers.unpublish_own", staffPerm: "providers.publish_any"},
	"resume":  {from: []string{StatusPaused}, to: StatusPublished, ownerAllowed: "providers.publish_own", staffPerm: "providers.publish_any"},
	"suspend": {from: []string{StatusPending, StatusPublished, StatusPaused}, to: StatusSuspended, staffPerm: "providers.suspend", needsReason: true},
	"restore": {from: []string{StatusSuspended}, to: StatusPublished, staffPerm: "providers.restore"},
	"archive": {from: []string{StatusDraft, StatusPending, StatusPublished, StatusPaused, StatusSuspended}, to: StatusArchived,
		ownerAllowed: "providers.unpublish_own", staffPerm: "providers.update_any"},
}

// Notices sent to the owner per action.
var actionNotice = map[string]string{
	"approve": "listing_approved", "reject": "listing_rejected", "suspend": "listing_suspended", "restore": "listing_restored",
}
