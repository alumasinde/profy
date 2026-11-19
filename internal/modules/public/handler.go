// Package public renders server-side public pages (plan §6.3). Phase 1 ships the layout,
// metadata plumbing and a placeholder homepage; the marketplace pages arrive in Phase 5.
package public

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/alumasinde/profy/internal/http/response"
	"github.com/alumasinde/profy/internal/i18n"
	"github.com/alumasinde/profy/internal/modules/settings"
	"github.com/alumasinde/profy/internal/shared/reqctx"
)

// Site is brand data for templates — always from settings, never hardcoded (§4).
type Site struct {
	Name        string
	ShortName   string
	Tagline     string
	Description string
	URL         string
}

// Meta is per-page SEO metadata (§9.2).
type Meta struct {
	Title       string
	Description string
	Canonical   string
	Robots      string // "index,follow" or "noindex,follow"
	// JSONLD is structured data marshalled into an application/ld+json block (a data
	// block, not script, so the CSP needs no exception).
	JSONLD any
}

// NavLink is a rendered navigation link from a database-managed menu (§7.2).
type NavLink struct {
	Label    string
	URL      string
	External bool
}

// Page is the data passed to every public template.
type Page struct {
	Locale        string
	Site          Site
	Meta          Meta
	Authenticated bool
	Header        []NavLink
	Footer        []NavLink
	Logo          string
	Favicon       string
	OGImage       string
	Preview       bool
	Data          any
}

// Handler renders public pages.
type Handler struct {
	tpl      map[string]*template.Template
	i18n     *i18n.Catalogue
	settings *settings.Service
	siteURL  string // bootstrap origin from config
	log      *slog.Logger

	// Nav returns active links of a menu ("header", "footer"). Set by the app wiring.
	Nav func(ctx context.Context, menu string) []NavLink
	// Redirect returns a 301 target for a path that has no route, or "".
	Redirect func(ctx context.Context, path string) string
}

// New parses templates: each page is combined with the layouts and partials.
func New(fsys fs.FS, cat *i18n.Catalogue, st *settings.Service, siteURL string, log *slog.Logger) (*Handler, error) {
	h := &Handler{tpl: map[string]*template.Template{}, i18n: cat, settings: st, siteURL: siteURL, log: log}
	pages, err := fs.Glob(fsys, "templates/pages/*.html")
	if err != nil {
		return nil, err
	}
	for _, p := range pages {
		name := strings.TrimSuffix(p[strings.LastIndex(p, "/")+1:], ".html")
		t := template.New(name).Funcs(template.FuncMap{
			// t is rebound per request in render; this placeholder only satisfies parsing.
			"t": func(string, ...any) string { return "" },
		})
		t, err = t.ParseFS(fsys, "templates/layouts/*.html", "templates/partials/*.html", p)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", p, err)
		}
		h.tpl[name] = t
	}
	if h.tpl["home"] == nil || h.tpl["not_found"] == nil {
		return nil, fmt.Errorf("templates: home and not_found pages are required")
	}
	return h, nil
}

// Register mounts public routes.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", h.home)
	mux.HandleFunc("GET /theme.css", h.theme)
	mux.HandleFunc("GET /robots.txt", h.robots)
}

// SiteOrigin returns the configured canonical origin (settings first, then bootstrap config).
func (h *Handler) SiteOrigin(r *http.Request) string {
	if u := strings.TrimRight(h.settings.String(r.Context(), settings.KeySiteURL), "/"); u != "" {
		return u
	}
	return h.siteURL
}

// SiteName returns the configured site name.
func (h *Handler) SiteName(r *http.Request) string {
	return h.settings.String(r.Context(), settings.KeySiteName)
}

func (h *Handler) site(r *http.Request) Site {
	ctx := r.Context()
	return Site{
		Name:        h.settings.String(ctx, settings.KeySiteName),
		ShortName:   h.settings.String(ctx, settings.KeySiteShortName),
		Tagline:     h.settings.String(ctx, settings.KeySiteTagline),
		Description: h.settings.String(ctx, settings.KeySiteDescription),
		URL:         h.SiteOrigin(r),
	}
}

func (h *Handler) locale(r *http.Request) string {
	if p := reqctx.PrincipalFrom(r.Context()); p != nil && h.i18n.Has(p.Locale) {
		return p.Locale
	}
	if l := h.settings.String(r.Context(), settings.KeyDefaultLocale); h.i18n.Has(l) {
		return l
	}
	return i18n.DefaultLocale
}

// Render writes a page with status. Rendering is buffered so a template error never
// produces a half-written 200 page.
func (h *Handler) Render(w http.ResponseWriter, r *http.Request, status int, name string, meta Meta, data any) {
	t := h.tpl[name]
	if t == nil {
		response.Fail(w, r, fmt.Errorf("template %q not found", name))
		return
	}
	locale := h.locale(r)
	t, err := t.Clone()
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	t.Funcs(template.FuncMap{"t": func(key string, args ...any) string { return h.i18n.T(locale, key, args...) }})
	ctx := r.Context()
	page := Page{Locale: locale, Site: h.site(r), Meta: meta, Authenticated: reqctx.PrincipalFrom(ctx) != nil, Data: data,
		Logo: h.settings.String(ctx, "logo_light"), Favicon: h.settings.String(ctx, "favicon"),
		OGImage: h.settings.String(ctx, "default_og_image")}
	if pv, ok := data.(interface{ IsPreview() bool }); ok {
		page.Preview = pv.IsPreview()
	}
	if h.Nav != nil {
		page.Header, page.Footer = h.Nav(ctx, "header"), h.Nav(ctx, "footer")
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "base", page); err != nil {
		h.log.ErrorContext(r.Context(), "template render failed", "template", name, "error", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if meta.Robots != "" && strings.HasPrefix(meta.Robots, "noindex") {
		w.Header().Set("X-Robots-Tag", meta.Robots)
	}
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

func (h *Handler) home(w http.ResponseWriter, r *http.Request) {
	site := h.site(r)
	title := site.Name
	if site.Tagline != "" {
		title = site.Name + " — " + site.Tagline
	}
	h.Render(w, r, http.StatusOK, "home", Meta{
		Title: title, Description: site.Description, Canonical: site.URL + "/", Robots: "index,follow",
	}, nil)
}

// NotFound follows a slug redirect when one exists, otherwise renders the public 404 page.
func (h *Handler) NotFound(w http.ResponseWriter, r *http.Request) {
	if h.Redirect != nil && r.Method == http.MethodGet {
		if to := h.Redirect(r.Context(), r.URL.Path); to != "" {
			if r.URL.RawQuery != "" {
				to += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, to, http.StatusMovedPermanently)
			return
		}
	}
	locale := h.locale(r)
	h.Render(w, r, http.StatusNotFound, "not_found", Meta{
		Title: h.i18n.T(locale, "public.not_found.title") + " — " + h.site(r).Name, Robots: "noindex,follow",
	}, nil)
}

// maintenanceExempt are paths that keep working during maintenance so staff can sign in,
// administer the site and turn maintenance off again.
var maintenanceExempt = []string{"/auth", "/account", "/provider", "/admin", "/api/", "/_app/", "/static/",
	"/healthz", "/readyz", "/theme.css", "/robots.txt", "/preview/", "/media/"}

// Maintenance serves a 503 page for public pages while maintenance mode is on (§9.4).
func (h *Handler) Maintenance(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.settings.Bool(r.Context(), settings.KeyMaintenance) {
			next.ServeHTTP(w, r)
			return
		}
		for _, p := range maintenanceExempt {
			if r.URL.Path == p || (strings.HasSuffix(p, "/") && strings.HasPrefix(r.URL.Path, p)) || strings.HasPrefix(r.URL.Path, p+"/") {
				next.ServeHTTP(w, r)
				return
			}
		}
		w.Header().Set("Retry-After", "3600")
		locale := h.locale(r)
		h.Render(w, r, http.StatusServiceUnavailable, "maintenance", Meta{
			Title: h.i18n.T(locale, "public.maintenance.title") + " — " + h.site(r).Name, Robots: "noindex,nofollow",
		}, nil)
	})
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// theme serves brand colors as CSS variables; components never hardcode brand colors (§7.1).
// Invalid values fall back to defaults. Contrast validation is enforced when admins save
// colors (Phase 3).
func (h *Handler) theme(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	color := func(key, def string) string {
		if v := h.settings.String(ctx, key); hexColor.MatchString(v) {
			return v
		}
		return def
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	fmt.Fprintf(w, ":root{--color-primary:%s;--color-secondary:%s;--color-accent:%s}\n",
		color(settings.KeyPrimaryColor, "#0f766e"), color(settings.KeySecondaryColor, "#1e293b"), color(settings.KeyAccentColor, "#b45309"))
}

// robots keeps private and application areas out of search indexes (§9.4).
// The sitemap line is added in Phase 5 when sitemaps exist.
func (h *Handler) robots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, "User-agent: *\nDisallow: /api/\nDisallow: /auth/\nDisallow: /account/\nDisallow: /provider/\nDisallow: /admin/\n")
}
