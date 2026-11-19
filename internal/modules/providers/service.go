package providers

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/alumasinde/profy/internal/authz"
	"github.com/alumasinde/profy/internal/database"
	"github.com/alumasinde/profy/internal/http/response"
	"github.com/alumasinde/profy/internal/i18n"
	"github.com/alumasinde/profy/internal/jobsqueue"
	"github.com/alumasinde/profy/internal/modules/audit"
	"github.com/alumasinde/profy/internal/modules/auth"
	"github.com/alumasinde/profy/internal/modules/locations"
	"github.com/alumasinde/profy/internal/modules/media"
	"github.com/alumasinde/profy/internal/modules/moderation"
	"github.com/alumasinde/profy/internal/modules/notifications"
	"github.com/alumasinde/profy/internal/modules/seo"
	"github.com/alumasinde/profy/internal/modules/services"
	"github.com/alumasinde/profy/internal/modules/settings"
	"github.com/alumasinde/profy/internal/modules/users"
	"github.com/alumasinde/profy/internal/modules/verification"
	"github.com/alumasinde/profy/internal/security"
	"github.com/alumasinde/profy/internal/shared/ids"
	"github.com/alumasinde/profy/internal/shared/reqctx"
	"github.com/alumasinde/profy/internal/shared/text"
)

// Error codes specific to providers.
const (
	CodeNoProvider    = "no_provider"
	CodeIncomplete    = "listing_incomplete"
	CodeInvalidAction = "invalid_transition"
	CodeArchived      = "listing_archived"
	CodeClaimMismatch = "claim_destination_mismatch"
	CodeAlreadyHasOne = "provider_exists"
)

var (
	errNoProvider = response.New(http.StatusNotFound, CodeNoProvider, "You have not created a provider profile yet.")
	errArchived   = response.Conflict(CodeArchived, "This listing is archived and can no longer be changed.")
)

// Service implements provider management.
type Service struct {
	db           *sql.DB
	log          *slog.Logger
	authz        *authz.Service
	settings     *settings.Service
	i18n         *i18n.Catalogue
	auth         *auth.Service
	services     *services.Service
	locations    *locations.Service
	media        *media.Service
	verification *verification.Service
	senders      map[string]notifications.Sender
	limiter      *security.Limiter
	users        users.Repository
	baseURL      string
}

// Deps are the dependencies.
type Deps struct {
	DB           *sql.DB
	Log          *slog.Logger
	Authz        *authz.Service
	Settings     *settings.Service
	I18n         *i18n.Catalogue
	Auth         *auth.Service
	Services     *services.Service
	Locations    *locations.Service
	Media        *media.Service
	Verification *verification.Service
	Senders      map[string]notifications.Sender
	// SiteURL is the bootstrap origin for links when the site_url setting is empty.
	SiteURL string
}

// New builds the service and wires the verification and location hooks.
func New(d Deps) *Service {
	s := &Service{db: d.DB, log: d.Log, authz: d.Authz, settings: d.Settings, i18n: d.I18n, auth: d.Auth, services: d.Services,
		locations: d.Locations, media: d.Media, verification: d.Verification, senders: d.Senders, limiter: security.NewLimiter(), baseURL: d.SiteURL}
	d.Verification.Providers = s
	d.Locations.OnMerge(s.onLocationMerge)
	return s
}

// ---- Loading and permissions ----------------------------------------------------------

func (s *Service) byID(ctx context.Context, ex database.Execer, id uint64, lock bool) (*row, error) {
	q := `SELECT ` + rowCols + ` FROM providers WHERE id = ?`
	if lock {
		q += ` FOR UPDATE`
	}
	return scanRow(ex.QueryRowContext(ctx, q, id))
}

func (s *Service) byPublic(ctx context.Context, ex database.Execer, publicID string, lock bool) (*row, error) {
	pub, err := ids.Parse(publicID)
	if err != nil {
		return nil, response.ErrNotFound
	}
	q := `SELECT ` + rowCols + ` FROM providers WHERE public_id = ?`
	if lock {
		q += ` FOR UPDATE`
	}
	return scanRow(ex.QueryRowContext(ctx, q, pub))
}

// own returns the caller's provider (the most recent non-archived one).
func (s *Service) own(ctx context.Context, ex database.Execer, lock bool) (*row, error) {
	p := reqctx.PrincipalFrom(ctx)
	if p == nil {
		return nil, response.ErrUnauthenticated
	}
	q := `SELECT ` + rowCols + ` FROM providers WHERE owner_user_id = ? AND listing_status <> 'archived' ORDER BY id DESC LIMIT 1`
	if lock {
		q += ` FOR UPDATE`
	}
	r, err := scanRow(ex.QueryRowContext(ctx, q, p.UserID))
	if errors.Is(err, response.ErrNotFound) {
		return nil, errNoProvider
	}
	return r, err
}

// Target selects the provider a request acts on: the caller's own (staff=false) or any
// provider by public ID for staff.
type Target struct {
	Staff    bool
	PublicID string
}

func (s *Service) resolve(ctx context.Context, ex database.Execer, t Target, lock bool) (*row, error) {
	if t.Staff {
		return s.byPublic(ctx, ex, t.PublicID, lock)
	}
	return s.own(ctx, ex, lock)
}

// require checks the owner (_own + ownership) or staff (_any) permission. Fails closed.
func (s *Service) require(ctx context.Context, t Target, r *row, ownPerm, anyPerm string) error {
	p := reqctx.PrincipalFrom(ctx)
	if p == nil {
		return response.ErrUnauthenticated
	}
	if t.Staff {
		if !s.authz.Can(ctx, p, anyPerm) {
			return response.ErrForbidden
		}
		return nil
	}
	if r.ownerUserID == nil || *r.ownerUserID != p.UserID || !s.authz.Can(ctx, p, ownPerm) {
		return response.ErrForbidden
	}
	return nil
}

// editable rejects changes to archived listings.
func editable(r *row) error {
	if r.status == StatusArchived {
		return errArchived
	}
	return nil
}

// ---- Views ----------------------------------------------------------------------------

// Pin is the provider's private map position.
type Pin struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	AccuracyM *int    `json:"accuracy_m"`
	Source    string  `json:"source"`
}

// Offered is a service the provider offers.
type Offered struct {
	ID              string  `json:"id"`
	ServiceID       string  `json:"service_id"`
	ServiceName     string  `json:"service_name"`
	ServiceSlug     string  `json:"service_slug"`
	CategoryName    string  `json:"category_name"`
	RequiresLicence bool    `json:"requires_licence"`
	ServiceLive     bool    `json:"service_live"`
	CustomTitle     *string `json:"custom_title"`
	Description     *string `json:"description"`
	PricingType     string  `json:"pricing_type"`
	PriceFrom       *int    `json:"price_from"`
	PriceTo         *int    `json:"price_to"`
	Status          string  `json:"status"`
	serviceInternal uint64
}

// Contact is one contact channel.
type Contact struct {
	Channel    string     `json:"channel"`
	Value      string     `json:"value"`
	Visibility string     `json:"visibility"`
	VerifiedAt *time.Time `json:"verified_at"`
}

// PortfolioItem is one portfolio image.
type PortfolioItem struct {
	ID       string       `json:"id"` // media public ID
	Caption  *string      `json:"caption"`
	Position int          `json:"position"`
	Media    *media.Media `json:"media"`
}

// CompletenessItem is one checklist entry (§7.6).
type CompletenessItem struct {
	Key      string `json:"key"`
	Done     bool   `json:"done"`
	Required bool   `json:"required"`
	Weight   int    `json:"weight"`
}

// Completeness is the score and checklist.
type Completeness struct {
	Score     int                `json:"score"`
	Items     []CompletenessItem `json:"items"`
	CanSubmit bool               `json:"can_submit"`
}

// HistoryEntry is one lifecycle change.
type HistoryEntry struct {
	From      *string   `json:"from"`
	To        string    `json:"to"`
	Reason    *string   `json:"reason"`
	ActorName *string   `json:"actor_name"`
	At        time.Time `json:"at"`
}

// OwnerInfo is shown to staff.
type OwnerInfo struct {
	PublicID    string  `json:"public_id"`
	DisplayName string  `json:"display_name"`
	Phone       *string `json:"phone"`
	Email       *string `json:"email"`
}

// ClaimInfo is the latest assisted-onboarding invite.
type ClaimInfo struct {
	Destination string     `json:"destination"`
	ExpiresAt   time.Time  `json:"expires_at"`
	ClaimedAt   *time.Time `json:"claimed_at"`
}

// View is the full provider representation for owners and staff.
type View struct {
	ID              string               `json:"id"`
	Type            string               `json:"provider_type"`
	DisplayName     string               `json:"display_name"`
	Slug            string               `json:"slug"`
	Path            string               `json:"path"`
	Description     string               `json:"description"`
	YearsExperience *int                 `json:"years_experience"`
	Status          string               `json:"status"`
	StatusReason    *string              `json:"status_reason"`
	BaseArea        *locations.Ref       `json:"base_area"`
	ServiceRadiusKm *int                 `json:"service_radius_km"`
	Pin             *Pin                 `json:"pin"`
	ProfilePhoto    *media.Media         `json:"profile_photo"`
	CoverPhoto      *media.Media         `json:"cover_photo"`
	Services        []Offered            `json:"services"`
	Coverage        []locations.Ref      `json:"coverage"`
	Contacts        []Contact            `json:"contacts"`
	Portfolio       []PortfolioItem      `json:"portfolio"`
	Badges          []verification.Badge `json:"badges"`
	Completeness    Completeness         `json:"completeness"`
	TermsAccepted   bool                 `json:"terms_accepted"`
	TermsVersion    *string              `json:"terms_version"`
	CurrentTerms    string               `json:"current_terms_version"`
	MetaTitle       *string              `json:"meta_title"`
	MetaDescription *string              `json:"meta_description"`
	SubmittedAt     *time.Time           `json:"submitted_at"`
	PublishedAt     *time.Time           `json:"published_at"`
	CreatedAt       time.Time            `json:"created_at"`
	UpdatedAt       time.Time            `json:"updated_at"`
	Assisted        bool                 `json:"assisted"`
	Owner           *OwnerInfo           `json:"owner,omitempty"`
	Claim           *ClaimInfo           `json:"claim,omitempty"`
	History         []HistoryEntry       `json:"history,omitempty"`
	row             *row
}

func (s *Service) offered(ctx context.Context, ex database.Execer, pid uint64) ([]Offered, error) {
	rows, err := ex.QueryContext(ctx, `SELECT public_id, service_id, custom_title, description, pricing_type, price_from, price_to, status
		FROM provider_services WHERE provider_id = ? ORDER BY id`, pid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Offered{}
	var sids []uint64
	for rows.Next() {
		var o Offered
		var pub ids.UUID
		var ct, desc sql.NullString
		var pf, pt sql.NullInt64
		if err := rows.Scan(&pub, &o.serviceInternal, &ct, &desc, &o.PricingType, &pf, &pt, &o.Status); err != nil {
			return nil, err
		}
		o.ID = pub.String()
		if ct.Valid {
			o.CustomTitle = &ct.String
		}
		if desc.Valid {
			o.Description = &desc.String
		}
		if pf.Valid {
			v := int(pf.Int64)
			o.PriceFrom = &v
		}
		if pt.Valid {
			v := int(pt.Int64)
			o.PriceTo = &v
		}
		out = append(out, o)
		sids = append(sids, o.serviceInternal)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	refs, err := s.services.ByInternal(ctx, ex, sids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		if ref := refs[out[i].serviceInternal]; ref != nil {
			out[i].ServiceID, out[i].ServiceName, out[i].ServiceSlug = ref.ID, ref.Name, ref.Slug
			out[i].CategoryName, out[i].RequiresLicence, out[i].ServiceLive = ref.CategoryName, ref.RequiresLicence, ref.Live()
		}
	}
	return out, nil
}

func (s *Service) coverage(ctx context.Context, ex database.Execer, pid uint64) ([]locations.Ref, error) {
	rows, err := ex.QueryContext(ctx, `SELECT location_type, location_id FROM provider_service_areas WHERE provider_id = ?`, pid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byType := map[string][]uint64{}
	type key struct {
		t  string
		id uint64
	}
	var order []key
	for rows.Next() {
		var k key
		if err := rows.Scan(&k.t, &k.id); err != nil {
			return nil, err
		}
		byType[k.t] = append(byType[k.t], k.id)
		order = append(order, k)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	refs := map[string]map[uint64]*locations.Ref{}
	for t, idl := range byType {
		if refs[t], err = s.locations.ByInternal(ctx, ex, t, idl); err != nil {
			return nil, err
		}
	}
	out := []locations.Ref{}
	for _, k := range order {
		if r := refs[k.t][k.id]; r != nil {
			out = append(out, *r)
		}
	}
	slices.SortFunc(out, func(a, b locations.Ref) int { return strings.Compare(a.Label, b.Label) })
	return out, nil
}

func (s *Service) contacts(ctx context.Context, ex database.Execer, pid uint64) ([]Contact, error) {
	rows, err := ex.QueryContext(ctx, `SELECT channel, value, visibility, verified_at FROM provider_contacts WHERE provider_id = ?
		ORDER BY FIELD(channel, 'phone', 'whatsapp', 'email')`, pid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Contact{}
	for rows.Next() {
		var c Contact
		var va sql.NullTime
		if err := rows.Scan(&c.Channel, &c.Value, &c.Visibility, &va); err != nil {
			return nil, err
		}
		if va.Valid {
			c.VerifiedAt = &va.Time
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Service) portfolio(ctx context.Context, ex database.Execer, pid uint64, publicOnly bool) ([]PortfolioItem, error) {
	rows, err := ex.QueryContext(ctx, `SELECT media_id, caption, position FROM provider_portfolio WHERE provider_id = ? ORDER BY position, id`, pid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type item struct {
		mid     uint64
		caption sql.NullString
		pos     int
	}
	var items []item
	var mids []uint64
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.mid, &it.caption, &it.pos); err != nil {
			return nil, err
		}
		items = append(items, it)
		mids = append(mids, it.mid)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ms, err := s.media.ByInternalIDs(ctx, ex, mids)
	if err != nil {
		return nil, err
	}
	out := []PortfolioItem{}
	for _, it := range items {
		m := ms[it.mid]
		if m == nil || m.Status == media.StatusDeleted || (publicOnly && m.Status != media.StatusActive) {
			continue
		}
		pi := PortfolioItem{ID: m.ID, Position: it.pos, Media: m}
		if it.caption.Valid {
			pi.Caption = &it.caption.String
		}
		out = append(out, pi)
	}
	return out, nil
}

func (s *Service) pin(ctx context.Context, ex database.Execer, pid uint64) (*Pin, error) {
	var p Pin
	var acc sql.NullInt64
	err := ex.QueryRowContext(ctx, `SELECT ST_Latitude(location), ST_Longitude(location), accuracy_m, source FROM provider_geo WHERE provider_id = ?`, pid).
		Scan(&p.Latitude, &p.Longitude, &acc, &p.Source)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if acc.Valid {
		v := int(acc.Int64)
		p.AccuracyM = &v
	}
	return &p, nil
}

// view assembles the full representation.
func (s *Service) view(ctx context.Context, ex database.Execer, r *row, staff bool) (*View, error) {
	v := &View{ID: r.publicID.String(), Type: r.providerType, DisplayName: r.displayName, Slug: r.slug, Path: seo.ProviderPath(r.slug),
		Description: r.description, YearsExperience: r.yearsExperience, Status: r.status, StatusReason: r.statusReason,
		ServiceRadiusKm: r.radiusKm, TermsAccepted: r.termsAcceptedAt != nil, TermsVersion: r.termsVersion,
		CurrentTerms: s.settings.String(ctx, settings.KeyProviderTermsVersion), MetaTitle: r.metaTitle, MetaDescription: r.metaDescription,
		SubmittedAt: r.submittedAt, PublishedAt: r.publishedAt, CreatedAt: r.createdAt, UpdatedAt: r.updatedAt,
		Assisted: r.createdBy != nil, row: r}
	var err error
	if r.baseAreaID != nil {
		refs, err := s.locations.ByInternal(ctx, ex, locations.Area, []uint64{*r.baseAreaID})
		if err != nil {
			return nil, err
		}
		v.BaseArea = refs[*r.baseAreaID]
	}
	var mids []uint64
	for _, m := range []*uint64{r.profileMediaID, r.coverMediaID} {
		if m != nil {
			mids = append(mids, *m)
		}
	}
	ms, err := s.media.ByInternalIDs(ctx, ex, mids)
	if err != nil {
		return nil, err
	}
	if r.profileMediaID != nil {
		v.ProfilePhoto = ms[*r.profileMediaID]
	}
	if r.coverMediaID != nil {
		v.CoverPhoto = ms[*r.coverMediaID]
	}
	if v.Services, err = s.offered(ctx, ex, r.id); err != nil {
		return nil, err
	}
	if v.Coverage, err = s.coverage(ctx, ex, r.id); err != nil {
		return nil, err
	}
	if v.Contacts, err = s.contacts(ctx, ex, r.id); err != nil {
		return nil, err
	}
	if v.Portfolio, err = s.portfolio(ctx, ex, r.id, false); err != nil {
		return nil, err
	}
	if v.Pin, err = s.pin(ctx, ex, r.id); err != nil {
		return nil, err
	}
	badges, err := s.verification.Badges(ctx, ex, []uint64{r.id})
	if err != nil {
		return nil, err
	}
	v.Badges = badges[r.id]
	if v.Badges == nil {
		v.Badges = []verification.Badge{}
	}
	v.Completeness = computeCompleteness(v)
	if staff {
		if err := s.staffExtras(ctx, ex, v); err != nil {
			return nil, err
		}
	}
	return v, nil
}

// computeCompleteness scores the profile (§7.6) and checks the minimum to submit (§12.2):
// display name, a service, base area, a service area, a contact channel and accepted terms.
func computeCompleteness(v *View) Completeness {
	liveServices := 0
	for _, o := range v.Services {
		if o.Status == "active" && o.ServiceLive {
			liveServices++
		}
	}
	portfolio := 0
	for _, p := range v.Portfolio {
		if p.Media != nil && p.Media.Status != media.StatusRejected {
			portfolio++
		}
	}
	items := []CompletenessItem{
		{Key: "display_name", Done: strings.TrimSpace(v.DisplayName) != "", Required: true, Weight: 10},
		{Key: "services", Done: liveServices > 0, Required: true, Weight: 15},
		{Key: "base_area", Done: v.BaseArea != nil && v.BaseArea.Status == "active", Required: true, Weight: 10},
		{Key: "service_areas", Done: len(v.Coverage) > 0, Required: true, Weight: 10},
		{Key: "contacts", Done: len(v.Contacts) > 0, Required: true, Weight: 10},
		{Key: "terms", Done: v.TermsAccepted, Required: true, Weight: 5},
		{Key: "description", Done: len([]rune(strings.TrimSpace(v.Description))) >= MinGoodDesc, Weight: 15},
		{Key: "profile_photo", Done: v.ProfilePhoto != nil && v.ProfilePhoto.Status != media.StatusRejected, Weight: 10},
		{Key: "portfolio", Done: portfolio > 0, Weight: 10},
		{Key: "experience", Done: v.YearsExperience != nil, Weight: 5},
	}
	c := Completeness{Items: items, CanSubmit: true}
	for _, it := range items {
		if it.Done {
			c.Score += it.Weight
		} else if it.Required {
			c.CanSubmit = false
		}
	}
	return c
}

// refresh recomputes and stores the completeness score inside tx.
func (s *Service) refresh(ctx context.Context, tx *sql.Tx, id uint64) (*View, error) {
	r, err := s.byID(ctx, tx, id, false)
	if err != nil {
		return nil, err
	}
	v, err := s.view(ctx, tx, r, false)
	if err != nil {
		return nil, err
	}
	if v.Completeness.Score != r.completeness {
		if _, err := tx.ExecContext(ctx, `UPDATE providers SET completeness_score = ? WHERE id = ?`, v.Completeness.Score, id); err != nil {
			return nil, err
		}
	}
	return v, nil
}

// ---- Creation -------------------------------------------------------------------------

// CreateInput starts a provider profile.
type CreateInput struct {
	ProviderType string `json:"provider_type"`
	DisplayName  string `json:"display_name"`
	AcceptTerms  bool   `json:"accept_terms"`
}

func validateCreate(in CreateInput) (string, map[string]string) {
	errs := map[string]string{}
	if in.ProviderType != TypeIndividual && in.ProviderType != TypeBusiness {
		errs["provider_type"] = "invalid"
	}
	name, ok := text.CleanLine(in.DisplayName, 120)
	if !ok {
		errs["display_name"] = "required"
	}
	return name, errs
}

// insertProvider creates the row with a unique slug and records history.
func (s *Service) insertProvider(ctx context.Context, tx *sql.Tx, ptype, name string, owner, createdBy *uint64) (uint64, ids.UUID, error) {
	base := text.Slugify(name)
	if base == "" {
		base = "provider"
	}
	pub := ids.New()
	for i := 1; i <= 50; i++ {
		slug := base
		if i > 1 {
			slug = base + "-" + strconv.Itoa(i)
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO providers (public_id, owner_user_id, provider_type, display_name, slug, created_by)
			VALUES (?, ?, ?, ?, ?, ?)`, pub, owner, ptype, name, slug, createdBy)
		if database.IsDuplicate(err) {
			continue
		}
		if err != nil {
			return 0, pub, err
		}
		id, _ := res.LastInsertId()
		if err := s.history(ctx, tx, uint64(id), "", StatusDraft, ""); err != nil {
			return 0, pub, err
		}
		return uint64(id), pub, nil
	}
	return 0, pub, response.Conflict(response.CodeConflict, "Could not allocate a unique address for this name.")
}

func (s *Service) history(ctx context.Context, tx *sql.Tx, pid uint64, from, to, reason string) error {
	var f, rsn, actor any
	if from != "" {
		f = from
	}
	if reason != "" {
		rsn = reason
	}
	if p := reqctx.PrincipalFrom(ctx); p != nil {
		actor = p.UserID
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO provider_status_history (provider_id, from_status, to_status, reason, actor_user_id)
		VALUES (?, ?, ?, ?, ?)`, pid, f, to, rsn, actor)
	return err
}

// acceptTerms records provider-terms consent for the owner.
func (s *Service) acceptTerms(ctx context.Context, tx *sql.Tx, pid, userID uint64) error {
	version := s.settings.String(ctx, settings.KeyProviderTermsVersion)
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `UPDATE providers SET terms_version = ?, terms_accepted_at = ? WHERE id = ?`, version, now, pid); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO user_consents (user_id, consent_type, version, granted, ip_address) VALUES (?, 'provider_terms', ?, 1, ?)`,
		userID, version, nullable(reqctx.ClientIP(ctx)))
	return err
}

func nullable(v string) any {
	if v == "" {
		return nil
	}
	return v
}

// becomeOwner grants the provider role and pre-fills contacts from the owner's verified
// login channels (already proven by OTP, so they carry contact badges).
func (s *Service) becomeOwner(ctx context.Context, tx *sql.Tx, pid, userID uint64) error {
	if err := s.users.AssignRole(ctx, tx, userID, authz.RoleProvider, nil); err != nil {
		return err
	}
	phone, email, err := s.auth.VerifiedDestinations(ctx, userID)
	if err != nil {
		return err
	}
	vis := s.settings.String(ctx, settings.KeyDefaultContactVisibility)
	now := time.Now().UTC()
	for ch, val := range map[string]string{ChannelPhone: phone, ChannelEmail: email} {
		if val == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT IGNORE INTO provider_contacts (provider_id, channel, value, visibility, verified_at)
			VALUES (?, ?, ?, ?, ?)`, pid, ch, val, vis, now); err != nil {
			return err
		}
		if err := s.syncContactBadges(ctx, tx, pid); err != nil {
			return err
		}
	}
	return nil
}

// Create starts the caller's provider profile (self-service onboarding).
func (s *Service) Create(ctx context.Context, in CreateInput) (*View, error) {
	p := reqctx.PrincipalFrom(ctx)
	if p == nil {
		return nil, response.ErrUnauthenticated
	}
	name, errs := validateCreate(in)
	if len(errs) > 0 {
		return nil, response.Validation(errs)
	}
	var id uint64
	err := database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		var lockUser uint64
		if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id = ? FOR UPDATE`, p.UserID).Scan(&lockUser); err != nil {
			return err
		}
		var existing int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM providers WHERE owner_user_id = ? AND listing_status <> 'archived'`, p.UserID).Scan(&existing); err != nil {
			return err
		}
		if existing > 0 {
			return response.Conflict(CodeAlreadyHasOne, "You already have a provider profile.")
		}
		uid := p.UserID
		pid, pub, err := s.insertProvider(ctx, tx, in.ProviderType, name, &uid, nil)
		if err != nil {
			return err
		}
		id = pid
		if err := s.becomeOwner(ctx, tx, pid, uid); err != nil {
			return err
		}
		if in.AcceptTerms {
			if err := s.acceptTerms(ctx, tx, pid, uid); err != nil {
				return err
			}
		}
		if _, err := s.refresh(ctx, tx, pid); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "providers.created", EntityType: "provider", EntityID: pub.String(),
			NewValues: map[string]any{"display_name": name, "provider_type": in.ProviderType}})
	})
	if err != nil {
		return nil, err
	}
	s.authz.Invalidate()
	r, err := s.byID(ctx, s.db, id, false)
	if err != nil {
		return nil, err
	}
	return s.view(ctx, s.db, r, false)
}

// Get returns the target provider's view.
func (s *Service) Get(ctx context.Context, t Target) (*View, error) {
	r, err := s.resolve(ctx, s.db, t, false)
	if err != nil {
		return nil, err
	}
	if err := s.require(ctx, t, r, "providers.update_own", "providers.view_any"); err != nil {
		return nil, err
	}
	return s.view(ctx, s.db, r, t.Staff)
}

// ---- Profile edits --------------------------------------------------------------------

// ProfileInput is a partial profile update.
type ProfileInput struct {
	ProviderType    *string `json:"provider_type"`
	DisplayName     *string `json:"display_name"`
	Description     *string `json:"description"`
	YearsExperience *int    `json:"years_experience"`
	ClearExperience bool    `json:"clear_years_experience"`
	AcceptTerms     bool    `json:"accept_terms"`
	Slug            *string `json:"slug"`             // staff only
	MetaTitle       *string `json:"meta_title"`       // staff only
	MetaDescription *string `json:"meta_description"` // staff only
}

// UpdateProfile edits profile fields. Material edits on a published listing re-enter
// moderation when listing_edit_moderation = "pre" (§12.2).
func (s *Service) UpdateProfile(ctx context.Context, t Target, in ProfileInput) (*View, error) {
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
		name, ptype, desc := r.displayName, r.providerType, r.description
		years := r.yearsExperience
		slug := r.slug
		if in.DisplayName != nil {
			n, ok := text.CleanLine(*in.DisplayName, 120)
			if !ok {
				errs["display_name"] = "required"
			}
			name = n
		}
		if in.ProviderType != nil {
			if *in.ProviderType != TypeIndividual && *in.ProviderType != TypeBusiness {
				errs["provider_type"] = "invalid"
			} else if *in.ProviderType != r.providerType && r.status != StatusDraft {
				errs["provider_type"] = "read_only"
			}
			ptype = *in.ProviderType
		}
		if in.Description != nil {
			d, ok := text.CleanText(*in.Description, MaxDescription)
			if !ok {
				errs["description"] = "too_long"
			}
			desc = d
		}
		if in.YearsExperience != nil {
			if *in.YearsExperience < 0 || *in.YearsExperience > MaxYearsExpInfo {
				errs["years_experience"] = "out_of_range"
			}
			years = in.YearsExperience
		}
		if in.ClearExperience {
			years = nil
		}
		if (in.Slug != nil || in.MetaTitle != nil || in.MetaDescription != nil) && !t.Staff {
			errs["slug"] = "read_only"
		}
		if in.Slug != nil {
			slug = strings.TrimSpace(*in.Slug)
			if !text.ValidSlug(slug) {
				errs["slug"] = "invalid"
			}
		}
		for k, v := range map[string]*string{"meta_title": in.MetaTitle, "meta_description": in.MetaDescription} {
			if v != nil {
				max := 160
				if k == "meta_description" {
					max = 320
				}
				if c, ok := text.CleanLine(*v, max); !ok && c != "" {
					errs[k] = "invalid"
				}
			}
		}
		if in.AcceptTerms && t.Staff {
			errs["accept_terms"] = "read_only" // only the owner can accept terms
		}
		if len(errs) > 0 {
			return response.Validation(errs)
		}
		mt, md := any(r.metaTitle), any(r.metaDescription)
		if in.MetaTitle != nil {
			mt = optional(*in.MetaTitle)
		}
		if in.MetaDescription != nil {
			md = optional(*in.MetaDescription)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE providers SET provider_type = ?, display_name = ?, description = ?, years_experience = ?,
			slug = ?, meta_title = ?, meta_description = ? WHERE id = ?`, ptype, name, desc, years, slug, mt, md, r.id); err != nil {
			if database.IsDuplicate(err) {
				return response.Validation(map[string]string{"slug": "taken"})
			}
			return err
		}
		if slug != r.slug && r.publishedAt != nil {
			if err := seo.AddRedirect(ctx, tx, seo.ProviderPath(r.slug), seo.ProviderPath(slug), "auto"); err != nil {
				return err
			}
		}
		if in.AcceptTerms && r.ownerUserID != nil {
			if err := s.acceptTerms(ctx, tx, r.id, *r.ownerUserID); err != nil {
				return err
			}
		}
		if name != r.displayName {
			if err := s.materialChange(ctx, tx, r, "display_name"); err != nil {
				return err
			}
		}
		if _, err := s.refresh(ctx, tx, r.id); err != nil {
			return err
		}
		old, nw := map[string]any{}, map[string]any{}
		for k, pair := range map[string][2]any{"display_name": {r.displayName, name}, "provider_type": {r.providerType, ptype}, "slug": {r.slug, slug}} {
			if pair[0] != pair[1] {
				old[k], nw[k] = pair[0], pair[1]
			}
		}
		if desc != r.description {
			old["description_length"], nw["description_length"] = len(r.description), len(desc)
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "providers.profile.updated", EntityType: "provider", EntityID: r.publicID.String(),
			OldValues: old, NewValues: nw})
	})
	if err != nil {
		return nil, err
	}
	return s.viewByID(ctx, id, t.Staff)
}

func optional(v string) any {
	if v = strings.TrimSpace(v); v == "" {
		return nil
	}
	return v
}

func (s *Service) viewByID(ctx context.Context, id uint64, staff bool) (*View, error) {
	r, err := s.byID(ctx, s.db, id, false)
	if err != nil {
		return nil, err
	}
	return s.view(ctx, s.db, r, staff)
}

// materialChange moves a published listing back to review when pre-moderation of material
// edits is configured.
func (s *Service) materialChange(ctx context.Context, tx *sql.Tx, r *row, what string) error {
	if r.status != StatusPublished || s.settings.String(ctx, settings.KeyListingEditModeration) != "pre" {
		return nil
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `UPDATE providers SET listing_status = 'pending_review', submitted_at = ? WHERE id = ?`, now, r.id); err != nil {
		return err
	}
	r.status = StatusPending
	return s.history(ctx, tx, r.id, StatusPublished, StatusPending, "material_change:"+what)
}

// ---- Lifecycle ------------------------------------------------------------------------

// Transition performs a lifecycle action (§12.2). Owners may submit, pause, resume and
// archive their own listing; staff approve, reject, suspend, restore and archive.
func (s *Service) Transition(ctx context.Context, t Target, action, reason string) (*View, error) {
	tr, ok := Transitions[action]
	if !ok {
		return nil, response.Validation(map[string]string{"action": "invalid"})
	}
	reason = strings.TrimSpace(reason)
	if tr.needsReason && (reason == "" || len([]rune(reason)) > 500) {
		return nil, response.Validation(map[string]string{"reason": "required"})
	}
	p := reqctx.PrincipalFrom(ctx)
	var id uint64
	err := database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		r, err := s.resolve(ctx, tx, t, true)
		if err != nil {
			return err
		}
		id = r.id
		if t.Staff {
			if tr.staffPerm == "" || !s.authz.Can(ctx, p, tr.staffPerm) {
				return response.ErrForbidden
			}
		} else if tr.ownerAllowed == "" || r.ownerUserID == nil || *r.ownerUserID != p.UserID || !s.authz.Can(ctx, p, tr.ownerAllowed) {
			return response.ErrForbidden
		}
		if !slices.Contains(tr.from, r.status) {
			return response.Conflict(CodeInvalidAction, "This action is not possible in the listing's current state.")
		}
		now := time.Now().UTC()
		sets := []string{"listing_status = ?"}
		args := []any{tr.to}
		switch action {
		case "submit", "approve":
			// Approval re-checks completeness: pending listings stay editable.
			v, err := s.view(ctx, tx, r, false)
			if err != nil {
				return err
			}
			if !v.Completeness.CanSubmit {
				missing := []string{}
				for _, it := range v.Completeness.Items {
					if it.Required && !it.Done {
						missing = append(missing, it.Key)
					}
				}
				e := response.Conflict(CodeIncomplete, "Complete the required sections before submitting.")
				e.Fields = map[string]string{"missing": strings.Join(missing, ",")}
				return e
			}
			if r.ownerUserID == nil {
				return response.Conflict(CodeIncomplete, "An assisted listing must be claimed by its owner before review.")
			}
			if action == "submit" {
				sets = append(sets, "submitted_at = ?", "status_reason = NULL")
				args = append(args, now)
			} else {
				sets = append(sets, "status_reason = NULL", "published_at = COALESCE(published_at, ?)")
				args = append(args, now)
			}
		case "restore", "resume":
			sets = append(sets, "status_reason = NULL", "published_at = COALESCE(published_at, ?)")
			args = append(args, now)
		case "reject", "suspend":
			sets = append(sets, "status_reason = ?")
			args = append(args, reason)
		}
		args = append(args, r.id)
		if _, err := tx.ExecContext(ctx, `UPDATE providers SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); err != nil {
			return err
		}
		if err := s.history(ctx, tx, r.id, r.status, tr.to, reason); err != nil {
			return err
		}
		if t.Staff && action != "archive" {
			if err := moderation.Record(ctx, tx, "provider", r.publicID.String(), "listing."+action, reason); err != nil {
				return err
			}
		}
		if ev, ok := actionNotice[action]; ok {
			if err := jobsqueue.Enqueue(ctx, tx, notifications.JobProviderNotify, notifications.ProviderNotice{
				ProviderID: r.id, Event: ev, Reason: reason}, jobsqueue.Options{}); err != nil {
				return err
			}
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "providers.listing." + action, EntityType: "provider", EntityID: r.publicID.String(),
			OldValues: map[string]any{"status": r.status}, NewValues: map[string]any{"status": tr.to, "reason": reason}})
	})
	if err != nil {
		return nil, err
	}
	return s.viewByID(ctx, id, t.Staff)
}

// staffExtras adds owner, claim and history for staff views.
func (s *Service) staffExtras(ctx context.Context, ex database.Execer, v *View) error {
	r := v.row
	if r.ownerUserID != nil {
		u, err := s.users.ByID(ctx, ex, *r.ownerUserID)
		if err != nil {
			return err
		}
		v.Owner = &OwnerInfo{PublicID: u.PublicID.String(), DisplayName: u.Names.Display(), Phone: u.PhoneE164, Email: u.Email}
	}
	var c ClaimInfo
	var claimed sql.NullTime
	err := ex.QueryRowContext(ctx, `SELECT destination, expires_at, claimed_at FROM provider_claims WHERE provider_id = ? ORDER BY id DESC LIMIT 1`, r.id).
		Scan(&c.Destination, &c.ExpiresAt, &claimed)
	if err == nil {
		c.Destination = security.MaskDestination(c.Destination)
		if claimed.Valid {
			c.ClaimedAt = &claimed.Time
		}
		v.Claim = &c
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	rows, err := ex.QueryContext(ctx, `SELECT h.from_status, h.to_status, h.reason, u.first_name, u.middle_name, u.last_name, h.created_at
		FROM provider_status_history h LEFT JOIN users u ON u.id = h.actor_user_id WHERE h.provider_id = ? ORDER BY h.id DESC LIMIT 50`, r.id)
	if err != nil {
		return err
	}
	defer rows.Close()
	v.History = []HistoryEntry{}
	for rows.Next() {
		var h HistoryEntry
		var from, reason, f, m, l sql.NullString
		if err := rows.Scan(&from, &h.To, &reason, &f, &m, &l, &h.At); err != nil {
			return err
		}
		if from.Valid {
			h.From = &from.String
		}
		if reason.Valid {
			h.Reason = &reason.String
		}
		if f.Valid {
			var mid *string
			if m.Valid {
				mid = &m.String
			}
			n := users.Names{First: f.String, Middle: mid, Last: l.String}.Display()
			h.ActorName = &n
		}
		v.History = append(v.History, h)
	}
	return rows.Err()
}

// ---- verification.Providers implementation --------------------------------------------

// OwnProvider implements verification.Providers.
func (s *Service) OwnProvider(ctx context.Context) (*verification.ProviderRef, error) {
	r, err := s.own(ctx, s.db, false)
	if err != nil {
		return nil, err
	}
	if err := editable(r); err != nil {
		return nil, err
	}
	return &verification.ProviderRef{InternalID: r.id, PublicID: r.publicID.String(), DisplayName: r.displayName, Type: r.providerType,
		OwnerUserID: r.ownerUserID}, nil
}

// ProviderRefs implements verification.Providers.
func (s *Service) ProviderRefs(ctx context.Context, ex database.Execer, idList []uint64) (map[uint64]*verification.ProviderRef, error) {
	out := map[uint64]*verification.ProviderRef{}
	for _, id := range idList {
		if _, ok := out[id]; ok {
			continue
		}
		r, err := s.byID(ctx, ex, id, false)
		if err != nil {
			return nil, err
		}
		ref := &verification.ProviderRef{InternalID: r.id, PublicID: r.publicID.String(), DisplayName: r.displayName, Type: r.providerType,
			OwnerUserID: r.ownerUserID}
		if r.ownerUserID != nil {
			if u, err := s.users.ByID(ctx, ex, *r.ownerUserID); err == nil {
				ref.OwnerName = u.Names.Display()
			}
		}
		out[id] = ref
	}
	return out, nil
}

// LicenceServices implements verification.Providers.
func (s *Service) LicenceServices(ctx context.Context, ex database.Execer, providerID uint64) ([]verification.LicenceService, error) {
	offered, err := s.offered(ctx, ex, providerID)
	if err != nil {
		return nil, err
	}
	out := []verification.LicenceService{}
	for _, o := range offered {
		if o.RequiresLicence && o.Status == "active" {
			out = append(out, verification.LicenceService{InternalID: o.serviceInternal, ID: o.ServiceID, Name: o.ServiceName})
		}
	}
	return out, nil
}
