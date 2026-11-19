package discovery

import (
	"context"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/alumasinde/profy/internal/http/response"
	"github.com/alumasinde/profy/internal/modules/analytics"
	"github.com/alumasinde/profy/internal/modules/pages"
	"github.com/alumasinde/profy/internal/modules/providers"
	"github.com/alumasinde/profy/internal/modules/public"
	"github.com/alumasinde/profy/internal/modules/seo"
	"github.com/alumasinde/profy/internal/modules/settings"
	"github.com/alumasinde/profy/internal/shared/reqctx"
	"github.com/alumasinde/profy/internal/shared/text"
)

// PublicHandler serves the server-rendered marketplace pages (§6.3). Every page works
// without JavaScript and carries a canonical URL, unique metadata, breadcrumbs and accurate
// structured data.
type PublicHandler struct {
	svc *Service
	pub *public.Handler
}

// NewPublicHandler builds the handler and installs the homepage and footer hooks.
func NewPublicHandler(svc *Service, pub *public.Handler) *PublicHandler {
	h := &PublicHandler{svc: svc, pub: pub}
	pub.Home = h.home
	pub.FooterLinks = h.footer
	return h
}

// Register mounts routes.
func (h *PublicHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /search", h.search)
	mux.HandleFunc("GET /categories", h.categories)
	mux.HandleFunc("GET /categories/{slug}", h.category)
	mux.HandleFunc("GET /services", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/categories", http.StatusMovedPermanently)
	})
	mux.HandleFunc("GET /services/{slug}", h.service)
	mux.HandleFunc("GET /services/{slug}/{location}", h.landing)
	mux.HandleFunc("GET /locations/{slug}", h.location)
	mux.HandleFunc("POST /requests", h.demandForm)
	mux.HandleFunc("GET /sitemap.xml", h.sitemapIndex)
	mux.HandleFunc("GET /sitemaps/{file}", h.sitemapFile)
}

// ---- Shared page data ------------------------------------------------------------------

type link struct {
	Label string
	URL   string
	Count int
}

type linkGroup struct {
	Title string
	Links []link
}

type crumb struct {
	Label string
	URL   string
}

// SearchForm is the service + location form shown on most pages.
type SearchForm struct {
	Q        string
	Where    string
	Radius   int
	Lat, Lng string
	Places   []string // datalist suggestions (works without JavaScript)
}

type demandForm struct {
	ServiceText  string
	LocationText string
	ServiceSlug  string
	LocationSlug string
	Return       string
}

// ResultsPage is shared by search, landing, service and location pages.
type ResultsPage struct {
	H1         string
	Lead       string
	Intro      template.HTML
	Crumbs     []crumb
	Form       SearchForm
	Cards      []providers.Card
	Total      int
	TotalText  string
	Page       int
	Pages      int
	Prev, Next string
	Alternates []link
	Groups     []linkGroup
	Zero       bool
	ZeroTitle  string
	Demand     *demandForm
	Requested  bool
	DemandErr  bool
}

func (h *PublicHandler) locale(r *http.Request) string {
	if p := reqctx.PrincipalFrom(r.Context()); p != nil && h.svc.i18n.Has(p.Locale) {
		return p.Locale
	}
	return h.svc.settings.String(r.Context(), settings.KeyDefaultLocale)
}

func (h *PublicHandler) t(r *http.Request, key string, args ...any) string {
	return h.svc.i18n.T(h.locale(r), key, args...)
}

func (h *PublicHandler) fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, response.ErrNotFound) {
		h.pub.NotFound(w, r)
		return
	}
	response.Fail(w, r, err)
}

func (h *PublicHandler) places(snap *Snapshot) []string {
	var out []string
	for _, c := range snap.Counties {
		if !c.Selectable {
			continue
		}
		out = append(out, c.Name)
		for _, t := range c.Children {
			if t.Selectable {
				out = append(out, t.Name)
				for _, a := range t.Children {
					if a.Selectable && len(out) < 400 {
						out = append(out, a.Name+", "+t.Name)
					}
				}
			}
		}
	}
	return out
}

func pageParam(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if n < 1 {
		n = 1
	}
	return n
}

func withPage(path string, q url.Values, page int) string {
	v := url.Values{}
	for k, vs := range q {
		if k != "page" {
			v[k] = vs
		}
	}
	if page > 1 {
		v.Set("page", strconv.Itoa(page))
	}
	if e := v.Encode(); e != "" {
		return path + "?" + e
	}
	return path
}

func (h *PublicHandler) paging(p *ResultsPage, r *http.Request, res *Result) {
	p.Total, p.Page, p.Pages = res.Total, res.Page, res.Pages
	p.TotalText = h.t(r, "discovery.results_count", "n", res.Total)
	if res.Page > 1 {
		p.Prev = withPage(r.URL.Path, r.URL.Query(), res.Page-1)
	}
	if res.Page < res.Pages {
		p.Next = withPage(r.URL.Path, r.URL.Query(), res.Page+1)
	}
}

func (h *PublicHandler) cards(ctx context.Context, r *http.Request, hits []Hit) ([]providers.Card, error) {
	in := make([]providers.CardInput, 0, len(hits))
	for _, x := range hits {
		in = append(in, providers.CardInput{ProviderID: x.ProviderID, ServiceID: x.ServiceID, Serves: x.Serves})
	}
	return h.svc.providers.Cards(ctx, h.locale(r), in)
}

// ---- Structured data -------------------------------------------------------------------

func breadcrumbLD(origin string, crumbs []crumb) map[string]any {
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

func itemListLD(origin string, cards []providers.Card) map[string]any {
	items := make([]map[string]any, 0, len(cards))
	for i, c := range cards {
		items = append(items, map[string]any{"@type": "ListItem", "position": i + 1, "url": origin + c.Path, "name": c.Name})
	}
	return map[string]any{"@type": "ItemList", "itemListElement": items}
}

func graph(nodes ...map[string]any) map[string]any {
	var list []map[string]any
	for _, n := range nodes {
		if n != nil {
			list = append(list, n)
		}
	}
	return map[string]any{"@context": "https://schema.org", "@graph": list}
}

func (h *PublicHandler) crumbsFor(r *http.Request, rest ...crumb) []crumb {
	return append([]crumb{{Label: h.t(r, "discovery.crumb_home"), URL: "/"}}, rest...)
}

func robots(index bool) string {
	if index {
		return "index,follow"
	}
	return "noindex,follow"
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return strings.TrimSpace(string(r[:n-1])) + "…"
	}
	return s
}

// ---- Homepage (§7.3) -------------------------------------------------------------------

// HomePage is the homepage data.
type HomePage struct {
	Form       SearchForm
	Categories []link
	Services   []link
	Near       []providers.Card
	NearTitle  string
	NearLink   string
	NearPlaces []link
	NearSlug   string
	Featured   []providers.Card
	HasSupply  bool
}

func (h *PublicHandler) home(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	snap, err := h.svc.Snapshot(ctx)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	counts, err := h.svc.serviceCounts(ctx)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	d := HomePage{Form: SearchForm{Places: h.places(snap)}}
	for _, c := range snap.Categories {
		n := 0
		for _, sv := range c.AllServices() {
			n += counts[sv.ID]
		}
		d.Categories = append(d.Categories, link{Label: c.Name, URL: c.Path(), Count: n})
	}
	// Once there is supply, only categories with professionals are promoted on the homepage.
	var withSupply []link
	for _, c := range d.Categories {
		if c.Count > 0 {
			withSupply = append(withSupply, c)
		}
	}
	if len(withSupply) > 0 {
		d.Categories = withSupply
	}
	var svcs []link
	for _, sv := range snap.Services {
		if counts[sv.ID] > 0 {
			svcs = append(svcs, link{Label: sv.Name, URL: sv.Path(), Count: counts[sv.ID]})
		}
	}
	sort.SliceStable(svcs, func(i, j int) bool { return svcs[i].Count > svcs[j].Count })
	if len(svcs) > 8 {
		svcs = svcs[:8]
	}
	d.Services = svcs
	d.HasSupply = len(svcs) > 0

	// Providers near a selected place (manual choice; never requires GPS) or, without a
	// choice, recently published listings — real inventory only.
	if slug := r.URL.Query().Get("near"); slug != "" {
		if l := snap.Location(slug); l != nil && l.Selectable {
			d.NearSlug = l.Slug
			res, err := h.svc.Search(ctx, snap, Query{Location: l, PerPage: 6})
			if err != nil {
				h.fail(w, r, err)
				return
			}
			if d.Near, err = h.cards(ctx, r, res.Hits); err != nil {
				h.fail(w, r, err)
				return
			}
			d.NearTitle = h.t(r, "discovery.home.near_title", "place", l.Name)
			d.NearLink = l.Path()
		}
	}
	if d.NearSlug == "" {
		ids, err := h.svc.recentProviders(ctx, 6)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		in := make([]providers.CardInput, 0, len(ids))
		for _, id := range ids {
			in = append(in, providers.CardInput{ProviderID: id})
		}
		if d.Near, err = h.svc.providers.Cards(ctx, h.locale(r), in); err != nil {
			h.fail(w, r, err)
			return
		}
		d.NearTitle = h.t(r, "discovery.home.recent_title")
	}
	for _, c := range snap.Counties {
		if !c.Selectable {
			continue
		}
		for _, t := range c.Children {
			if t.Selectable {
				d.NearPlaces = append(d.NearPlaces, link{Label: t.Name, URL: t.Slug})
			}
		}
	}
	feat, err := h.svc.featuredProviders(ctx, 6)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if len(feat) > 0 {
		in := make([]providers.CardInput, 0, len(feat))
		for _, id := range feat {
			in = append(in, providers.CardInput{ProviderID: id})
		}
		if d.Featured, err = h.svc.providers.Cards(ctx, h.locale(r), in); err != nil {
			h.fail(w, r, err)
			return
		}
	}
	origin := h.pub.SiteOrigin(r)
	site := h.pub.SiteName(r)
	tagline := h.svc.settings.String(ctx, settings.KeySiteTagline)
	title := site
	if tagline != "" {
		title = site + " — " + tagline
	}
	desc := h.svc.settings.String(ctx, settings.KeySiteDescription)
	org := map[string]any{"@type": "Organization", "@id": origin + "/#org", "name": site, "url": origin + "/"}
	if logo := h.svc.settings.String(ctx, "logo_light"); logo != "" {
		if strings.HasPrefix(logo, "/") {
			logo = origin + logo
		}
		org["logo"] = logo
	}
	webSite := map[string]any{"@type": "WebSite", "@id": origin + "/#website", "name": site, "url": origin + "/",
		"publisher": map[string]any{"@id": origin + "/#org"},
		"potentialAction": map[string]any{"@type": "SearchAction", "target": origin + "/search?q={search_term_string}",
			"query-input": "required name=search_term_string"}}
	robotsVal := "index,follow"
	if d.NearSlug != "" {
		robotsVal = "noindex,follow" // ?near= variants are filters of the homepage
	}
	h.pub.Render(w, r, http.StatusOK, "home", public.Meta{Title: title, Description: desc, Canonical: origin + "/",
		Robots: robotsVal, JSONLD: graph(org, webSite)}, d)
}

func (h *PublicHandler) footer(ctx context.Context) (cats, places []public.NavLink) {
	snap, err := h.svc.Snapshot(ctx)
	if err != nil {
		return nil, nil
	}
	for _, c := range snap.Categories {
		if len(cats) < 8 {
			cats = append(cats, public.NavLink{Label: c.Name, URL: c.Path()})
		}
	}
	for _, c := range snap.Counties {
		if c.Selectable && c.Indexable && len(places) < 8 {
			places = append(places, public.NavLink{Label: c.Name, URL: c.Path()})
		}
		for _, t := range c.Children {
			if t.Selectable && t.Indexable && len(places) < 8 {
				places = append(places, public.NavLink{Label: t.Name, URL: t.Path()})
			}
		}
	}
	return cats, places
}

// ---- Search (§10.4) --------------------------------------------------------------------

func (h *PublicHandler) search(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	snap, err := h.svc.Snapshot(ctx)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	in := parseSearch(r)
	run, err := h.svc.resolve(snap, in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	res, err := h.svc.Search(ctx, snap, run.query)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.svc.logSearch(ctx, run, res)
	p := ResultsPage{Form: SearchForm{Q: in.Q, Where: in.Where, Radius: in.Radius, Lat: in.Lat, Lng: in.Lng, Places: h.places(snap)},
		Requested: r.URL.Query().Get("requested") == "1", DemandErr: r.URL.Query().Get("request_error") == "1"}
	switch {
	case run.service != nil && run.query.Location != nil:
		p.H1 = h.t(r, "discovery.landing_h1", "service", run.service.Name, "place", run.query.Location.Name)
	case run.service != nil:
		p.H1 = run.service.Name
	case in.Q != "" && run.query.Location != nil:
		p.H1 = h.t(r, "discovery.search_h1_where", "q", in.Q, "place", run.query.Location.Name)
	case in.Q != "":
		p.H1 = h.t(r, "discovery.search_h1", "q", in.Q)
	case run.query.Location != nil:
		p.H1 = h.t(r, "discovery.location_h1", "place", run.query.Location.Name)
	default:
		p.H1 = h.t(r, "discovery.search_title")
	}
	for _, alt := range run.alternates {
		u := url.Values{"q": {in.Q}, "where": {alt.Label}}
		p.Alternates = append(p.Alternates, link{Label: alt.Label, URL: "/search?" + u.Encode()})
	}
	if in.Where != "" && run.query.Location == nil && run.query.Lat == nil {
		p.Lead = h.t(r, "discovery.place_unknown", "where", in.Where)
	}
	if run.service != nil && run.query.Location != nil {
		p.Groups = append(p.Groups, linkGroup{Title: h.t(r, "discovery.see_page"),
			Links: []link{{Label: p.H1, URL: seo.LandingPath(run.service.Slug, run.query.Location.Slug)}}})
	}
	if err := h.fillResults(ctx, r, snap, &p, res, run.service, run.query.Location, in.Q); err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-cache")
	h.pub.Render(w, r, http.StatusOK, "results", public.Meta{Title: p.H1 + " | " + h.pub.SiteName(r),
		Robots: "noindex,follow", Canonical: h.pub.SiteOrigin(r) + "/search"}, p)
}

// fillResults adds cards, paging and the zero-result experience (§3.2).
func (h *PublicHandler) fillResults(ctx context.Context, r *http.Request, snap *Snapshot, p *ResultsPage, res *Result,
	svc *CatService, loc *Location, q string) error {
	cards, err := h.cards(ctx, r, res.Hits)
	if err != nil {
		return err
	}
	p.Cards = cards
	h.paging(p, r, res)
	if res.Total > 0 {
		return nil
	}
	p.Zero = true
	p.ZeroTitle = h.t(r, "discovery.zero_title")
	alts, err := h.svc.alternatives(ctx, snap, res, svc, loc, q)
	if err != nil {
		return err
	}
	for _, g := range alts {
		g.Title = h.t(r, g.Title)
		p.Groups = append(p.Groups, g)
	}
	d := &demandForm{Return: r.URL.RequestURI()}
	if svc != nil {
		d.ServiceSlug, d.ServiceText = svc.Slug, svc.Name
	} else {
		d.ServiceText = q
	}
	if loc != nil {
		d.LocationSlug, d.LocationText = loc.Slug, loc.Label
	}
	p.Demand = d
	return nil
}

// ---- Categories ------------------------------------------------------------------------

// CategoriesPage lists the catalogue.
type CategoriesPage struct {
	H1     string
	Crumbs []crumb
	Groups []categoryGroup
	Form   SearchForm
}

type categoryGroup struct {
	Name, URL   string
	Description string
	Count       int
	Subgroups   []linkGroup
	Services    []link
}

func (h *PublicHandler) categories(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	snap, err := h.svc.Snapshot(ctx)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	counts, err := h.svc.serviceCounts(ctx)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	p := CategoriesPage{H1: h.t(r, "discovery.categories_h1"), Form: SearchForm{Places: h.places(snap)}}
	p.Crumbs = h.crumbsFor(r, crumb{Label: p.H1})
	for _, c := range snap.Categories {
		g := h.categoryGroup(c, counts)
		p.Groups = append(p.Groups, g)
	}
	origin := h.pub.SiteOrigin(r)
	h.pub.Render(w, r, http.StatusOK, "categories", public.Meta{Title: p.H1 + " | " + h.pub.SiteName(r),
		Description: h.t(r, "discovery.categories_desc", "site", h.pub.SiteName(r)), Canonical: origin + "/categories",
		Robots: robots(len(p.Groups) > 0), JSONLD: graph(breadcrumbLD(origin, p.Crumbs))}, p)
}

func (h *PublicHandler) categoryGroup(c *Category, counts map[uint64]int) categoryGroup {
	g := categoryGroup{Name: c.Name, URL: c.Path(), Description: c.Description}
	for _, sv := range c.Services {
		g.Services = append(g.Services, link{Label: sv.Name, URL: sv.Path(), Count: counts[sv.ID]})
		g.Count += counts[sv.ID]
	}
	for _, ch := range c.Children {
		sg := linkGroup{Title: ch.Name}
		for _, sv := range ch.Services {
			sg.Links = append(sg.Links, link{Label: sv.Name, URL: sv.Path(), Count: counts[sv.ID]})
			g.Count += counts[sv.ID]
		}
		g.Subgroups = append(g.Subgroups, sg)
	}
	return g
}

func (h *PublicHandler) category(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	snap, err := h.svc.Snapshot(ctx)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	c := snap.Category(r.PathValue("slug"))
	if c == nil {
		h.pub.NotFound(w, r)
		return
	}
	counts, err := h.svc.serviceCounts(ctx)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	g := h.categoryGroup(c, counts)
	p := CategoriesPage{H1: c.Name, Groups: []categoryGroup{g}, Form: SearchForm{Places: h.places(snap)}}
	trail := []crumb{{Label: h.t(r, "discovery.categories_h1"), URL: "/categories"}}
	if c.Parent != nil {
		trail = append(trail, crumb{Label: c.Parent.Name, URL: c.Parent.Path()})
	}
	p.Crumbs = h.crumbsFor(r, append(trail, crumb{Label: c.Name})...)
	origin := h.pub.SiteOrigin(r)
	meta := public.Meta{Title: h.t(r, "discovery.category_title", "category", c.Name, "site", h.pub.SiteName(r)),
		Description: clip(c.Description, 155), Canonical: origin + c.Path(), Robots: robots(g.Count > 0),
		JSONLD: graph(breadcrumbLD(origin, p.Crumbs))}
	if meta.Description == "" {
		meta.Description = h.t(r, "discovery.category_desc", "category", c.Name, "site", h.pub.SiteName(r))
	}
	if c.MetaTitle != "" {
		meta.Title = c.MetaTitle
	}
	if c.MetaDesc != "" {
		meta.Description = c.MetaDesc
	}
	h.pub.Render(w, r, http.StatusOK, "categories", meta, p)
}

// ---- Service page ----------------------------------------------------------------------

func (h *PublicHandler) service(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	snap, err := h.svc.Snapshot(ctx)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	sv := snap.Service(r.PathValue("slug"))
	if sv == nil {
		h.pub.NotFound(w, r) // follows slug redirects
		return
	}
	res, err := h.svc.Search(ctx, snap, Query{Service: sv, Page: pageParam(r)})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	p := ResultsPage{H1: h.t(r, "discovery.service_h1", "service", sv.Name), Lead: sv.Description,
		Form: SearchForm{Q: sv.Name, Places: h.places(snap)}}
	p.Crumbs = h.crumbsFor(r, crumb{Label: sv.Category.rootName(), URL: sv.Category.root().Path()}, crumb{Label: sv.Name})
	if err := h.fillResults(ctx, r, snap, &p, res, sv, nil, ""); err != nil {
		h.fail(w, r, err)
		return
	}
	places, err := h.svc.landingLinks(ctx, snap, sv, nil, 24)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if len(places) > 0 {
		p.Groups = append([]linkGroup{{Title: h.t(r, "discovery.by_location", "service", sv.Name), Links: places}}, p.Groups...)
	}
	p.Groups = append(p.Groups, h.relatedServices(r, snap, sv, nil))
	origin := h.pub.SiteOrigin(r)
	meta := public.Meta{Title: h.t(r, "discovery.service_title", "service", sv.Name, "site", h.pub.SiteName(r)),
		Canonical: origin + sv.Path(), Robots: robots(res.Total > 0 && res.Page == 1)}
	meta.Description = clip(sv.Description, 155)
	if meta.Description == "" {
		meta.Description = h.t(r, "discovery.service_desc", "service", sv.Name, "n", res.Total)
	}
	if sv.MetaTitle != "" {
		meta.Title = sv.MetaTitle
	}
	if sv.MetaDesc != "" {
		meta.Description = sv.MetaDesc
	}
	if res.Page > 1 {
		meta.Canonical = origin + withPage(sv.Path(), nil, res.Page)
	}
	meta.JSONLD = graph(breadcrumbLD(origin, p.Crumbs), listIf(origin, p.Cards))
	h.pub.Render(w, r, http.StatusOK, "results", meta, p)
}

func listIf(origin string, cards []providers.Card) map[string]any {
	if len(cards) == 0 {
		return nil
	}
	return itemListLD(origin, cards)
}

func (c *Category) root() *Category {
	if c.Parent != nil {
		return c.Parent
	}
	return c
}

func (c *Category) rootName() string { return c.root().Name }

func (h *PublicHandler) relatedServices(r *http.Request, snap *Snapshot, sv *CatService, loc *Location) linkGroup {
	g := linkGroup{Title: h.t(r, "discovery.related_services")}
	for _, o := range sv.Category.root().AllServices() {
		if o == sv {
			continue
		}
		u := o.Path()
		if loc != nil {
			u = seo.LandingPath(o.Slug, loc.Slug)
		}
		g.Links = append(g.Links, link{Label: o.Name, URL: u})
	}
	if len(g.Links) > 12 {
		g.Links = g.Links[:12]
	}
	return g
}

// ---- Landing page: service × location (§9.6) -------------------------------------------

func (h *PublicHandler) landing(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	snap, err := h.svc.Snapshot(ctx)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	sv := snap.Service(r.PathValue("slug"))
	locSlug := r.PathValue("location")
	if sv == nil {
		// A renamed service keeps its landing pages: follow the service slug redirect.
		if h.pub.Redirect != nil {
			if to := h.pub.Redirect(ctx, seo.ServicePath(r.PathValue("slug"))); to != "" {
				http.Redirect(w, r, to+"/"+locSlug, http.StatusMovedPermanently)
				return
			}
		}
		h.pub.NotFound(w, r)
		return
	}
	loc := snap.Location(locSlug)
	if loc == nil {
		h.pub.NotFound(w, r)
		return
	}
	if !loc.Active {
		if to := snap.Resolved(loc); to != nil && to != loc && to.Selectable {
			http.Redirect(w, r, seo.LandingPath(sv.Slug, to.Slug), http.StatusMovedPermanently)
			return
		}
		h.pub.Render(w, r, http.StatusGone, "gone", public.Meta{Title: loc.Name, Robots: "noindex,follow"}, nil)
		return
	}
	if !loc.Selectable {
		h.pub.NotFound(w, r)
		return
	}
	res, err := h.svc.Search(ctx, snap, Query{Service: sv, Location: loc, Page: pageParam(r)})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	state, err := h.svc.landing(ctx, h.svc.db, sv.ID, loc)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	index := h.svc.landingIndexable(ctx, state, res.Total) && res.Page == 1
	p := ResultsPage{H1: h.t(r, "discovery.landing_h1", "service", sv.Name, "place", loc.Name),
		Form:      SearchForm{Q: sv.Name, Where: loc.Name, Places: h.places(snap)},
		Requested: r.URL.Query().Get("requested") == "1", DemandErr: r.URL.Query().Get("request_error") == "1"}
	if res.Total > 0 {
		p.Lead = h.t(r, "discovery.landing_lead", "service", sv.Name, "place", loc.Label, "n", res.Total)
	}
	if state != nil && state.Intro != "" {
		p.Intro = pages.RenderMarkdown(state.Intro)
	}
	trail := []crumb{{Label: sv.Name, URL: sv.Path()}}
	for _, a := range ancestors(loc) {
		trail = append(trail, crumb{Label: a.Name, URL: seo.LandingPath(sv.Slug, a.Slug)})
	}
	p.Crumbs = h.crumbsFor(r, append(trail, crumb{Label: loc.Name})...)
	if err := h.fillResults(ctx, r, snap, &p, res, sv, loc, ""); err != nil {
		h.fail(w, r, err)
		return
	}
	if !p.Zero {
		near, err := h.svc.landingLinks(ctx, snap, sv, loc, 12)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		if len(near) > 0 {
			p.Groups = append(p.Groups, linkGroup{Title: h.t(r, "discovery.nearby"), Links: near})
		}
		p.Groups = append(p.Groups, h.relatedServices(r, snap, sv, loc))
	}
	origin := h.pub.SiteOrigin(r)
	path := seo.LandingPath(sv.Slug, loc.Slug)
	meta := public.Meta{Title: h.t(r, "discovery.landing_title", "service", sv.Name, "place", loc.Label, "site", h.pub.SiteName(r)),
		Description: h.t(r, "discovery.landing_desc", "service", sv.Name, "place", loc.Label, "n", res.Total),
		Canonical:   origin + path, Robots: robots(index)}
	if state != nil {
		if state.MetaTitle != "" {
			meta.Title = state.MetaTitle
		}
		if state.MetaDesc != "" {
			meta.Description = state.MetaDesc
		}
	}
	if res.Page > 1 {
		meta.Canonical = origin + withPage(path, nil, res.Page)
	}
	meta.JSONLD = graph(breadcrumbLD(origin, p.Crumbs), listIf(origin, p.Cards))
	h.pub.Render(w, r, http.StatusOK, "results", meta, p)
}

func ancestors(l *Location) []*Location {
	var out []*Location
	for p := l.Parent; p != nil; p = p.Parent {
		out = append([]*Location{p}, out...)
	}
	return out
}

// ---- Location page ---------------------------------------------------------------------

func (h *PublicHandler) location(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	snap, err := h.svc.Snapshot(ctx)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	loc := snap.Location(r.PathValue("slug"))
	if loc == nil || !loc.Active || !loc.Selectable {
		h.pub.NotFound(w, r) // merged locations redirect through slug_redirects
		return
	}
	res, err := h.svc.Search(ctx, snap, Query{Location: loc, Page: pageParam(r)})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	p := ResultsPage{H1: h.t(r, "discovery.location_h1", "place", loc.Name), Form: SearchForm{Where: loc.Name, Places: h.places(snap)}}
	if loc.Parent != nil {
		p.Lead = loc.Label
	}
	var trail []crumb
	for _, a := range ancestors(loc) {
		trail = append(trail, crumb{Label: a.Name, URL: a.Path()})
	}
	p.Crumbs = h.crumbsFor(r, append(trail, crumb{Label: loc.Name})...)
	if err := h.fillResults(ctx, r, snap, &p, res, nil, loc, ""); err != nil {
		h.fail(w, r, err)
		return
	}
	svcLinks, err := h.svc.servicesIn(ctx, snap, res, loc)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if len(svcLinks) > 0 {
		p.Groups = append([]linkGroup{{Title: h.t(r, "discovery.services_in", "place", loc.Name), Links: svcLinks}}, p.Groups...)
	}
	var kids []link
	for _, ch := range loc.Children {
		if ch.Selectable {
			kids = append(kids, link{Label: ch.Name, URL: ch.Path()})
		}
	}
	if len(kids) > 0 {
		p.Groups = append(p.Groups, linkGroup{Title: h.t(r, "discovery.places_in", "place", loc.Name), Links: kids})
	}
	origin := h.pub.SiteOrigin(r)
	meta := public.Meta{Title: h.t(r, "discovery.location_title", "place", loc.Label, "site", h.pub.SiteName(r)),
		Description: h.t(r, "discovery.location_desc", "place", loc.Label, "n", res.Total),
		Canonical:   origin + loc.Path(), Robots: robots(loc.Indexable && res.Total > 0 && res.Page == 1)}
	if loc.MetaTitle != "" {
		meta.Title = loc.MetaTitle
	}
	if loc.MetaDesc != "" {
		meta.Description = loc.MetaDesc
	}
	if res.Page > 1 {
		meta.Canonical = origin + withPage(loc.Path(), nil, res.Page)
	}
	meta.JSONLD = graph(breadcrumbLD(origin, p.Crumbs), listIf(origin, p.Cards))
	h.pub.Render(w, r, http.StatusOK, "results", meta, p)
}

// ---- Search input parsing --------------------------------------------------------------

type searchInput struct {
	Q, Where, Location string
	Lat, Lng           string
	Radius, Page       int
	Limit              int
}

func parseSearch(r *http.Request) searchInput {
	v := r.URL.Query()
	in := searchInput{Q: strings.TrimSpace(v.Get("q")), Where: strings.TrimSpace(v.Get("where")),
		Location: v.Get("location"), Lat: v.Get("lat"), Lng: v.Get("lng")}
	if s := v.Get("service"); s != "" && in.Q == "" {
		in.Q = s
	}
	in.Radius, _ = strconv.Atoi(v.Get("radius_km"))
	in.Page, _ = strconv.Atoi(v.Get("page"))
	in.Limit, _ = strconv.Atoi(v.Get("limit"))
	if len([]rune(in.Q)) > 100 {
		in.Q = string([]rune(in.Q)[:100])
	}
	if len([]rune(in.Where)) > 100 {
		in.Where = string([]rune(in.Where)[:100])
	}
	return in
}

type resolvedSearch struct {
	query      Query
	service    *CatService // when the text resolves to exactly one service
	alternates []*Location
	q          string
}

// resolve turns raw input into a Query: location slug or free text (alias-aware), optional
// one-time coordinates (validated, never stored), a bounded radius and page.
func (s *Service) resolve(snap *Snapshot, in searchInput) (*resolvedSearch, error) {
	out := &resolvedSearch{q: in.Q, query: Query{Text: in.Q, Page: in.Page, PerPage: in.Limit}}
	if in.Radius < 0 {
		in.Radius = 0
	}
	out.query.RadiusKm = min(in.Radius, MaxRadiusKm)
	if sv := snap.Service(text.Slugify(in.Q)); sv != nil && in.Q != "" {
		out.service = sv
	} else if in.Q != "" {
		m := snap.MatchServices(in.Q)
		var exact []uint64
		for id, rel := range m {
			if rel >= relSynonym {
				exact = append(exact, id)
			}
		}
		if len(exact) == 1 {
			out.service = snap.ServiceByID(exact[0])
		}
	}
	if in.Location != "" {
		if l := snap.Resolved(snap.Location(in.Location)); l != nil && l.Selectable {
			out.query.Location = l
		}
	}
	if out.query.Location == nil && in.Where != "" {
		where := in.Where
		if i := strings.Index(where, ","); i > 0 {
			// "Parklands, Westlands" from the suggestion list: match the area within its town.
			name, parent := strings.TrimSpace(where[:i]), text.NormalizeTerm(strings.TrimSpace(where[i+1:]))
			for _, l := range snap.MatchLocations(name) {
				if l.Parent != nil && (l.Parent.norm == parent || strings.HasPrefix(text.NormalizeTerm(l.Label), text.NormalizeTerm(where))) {
					out.query.Location = l
					break
				}
			}
		}
		if out.query.Location == nil {
			ms := snap.MatchLocations(where)
			if len(ms) > 0 {
				out.query.Location = ms[0]
				for _, alt := range ms[1:] {
					if len(out.alternates) < 4 && alt.norm == ms[0].norm {
						out.alternates = append(out.alternates, alt)
					}
				}
			}
		}
	}
	if lat, err1 := strconv.ParseFloat(in.Lat, 64); err1 == nil {
		if lng, err2 := strconv.ParseFloat(in.Lng, 64); err2 == nil && lat >= -90 && lat <= 90 && lng >= -180 && lng <= 180 {
			out.query.Lat, out.query.Lng = &lat, &lng
		}
	}
	return out, nil
}

// logSearch records search analytics (§3.2, §16.3): the service and location slugs, a result
// bucket and, for unmatched text, the normalized query without digits (no personal data).
func (s *Service) logSearch(ctx context.Context, run *resolvedSearch, res *Result) {
	props := map[string]string{"results": bucket(res.Total)}
	if run.service != nil {
		props["service"] = run.service.Slug
	} else if run.q != "" {
		props["q"] = scrubQuery(run.q)
	}
	if run.query.Location != nil {
		props["location"] = run.query.Location.Slug
	} else if run.query.Lat != nil {
		props["location"] = "near_me"
	}
	if err := analytics.Record(ctx, s.db, analytics.SearchPerformed, 0, props); err != nil {
		s.log.WarnContext(ctx, "analytics", "error", err)
	}
	if res.Total == 0 {
		if err := analytics.Record(ctx, s.db, analytics.SearchZeroResults, 0, props); err != nil {
			s.log.WarnContext(ctx, "analytics", "error", err)
		}
	}
}

func bucket(n int) string {
	switch {
	case n == 0:
		return "0"
	case n <= 5:
		return "1-5"
	case n <= 20:
		return "6-20"
	}
	return "21+"
}

func scrubQuery(q string) string {
	n := text.NormalizeTerm(q)
	var b strings.Builder
	for _, r := range n {
		if r < '0' || r > '9' {
			b.WriteRune(r)
		}
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	if r := []rune(out); len(r) > 40 {
		out = string(r[:40])
	}
	return out
}
