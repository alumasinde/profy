package providers

import (
	"context"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/alumasinde/profy/internal/http/response"
	"github.com/alumasinde/profy/internal/modules/analytics"
	"github.com/alumasinde/profy/internal/modules/media"
	"github.com/alumasinde/profy/internal/modules/public"
	"github.com/alumasinde/profy/internal/modules/seo"
	"github.com/alumasinde/profy/internal/modules/settings"
	"github.com/alumasinde/profy/internal/modules/verification"
	"github.com/alumasinde/profy/internal/security"
	"github.com/alumasinde/profy/internal/shared/reqctx"
)

// revealLimit bounds contact reveals per client to deter scraping (§12.5, §16.1).
var revealLimit = security.Limit{Max: 30, Window: 10 * time.Minute}

type pubService struct {
	Name, Category, Title, Description, Price string
}

type pubImage struct {
	Src, SrcSet, Caption string
	Width, Height        int
}

type pubContact struct {
	Channel  string
	Value    string       // only when shown
	Href     template.URL // built from validated values; tel: needs an explicit type
	Reveal   string       // link that reveals the value (no-JS)
	Login    string       // sign-in link for logged_in_only
	Revealed bool
}

// ProfilePage is the template data for /professionals/{slug}.
type ProfilePage struct {
	Name, Type, Description string
	Years                   int // 0 = not shown
	Area                    string
	Coverage                []string
	Radius                  int
	Photo, PhotoSrcSet      string
	Cover                   string
	Services                []pubService
	Badges                  []verification.Badge
	Portfolio               []pubImage
	Contacts                []pubContact
	Available               bool
	Path                    string
	RevealLimited           bool
	Crumbs                  []Crumb
	preview                 bool
}

// Crumb is one breadcrumb link.
type Crumb struct {
	Label string
	URL   string
}

func breadcrumbLD(origin string, crumbs []Crumb) map[string]any {
	items := make([]map[string]any, 0, len(crumbs))
	for i, c := range crumbs {
		it := map[string]any{"@type": "ListItem", "position": i + 1, "name": c.Label}
		if c.URL != "" {
			it["item"] = origin + c.URL
		}
		items = append(items, it)
	}
	return map[string]any{"@type": "BreadcrumbList", "itemListElement": items}
}

// IsPreview lets the layout show the preview banner.
func (p ProfilePage) IsPreview() bool { return p.preview }

// PublicHandler serves public provider pages and the badge explainer.
type PublicHandler struct {
	svc *Service
	pub *public.Handler
}

// NewPublicHandler builds the handler.
func NewPublicHandler(svc *Service, pub *public.Handler) *PublicHandler {
	return &PublicHandler{svc: svc, pub: pub}
}

// Register mounts public routes.
func (h *PublicHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /professionals/{slug}", h.profile)
	mux.HandleFunc("GET /preview/professionals/{id}", h.preview)
	mux.HandleFunc("GET /badges", h.badges)
}

// APIHandler exposes the JS contact-reveal endpoint.
type APIHandler struct{ svc *Service }

// NewAPIHandler builds the handler.
func NewAPIHandler(svc *Service) *APIHandler { return &APIHandler{svc: svc} }

// Register mounts routes.
func (h *APIHandler) Register(mux *http.ServeMux, prefix string) {
	mux.HandleFunc("POST "+prefix+"/providers/{slug}/contact-reveal", h.reveal)
}

func (s *Service) bySlug(ctx context.Context, slug string) (*row, error) {
	if slug == "" || len(slug) > 140 {
		return nil, response.ErrNotFound
	}
	return scanRow(s.db.QueryRowContext(ctx, `SELECT `+rowCols+` FROM providers WHERE slug = ?`, slug))
}

func (h *PublicHandler) profile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	row, err := h.svc.bySlug(ctx, r.PathValue("slug"))
	if err != nil {
		if !errors.Is(err, response.ErrNotFound) {
			response.Fail(w, r, err)
			return
		}
		h.pub.NotFound(w, r)
		return
	}
	origin := h.pub.SiteOrigin(r)
	switch row.status {
	case StatusDraft, StatusPending:
		h.pub.NotFound(w, r)
		return
	case StatusArchived:
		h.pub.Render(w, r, http.StatusGone, "gone", public.Meta{Title: row.displayName + " — " + h.pub.SiteName(r), Robots: "noindex,follow"}, nil)
		return
	case StatusPaused, StatusSuspended:
		// Kept as 200 + noindex so a temporary pause does not lose the page (§13.2).
		h.pub.Render(w, r, http.StatusOK, "professional", public.Meta{Title: row.displayName + " — " + h.pub.SiteName(r),
			Robots: "noindex,follow", Canonical: origin + "/professionals/" + row.slug}, ProfilePage{Name: row.displayName, Available: false})
		return
	}
	reveal := r.URL.Query().Get("reveal")
	page, v, err := h.svc.publicPage(ctx, row, reveal, r.URL.Path, false)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	// Generated title: name, primary service and area (§9.3); staff may override.
	meta := public.Meta{Title: row.displayName, Canonical: origin + page.Path, Robots: "index,follow"}
	primary := ""
	for _, o := range v.Services {
		if o.Status == "active" && o.ServiceLive {
			primary = o.ServiceName
			break
		}
	}
	switch {
	case primary != "" && v.BaseArea != nil:
		meta.Title += " — " + primary + ", " + v.BaseArea.Name
	case primary != "":
		meta.Title += " — " + primary
	case v.BaseArea != nil:
		meta.Title += " — " + v.BaseArea.Name
	}
	meta.Title += " | " + h.pub.SiteName(r)
	if v.MetaTitle != nil {
		meta.Title = *v.MetaTitle
	}
	meta.Description = summarize(page)
	if v.MetaDescription != nil {
		meta.Description = *v.MetaDescription
	}
	if reveal != "" {
		// Revealed values are never cached or indexed.
		w.Header().Set("Cache-Control", "private, no-store")
		meta.Robots = "noindex,nofollow"
	} else {
		meta.JSONLD = map[string]any{"@context": "https://schema.org", "@graph": []any{
			jsonLD(origin, page, v, h.svc.settings.String(ctx, "default_country")), breadcrumbLD(origin, page.Crumbs)}}
		if p := reqctx.PrincipalFrom(ctx); p == nil || row.ownerUserID == nil || *row.ownerUserID != p.UserID {
			if err := analytics.Record(ctx, h.svc.db, analytics.ProviderProfileView, row.id, map[string]string{"source": "web"}); err != nil {
				h.svc.log.WarnContext(ctx, "analytics", "error", err)
			}
		}
	}
	h.pub.Render(w, r, http.StatusOK, "professional", meta, *page)
}

// preview renders any listing for its owner or staff holding providers.view_any.
func (h *PublicHandler) preview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := reqctx.PrincipalFrom(ctx)
	row, err := h.svc.byPublic(ctx, h.svc.db, r.PathValue("id"), false)
	if err != nil || p == nil {
		h.pub.NotFound(w, r)
		return
	}
	isOwner := row.ownerUserID != nil && *row.ownerUserID == p.UserID
	if !isOwner && !h.svc.authz.Can(ctx, p, "providers.view_any") {
		h.pub.NotFound(w, r)
		return
	}
	page, _, err := h.svc.publicPage(ctx, row, "", r.URL.Path, true)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	h.pub.Render(w, r, http.StatusOK, "professional", public.Meta{Title: row.displayName, Robots: "noindex,nofollow"}, *page)
}

func (h *PublicHandler) badges(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.verification.Types(r.Context())
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	locale := h.svc.settings.String(r.Context(), settings.KeyDefaultLocale)
	h.pub.Render(w, r, http.StatusOK, "badges", public.Meta{Title: h.svc.i18n.T(locale, "provider.badges.title") + " — " + h.pub.SiteName(r),
		Canonical: h.pub.SiteOrigin(r) + "/badges", Robots: "index,follow"}, list)
}

// publicPage builds the visitor view. Only active media, active offerings on live services,
// and contacts allowed by their visibility are included. preview shows every non-hidden
// contact (owner/staff only).
func (s *Service) publicPage(ctx context.Context, r *row, reveal, path string, preview bool) (*ProfilePage, *View, error) {
	v, err := s.view(ctx, s.db, r, false)
	if err != nil {
		return nil, nil, err
	}
	locale := s.settings.String(ctx, settings.KeyDefaultLocale)
	if p := reqctx.PrincipalFrom(ctx); p != nil && s.i18n.Has(p.Locale) {
		locale = p.Locale
	}
	page := &ProfilePage{Name: v.DisplayName, Type: v.Type, Description: v.Description, Badges: v.Badges, Available: true,
		Path: v.Path, preview: preview}
	if v.YearsExperience != nil {
		page.Years = *v.YearsExperience
	}
	if v.ServiceRadiusKm != nil {
		page.Radius = *v.ServiceRadiusKm
	}
	if v.BaseArea != nil {
		page.Area = v.BaseArea.Label
	}
	page.Crumbs = []Crumb{{Label: s.i18n.T(locale, "discovery.crumb_home"), URL: "/"}}
	for _, o := range v.Services {
		if o.Status == "active" && o.ServiceLive {
			page.Crumbs = append(page.Crumbs, Crumb{Label: o.ServiceName, URL: seo.ServicePath(o.ServiceSlug)})
			if v.BaseArea != nil {
				page.Crumbs = append(page.Crumbs, Crumb{Label: v.BaseArea.Name, URL: seo.LandingPath(o.ServiceSlug, v.BaseArea.Slug)})
			}
			break
		}
	}
	page.Crumbs = append(page.Crumbs, Crumb{Label: v.DisplayName})
	for _, c := range v.Coverage {
		page.Coverage = append(page.Coverage, c.Label)
	}
	if m := v.ProfilePhoto; m != nil && m.Status == media.StatusActive {
		page.Photo, page.PhotoSrcSet = pickURL(m, 480), srcset(m)
	}
	if m := v.CoverPhoto; m != nil && m.Status == media.StatusActive {
		page.Cover = pickURL(m, 1600)
	}
	for _, o := range v.Services {
		if o.Status != "active" || !o.ServiceLive {
			continue
		}
		ps := pubService{Name: o.ServiceName, Category: o.CategoryName, Price: s.priceLabel(locale, o)}
		if o.CustomTitle != nil {
			ps.Title = *o.CustomTitle
		}
		if o.Description != nil {
			ps.Description = *o.Description
		}
		page.Services = append(page.Services, ps)
	}
	items, err := s.portfolio(ctx, s.db, r.id, true)
	if err != nil {
		return nil, nil, err
	}
	for _, it := range items {
		img := pubImage{Src: pickURL(it.Media, 960), SrcSet: srcset(it.Media)}
		if it.Caption != nil {
			img.Caption = *it.Caption
		}
		if it.Media.Width != nil && it.Media.Height != nil {
			img.Width, img.Height = *it.Media.Width, *it.Media.Height
		}
		page.Portfolio = append(page.Portfolio, img)
	}
	signedIn := reqctx.PrincipalFrom(ctx) != nil
	allowed := reveal != "" && !preview
	if allowed {
		if ok, _ := s.limiter.Allow("reveal:"+reqctx.ClientIP(ctx), revealLimit); !ok {
			allowed, page.RevealLimited = false, true
		}
	}
	for _, c := range v.Contacts {
		if c.Visibility == VisHidden {
			continue
		}
		pc := pubContact{Channel: c.Channel}
		show := preview || c.Visibility == VisPublic
		switch c.Visibility {
		case VisReveal:
			show = show || (allowed && reveal == c.Channel)
		case VisLoggedIn:
			show = show || (signedIn && allowed && reveal == c.Channel)
			if !signedIn && !preview {
				pc.Login = "/auth/login?next=" + url.QueryEscape(path)
			}
		}
		if show {
			pc.Value, pc.Href, pc.Revealed = displayValue(c), template.URL(s.contactHref(locale, c, v.DisplayName)), true
			if reveal == c.Channel && !preview {
				if err := analytics.Record(ctx, s.db, revealEvent(c.Channel), r.id, map[string]string{"channel": c.Channel, "source": "web"}); err != nil {
					s.log.WarnContext(ctx, "analytics", "error", err)
				}
			}
		} else if pc.Login == "" {
			pc.Reveal = path + "?reveal=" + c.Channel
		}
		page.Contacts = append(page.Contacts, pc)
	}
	return page, v, nil
}

func revealEvent(channel string) string {
	switch channel {
	case ChannelWhatsApp:
		return analytics.ProviderWhatsAppClick
	case ChannelEmail:
		return analytics.ProviderEmailReveal
	}
	return analytics.ProviderPhoneReveal
}

// displayValue formats +2547XXXXXXXX as 0712 345 678 for Kenyan readers.
func displayValue(c Contact) string {
	if c.Channel == ChannelEmail {
		return c.Value
	}
	if strings.HasPrefix(c.Value, "+254") && len(c.Value) == 13 {
		n := "0" + c.Value[4:]
		return n[:4] + " " + n[4:7] + " " + n[7:]
	}
	return c.Value
}

func (s *Service) contactHref(locale string, c Contact, name string) string {
	switch c.Channel {
	case ChannelPhone:
		return "tel:" + c.Value
	case ChannelWhatsApp:
		msg := s.i18n.T(locale, "provider.public.whatsapp_message", "name", name, "site", s.settings.String(context.Background(), settings.KeySiteName))
		return "https://wa.me/" + strings.TrimPrefix(c.Value, "+") + "?text=" + url.QueryEscape(msg)
	case ChannelEmail:
		return "mailto:" + c.Value
	}
	return ""
}

func formatKES(n int) string {
	s := strconv.Itoa(n)
	var b strings.Builder
	for i, ch := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(ch)
	}
	return b.String()
}

func (s *Service) priceLabel(locale string, o Offered) string {
	from, to := "", ""
	if o.PriceFrom != nil {
		from = formatKES(*o.PriceFrom)
	}
	if o.PriceTo != nil {
		to = formatKES(*o.PriceTo)
	}
	switch o.PricingType {
	case PriceFixed:
		return s.i18n.T(locale, "provider.price.fixed", "amount", from)
	case PriceFrom:
		return s.i18n.T(locale, "provider.price.from", "amount", from)
	case PriceRange:
		return s.i18n.T(locale, "provider.price.range", "from", from, "to", to)
	}
	return s.i18n.T(locale, "provider.price.on_quote")
}

func pickURL(m *media.Media, want int) string {
	best, bestW := m.URL, 0
	for w, u := range m.URLs {
		if w <= want && w > bestW {
			best, bestW = u, w
		}
	}
	return best
}

func srcset(m *media.Media) string {
	ws := make([]int, 0, len(m.URLs))
	for w := range m.URLs {
		ws = append(ws, w)
	}
	for i := 1; i < len(ws); i++ {
		for j := i; j > 0 && ws[j] < ws[j-1]; j-- {
			ws[j], ws[j-1] = ws[j-1], ws[j]
		}
	}
	parts := make([]string, 0, len(ws))
	for _, w := range ws {
		parts = append(parts, m.URLs[w]+" "+strconv.Itoa(w)+"w")
	}
	return strings.Join(parts, ", ")
}

func summarize(p *ProfilePage) string {
	d := strings.Join(strings.Fields(p.Description), " ")
	if d == "" {
		names := []string{}
		for _, s := range p.Services {
			names = append(names, s.Name)
		}
		d = p.Name
		if len(names) > 0 {
			d += ": " + strings.Join(names, ", ")
		}
		if p.Area != "" {
			d += " — " + p.Area
		}
	}
	if r := []rune(d); len(r) > 155 {
		d = strings.TrimSpace(string(r[:155])) + "…"
	}
	return d
}

// jsonLD describes the listing as a LocalBusiness. Only publicly visible contacts appear;
// there are no ratings until reviews exist (Phase 6), so none are emitted.
func jsonLD(origin string, p *ProfilePage, v *View, country string) map[string]any {
	ld := map[string]any{"@type": "LocalBusiness", "name": p.Name, "url": origin + p.Path}
	if p.Description != "" {
		ld["description"] = p.Description
	}
	if p.Photo != "" {
		ld["image"] = origin + p.Photo
	}
	if v.BaseArea != nil {
		addr := map[string]any{"@type": "PostalAddress", "addressLocality": v.BaseArea.Name}
		if country != "" {
			addr["addressCountry"] = country
		}
		ld["address"] = addr
	}
	if len(p.Coverage) > 0 {
		ld["areaServed"] = p.Coverage
	}
	for _, c := range v.Contacts {
		if c.Visibility == VisPublic && c.Channel == ChannelPhone {
			ld["telephone"] = c.Value
		}
	}
	return ld
}

// reveal returns one contact for JS clients (same rules and limits as the no-JS path).
func (h *APIHandler) reveal(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in struct {
		Channel string `json:"channel"`
	}
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	row, err := h.svc.bySlug(ctx, r.PathValue("slug"))
	if err != nil || row.status != StatusPublished {
		response.Fail(w, r, response.ErrNotFound)
		return
	}
	if ok, retry := h.svc.limiter.Allow("reveal:"+reqctx.ClientIP(ctx), revealLimit); !ok {
		response.Fail(w, r, response.RateLimited(retry))
		return
	}
	list, err := h.svc.contacts(ctx, h.svc.db, row.id)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	for _, c := range list {
		if c.Channel != in.Channel || c.Visibility == VisHidden {
			continue
		}
		if c.Visibility == VisLoggedIn && reqctx.PrincipalFrom(ctx) == nil {
			response.Fail(w, r, response.ErrUnauthenticated)
			return
		}
		if err := analytics.Record(ctx, h.svc.db, revealEvent(c.Channel), row.id, map[string]string{"channel": c.Channel, "source": "api"}); err != nil {
			h.svc.log.WarnContext(ctx, "analytics", "error", err)
		}
		locale := h.svc.settings.String(ctx, settings.KeyDefaultLocale)
		w.Header().Set("Cache-Control", "private, no-store")
		response.OK(w, map[string]string{"channel": c.Channel, "value": displayValue(c), "href": h.svc.contactHref(locale, c, row.displayName)})
		return
	}
	response.Fail(w, r, response.ErrNotFound)
}
