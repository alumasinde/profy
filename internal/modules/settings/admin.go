package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/alumasinde/profy/internal/authz"
	"github.com/alumasinde/profy/internal/database"
	"github.com/alumasinde/profy/internal/http/response"
	"github.com/alumasinde/profy/internal/modules/audit"
	"github.com/alumasinde/profy/internal/security"
	"github.com/alumasinde/profy/internal/shared/reqctx"
	"github.com/alumasinde/profy/internal/shared/text"
)

// Kind is a setting's value type; each kind has its own validation.
type Kind string

// Setting kinds.
const (
	KindString   Kind = "string"
	KindURL      Kind = "url"   // absolute https URL (http allowed only for localhost)
	KindAsset    Kind = "asset" // https URL or /static/ path
	KindEmail    Kind = "email"
	KindPhone    Kind = "phone"    // Kenyan mobile → E.164
	KindColor    Kind = "color"    // #rrggbb, contrast-checked as a set
	KindLocale   Kind = "locale"   // must have a string catalogue
	KindTimezone Kind = "timezone" // IANA name
	KindCountry  Kind = "country"  // ISO 3166-1 alpha-2
	KindCurrency Kind = "currency" // ISO 4217
	KindVersion  Kind = "version"  // policy version label
	KindSocial   Kind = "social"   // [{label, url}]
	KindChannels Kind = "channels" // non-empty subset of sms, email
)

// Definition describes one administrable setting.
type Definition struct {
	Key        string `json:"key"`
	Kind       Kind   `json:"kind"`
	Group      string `json:"group"`
	Required   bool   `json:"required"`
	MaxLen     int    `json:"max_length,omitempty"`
	Public     bool   `json:"public"`
	Permission string `json:"-"`
}

// Definitions is the code-defined schema of editable settings (§4.1). Keys not listed here
// (for example maintenance_mode) cannot be changed through the settings endpoint.
var Definitions = []Definition{
	{Key: KeySiteName, Kind: KindString, Group: "brand", Required: true, MaxLen: 60, Public: true},
	{Key: KeySiteShortName, Kind: KindString, Group: "brand", Required: true, MaxLen: 30, Public: true},
	{Key: KeySiteTagline, Kind: KindString, Group: "brand", MaxLen: 160, Public: true},
	{Key: KeySiteDescription, Kind: KindString, Group: "brand", MaxLen: 320, Public: true},
	{Key: "logo_light", Kind: KindAsset, Group: "brand", MaxLen: 500, Public: true},
	{Key: "logo_dark", Kind: KindAsset, Group: "brand", MaxLen: 500, Public: true},
	{Key: "favicon", Kind: KindAsset, Group: "brand", MaxLen: 500, Public: true},
	{Key: "default_og_image", Kind: KindAsset, Group: "brand", MaxLen: 500, Public: true},
	{Key: KeyPrimaryColor, Kind: KindColor, Group: "theme", Required: true, Public: true},
	{Key: KeySecondaryColor, Kind: KindColor, Group: "theme", Required: true, Public: true},
	{Key: KeyAccentColor, Kind: KindColor, Group: "theme", Required: true, Public: true},
	{Key: KeySiteURL, Kind: KindURL, Group: "contact", MaxLen: 200, Public: true},
	{Key: "support_email", Kind: KindEmail, Group: "contact", MaxLen: 254, Public: true},
	{Key: "support_phone", Kind: KindPhone, Group: "contact", Public: true},
	{Key: "whatsapp_number", Kind: KindPhone, Group: "contact", Public: true},
	{Key: "social_links", Kind: KindSocial, Group: "contact", Public: true},
	{Key: KeyDefaultLocale, Kind: KindLocale, Group: "regional", Required: true, Public: true},
	{Key: "default_timezone", Kind: KindTimezone, Group: "regional", Required: true, Public: true},
	{Key: "default_country", Kind: KindCountry, Group: "regional", Required: true, Public: true},
	{Key: "default_currency", Kind: KindCurrency, Group: "regional", Required: true, Public: true},
	{Key: KeyTermsVersion, Kind: KindVersion, Group: "legal", Public: true},
	{Key: KeyPrivacyVersion, Kind: KindVersion, Group: "legal", Public: true},
	{Key: KeyOTPChannels, Kind: KindChannels, Group: "security", Required: true, Permission: "notification_channels.configure"},
}

func definition(key string) (Definition, bool) {
	for _, d := range Definitions {
		if d.Key == key {
			if d.Permission == "" {
				d.Permission = "platform.settings.update"
			}
			return d, true
		}
	}
	return Definition{}, false
}

var (
	hexColorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	countryRe  = regexp.MustCompile(`^[A-Z]{2}$`)
	currencyRe = regexp.MustCompile(`^[A-Z]{3}$`)
	versionRe  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,30}$`)
)

// SocialLink is one entry in social_links.
type SocialLink struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// validateValue checks and normalizes one raw JSON value, returning the JSON to store or a field code.
func validateValue(d Definition, raw json.RawMessage, hasLocale func(string) bool) (json.RawMessage, string) {
	switch d.Kind {
	case KindSocial:
		var links []SocialLink
		if json.Unmarshal(raw, &links) != nil || len(links) > 10 {
			return nil, "invalid"
		}
		for i := range links {
			l, ok := text.CleanLine(links[i].Label, 40)
			if !ok || !isHTTPS(links[i].URL) {
				return nil, "invalid"
			}
			links[i].Label = l
		}
		if links == nil {
			links = []SocialLink{}
		}
		b, _ := json.Marshal(links)
		return b, ""
	case KindChannels:
		var chans []string
		if json.Unmarshal(raw, &chans) != nil || len(chans) == 0 {
			return nil, "required"
		}
		out := []string{}
		for _, c := range chans {
			if c != "sms" && c != "email" {
				return nil, "invalid"
			}
			if !slices.Contains(out, c) {
				out = append(out, c)
			}
		}
		b, _ := json.Marshal(out)
		return b, ""
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return nil, "invalid"
	}
	s = strings.TrimSpace(s)
	if s == "" {
		if d.Required {
			return nil, "required"
		}
		b, _ := json.Marshal("")
		return b, ""
	}
	if d.MaxLen > 0 && len([]rune(s)) > d.MaxLen {
		return nil, "too_long"
	}
	switch d.Kind {
	case KindString:
		if _, ok := text.CleanLine(s, d.MaxLen); !ok {
			return nil, "invalid"
		}
	case KindURL:
		u, err := url.Parse(s)
		local := err == nil && u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1")
		if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && !local) || (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
			return nil, "invalid"
		}
		s = strings.TrimRight(s, "/")
	case KindAsset:
		if !isHTTPS(s) && !(strings.HasPrefix(s, "/static/") && !strings.Contains(s, "..")) {
			return nil, "invalid"
		}
	case KindEmail:
		e, ok := security.NormalizeEmail(s)
		if !ok {
			return nil, "invalid"
		}
		s = e
	case KindPhone:
		p, ok := security.NormalizeKenyanPhone(s)
		if !ok {
			return nil, "invalid"
		}
		s = p
	case KindColor:
		if !hexColorRe.MatchString(s) {
			return nil, "invalid"
		}
		s = strings.ToLower(s)
	case KindLocale:
		if !hasLocale(s) {
			return nil, "invalid"
		}
	case KindTimezone:
		if _, err := time.LoadLocation(s); err != nil || s == "Local" {
			return nil, "invalid"
		}
	case KindCountry:
		if !countryRe.MatchString(s) {
			return nil, "invalid"
		}
	case KindCurrency:
		if !currencyRe.MatchString(s) {
			return nil, "invalid"
		}
	case KindVersion:
		if !versionRe.MatchString(s) {
			return nil, "invalid"
		}
	}
	b, _ := json.Marshal(s)
	return b, ""
}

func isHTTPS(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && !strings.ContainsAny(s, " \"'<>")
}

// ---- WCAG contrast (§7.1: reject theme colours that fail contrast checks) ----------------

func luminance(hex string) float64 {
	ch := func(i int) float64 {
		v, _ := strconv.ParseUint(hex[i:i+2], 16, 8)
		c := float64(v) / 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(1) + 0.7152*ch(3) + 0.0722*ch(5)
}

// Contrast returns the WCAG contrast ratio between two #rrggbb colours.
func Contrast(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// Surfaces used by the UI stylesheets (public.css, app.css).
const (
	surfaceWhite = "#ffffff"
	surfacePage  = "#f8fafc"
)

// ContrastErrors checks the theme as used by the UI (WCAG 2.2 AA):
// primary is button background behind white text and link text on the page (4.5:1);
// secondary is heading/brand text on white and page backgrounds (4.5:1);
// accent is the focus indicator against the page background (3:1, non-text).
func ContrastErrors(primary, secondary, accent string) map[string]string {
	errs := map[string]string{}
	if Contrast(primary, surfaceWhite) < 4.5 || Contrast(primary, surfacePage) < 4.5 {
		errs[KeyPrimaryColor] = "contrast_too_low"
	}
	if Contrast(secondary, surfaceWhite) < 4.5 || Contrast(secondary, surfacePage) < 4.5 {
		errs[KeySecondaryColor] = "contrast_too_low"
	}
	if Contrast(accent, surfacePage) < 3 {
		errs[KeyAccentColor] = "contrast_too_low"
	}
	return errs
}

// ---- Admin service --------------------------------------------------------------------

// AdminSetting is a setting with its definition, for the admin screen.
type AdminSetting struct {
	Definition
	Value json.RawMessage `json:"value"`
}

// AdminList returns every administrable setting with its current value.
func (s *Service) AdminList(ctx context.Context) ([]AdminSetting, error) {
	snap, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]AdminSetting, 0, len(Definitions))
	for _, d := range Definitions {
		v, ok := snap.values[d.Key]
		if !ok {
			def := defaults[d.Key]
			if def == nil {
				def = ""
			}
			v, _ = json.Marshal(def)
		}
		out = append(out, AdminSetting{Definition: d, Value: v})
	}
	return out, nil
}

// Update applies a partial update. Each key needs its definition's permission; the whole
// patch is validated (including colour contrast as a set) before anything is written, then
// written and audited in one transaction.
func (s *Service) Update(ctx context.Context, az *authz.Service, patch map[string]json.RawMessage, hasLocale func(string) bool) error {
	p := reqctx.PrincipalFrom(ctx)
	if len(patch) == 0 {
		return response.Validation(map[string]string{"settings": "required"})
	}
	errs := map[string]string{}
	clean := map[string]json.RawMessage{}
	for k, raw := range patch {
		d, ok := definition(k)
		if !ok {
			errs[k] = "unknown"
			continue
		}
		if !az.Can(ctx, p, d.Permission) {
			return response.ErrForbidden
		}
		v, code := validateValue(d, raw, hasLocale)
		if code != "" {
			errs[k] = code
			continue
		}
		clean[k] = v
	}
	if len(errs) > 0 {
		return response.Validation(errs)
	}
	color := func(k string) string {
		if v, ok := clean[k]; ok {
			var c string
			_ = json.Unmarshal(v, &c)
			return c
		}
		return strings.ToLower(s.String(ctx, k))
	}
	_, p1 := clean[KeyPrimaryColor]
	_, p2 := clean[KeySecondaryColor]
	_, p3 := clean[KeyAccentColor]
	if p1 || p2 || p3 {
		if ce := ContrastErrors(color(KeyPrimaryColor), color(KeySecondaryColor), color(KeyAccentColor)); len(ce) > 0 {
			return response.Validation(ce)
		}
	}
	err := database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		oldVals, newVals := map[string]any{}, map[string]any{}
		for k, v := range clean {
			d, _ := definition(k)
			var old []byte
			err := tx.QueryRowContext(ctx, `SELECT value_json FROM site_settings WHERE setting_key = ? FOR UPDATE`, k).Scan(&old)
			if err != nil && err != sql.ErrNoRows {
				return err
			}
			if string(old) == string(v) {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO site_settings (setting_key, value_json, is_public, updated_by)
				VALUES (?, ?, ?, ?) AS new ON DUPLICATE KEY UPDATE value_json = new.value_json, updated_by = new.updated_by`,
				k, string(v), d.Public, p.UserID); err != nil {
				return err
			}
			oldVals[k], newVals[k] = rawAny(old), rawAny(v)
		}
		if len(newVals) == 0 {
			return nil
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "settings.updated", EntityType: "site_settings",
			OldValues: oldVals, NewValues: newVals})
	})
	if err == nil {
		s.Invalidate()
	}
	return err
}

func rawAny(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	var v any
	_ = json.Unmarshal(b, &v)
	return v
}

// FlagView is a feature flag for administration.
type FlagView struct {
	Key         string `json:"key"`
	Enabled     bool   `json:"enabled"`
	Description string `json:"description"`
}

// Flags lists feature flags. Flags are created by seeds (code references them); admins toggle them.
func (s *Service) Flags(ctx context.Context) ([]FlagView, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT flag_key, is_enabled, description FROM feature_flags ORDER BY flag_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FlagView{}
	for rows.Next() {
		var f FlagView
		if err := rows.Scan(&f.Key, &f.Enabled, &f.Description); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// SetFlags toggles existing flags (audited).
func (s *Service) SetFlags(ctx context.Context, flags map[string]bool) error {
	if len(flags) == 0 {
		return response.Validation(map[string]string{"flags": "required"})
	}
	p := reqctx.PrincipalFrom(ctx)
	err := database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		oldVals, newVals := map[string]any{}, map[string]any{}
		for k, on := range flags {
			var cur bool
			err := tx.QueryRowContext(ctx, `SELECT is_enabled FROM feature_flags WHERE flag_key = ? FOR UPDATE`, k).Scan(&cur)
			if err == sql.ErrNoRows {
				return response.Validation(map[string]string{k: "unknown"})
			}
			if err != nil {
				return err
			}
			if cur == on {
				continue
			}
			if _, err := tx.ExecContext(ctx, `UPDATE feature_flags SET is_enabled = ?, updated_by = ? WHERE flag_key = ?`, on, p.UserID, k); err != nil {
				return err
			}
			oldVals[k], newVals[k] = cur, on
		}
		if len(newVals) == 0 {
			return nil
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "feature_flags.updated", EntityType: "feature_flags",
			OldValues: oldVals, NewValues: newVals})
	})
	if err == nil {
		s.Invalidate()
	}
	return err
}

// SetMaintenance turns maintenance mode on or off (audited).
func (s *Service) SetMaintenance(ctx context.Context, on bool) error {
	p := reqctx.PrincipalFrom(ctx)
	err := database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		v, _ := json.Marshal(on)
		if _, err := tx.ExecContext(ctx, `INSERT INTO site_settings (setting_key, value_json, is_public, updated_by)
			VALUES (?, ?, 0, ?) AS new ON DUPLICATE KEY UPDATE value_json = new.value_json, updated_by = new.updated_by`,
			KeyMaintenance, string(v), p.UserID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "platform.maintenance." + map[bool]string{true: "enabled", false: "disabled"}[on],
			EntityType: "site_settings", EntityID: KeyMaintenance, NewValues: map[string]any{"enabled": on}})
	})
	if err == nil {
		s.Invalidate()
	}
	return err
}

// ---- HTTP -----------------------------------------------------------------------------

// AdminHandler exposes /api/v1/admin/settings, /feature-flags and /maintenance.
type AdminHandler struct {
	svc       *Service
	authz     *authz.Service
	hasLocale func(string) bool
}

// NewAdminHandler builds the handler.
func NewAdminHandler(svc *Service, az *authz.Service, hasLocale func(string) bool) *AdminHandler {
	return &AdminHandler{svc: svc, authz: az, hasLocale: hasLocale}
}

// Register mounts routes.
func (h *AdminHandler) Register(mux *http.ServeMux, prefix string) {
	req := func(perm string, fn http.HandlerFunc) http.Handler { return h.authz.Require(perm)(fn) }
	mux.Handle("GET "+prefix+"/admin/settings", req("platform.settings.view", h.list))
	mux.Handle("PATCH "+prefix+"/admin/settings", authz.RequireAuth(http.HandlerFunc(h.patch))) // per-key permissions
	mux.Handle("GET "+prefix+"/admin/feature-flags", req("platform.feature_flags.view", h.flags))
	mux.Handle("PATCH "+prefix+"/admin/feature-flags", req("platform.feature_flags.update", h.setFlags))
	mux.Handle("GET "+prefix+"/admin/maintenance", req("platform.settings.view", h.maintenance))
	mux.Handle("PUT "+prefix+"/admin/maintenance", authz.RequireAuth(http.HandlerFunc(h.setMaintenance)))
}

func (h *AdminHandler) list(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.AdminList(r.Context())
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.OK(w, list)
}

func (h *AdminHandler) patch(w http.ResponseWriter, r *http.Request) {
	var in map[string]json.RawMessage
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	if err := h.svc.Update(r.Context(), h.authz, in, h.hasLocale); err != nil {
		response.Fail(w, r, err)
		return
	}
	h.list(w, r)
}

func (h *AdminHandler) flags(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.Flags(r.Context())
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.OK(w, list)
}

func (h *AdminHandler) setFlags(w http.ResponseWriter, r *http.Request) {
	var in map[string]bool
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	if err := h.svc.SetFlags(r.Context(), in); err != nil {
		response.Fail(w, r, err)
		return
	}
	h.flags(w, r)
}

func (h *AdminHandler) maintenance(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]bool{"enabled": h.svc.Bool(r.Context(), KeyMaintenance)})
}

func (h *AdminHandler) setMaintenance(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	if in.Enabled == nil {
		response.Fail(w, r, response.Validation(map[string]string{"enabled": "required"}))
		return
	}
	perm := "platform.maintenance.disable"
	if *in.Enabled {
		perm = "platform.maintenance.enable"
	}
	if !h.authz.Can(r.Context(), reqctx.PrincipalFrom(r.Context()), perm) {
		response.Fail(w, r, response.ErrForbidden)
		return
	}
	if err := h.svc.SetMaintenance(r.Context(), *in.Enabled); err != nil {
		response.Fail(w, r, err)
		return
	}
	h.maintenance(w, r)
}
