package discovery

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alumasinde/profy/internal/database"
	"github.com/alumasinde/profy/internal/http/response"
	"github.com/alumasinde/profy/internal/modules/audit"
	"github.com/alumasinde/profy/internal/modules/providers"
	"github.com/alumasinde/profy/internal/modules/settings"
	"github.com/alumasinde/profy/internal/shared/paging"
	"github.com/alumasinde/profy/internal/shared/reqctx"
	"github.com/alumasinde/profy/internal/shared/text"
)

// APIHandler exposes discovery over JSON (§15.3) plus staff tools.
type APIHandler struct{ svc *Service }

// NewAPIHandler builds the handler.
func NewAPIHandler(svc *Service) *APIHandler { return &APIHandler{svc: svc} }

// Register mounts routes.
func (h *APIHandler) Register(mux *http.ServeMux, prefix string) {
	az := h.svc.authz
	mux.HandleFunc("GET "+prefix+"/providers/search", h.search)
	mux.HandleFunc("GET "+prefix+"/categories", h.categories)
	mux.HandleFunc("POST "+prefix+"/demand-requests", h.createDemand)
	mux.Handle("GET "+prefix+"/admin/demand-requests", az.Require("demand_requests.view")(http.HandlerFunc(h.listDemand)))
	mux.Handle("PATCH "+prefix+"/admin/demand-requests/{id}", az.Require("demand_requests.update")(http.HandlerFunc(h.updateDemand)))
	mux.Handle("GET "+prefix+"/admin/search-gaps", az.Require("analytics.view_platform")(http.HandlerFunc(h.gaps)))
	mux.Handle("GET "+prefix+"/admin/landing-pages", az.Require("seo.view")(http.HandlerFunc(h.listLanding)))
	mux.Handle("PUT "+prefix+"/admin/landing-pages", az.Require("seo.update_pages")(http.HandlerFunc(h.putLanding)))
	mux.Handle("POST "+prefix+"/admin/landing-pages/evaluate", az.Require("seo.update_pages")(http.HandlerFunc(h.evaluate)))
}

type placeJSON struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Slug  string `json:"slug"`
	Name  string `json:"name"`
	Label string `json:"label"`
}

func placeOf(l *Location) *placeJSON {
	if l == nil {
		return nil
	}
	return &placeJSON{Type: l.Type, ID: l.PublicID, Slug: l.Slug, Name: l.Name, Label: l.Label}
}

type serviceJSON struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
	Path string `json:"path"`
}

// search mirrors /search: q|service, location|where, lat/lng (one-time, never stored),
// radius_km (≤ 50), page (≤ 50), limit (≤ 50).
func (h *APIHandler) search(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	snap, err := h.svc.Snapshot(ctx)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	in := parseSearch(r)
	run, _ := h.svc.resolve(snap, in)
	res, err := h.svc.Search(ctx, snap, run.query)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	h.svc.logSearch(ctx, run, res)
	in2 := make([]providers.CardInput, 0, len(res.Hits))
	for _, x := range res.Hits {
		in2 = append(in2, providers.CardInput{ProviderID: x.ProviderID, ServiceID: x.ServiceID, Serves: x.Serves})
	}
	locale := h.svc.settings.String(ctx, settings.KeyDefaultLocale)
	if p := reqctx.PrincipalFrom(ctx); p != nil && h.svc.i18n.Has(p.Locale) {
		locale = p.Locale
	}
	cards, err := h.svc.providers.Cards(ctx, locale, in2)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	out := map[string]any{"items": cards, "location": placeOf(run.query.Location)}
	if run.service != nil {
		out["service"] = serviceJSON{run.service.PublicID, run.service.Slug, run.service.Name, run.service.Path()}
	}
	var alts []*placeJSON
	for _, a := range run.alternates {
		alts = append(alts, placeOf(a))
	}
	out["alternate_locations"] = alts
	if res.Total == 0 {
		groups, err := h.svc.alternatives(ctx, snap, res, run.service, run.query.Location, in.Q)
		if err != nil {
			response.Fail(w, r, err)
			return
		}
		type lg struct {
			Kind  string `json:"kind"`
			Links []link `json:"links"`
		}
		var gs []lg
		for _, g := range groups {
			gs = append(gs, lg{Kind: strings.TrimPrefix(g.Title, "discovery.zero_"), Links: g.Links})
		}
		out["alternatives"] = gs
	}
	w.Header().Set("Cache-Control", "private, no-cache")
	response.JSON(w, http.StatusOK, out, map[string]any{"total": res.Total, "page": res.Page, "pages": res.Pages, "per_page": res.PerPage})
}

func (h *APIHandler) categories(w http.ResponseWriter, r *http.Request) {
	snap, err := h.svc.Snapshot(r.Context())
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	type cat struct {
		ID       string        `json:"id"`
		Slug     string        `json:"slug"`
		Name     string        `json:"name"`
		Path     string        `json:"path"`
		Services []serviceJSON `json:"services"`
		Children []cat         `json:"children,omitempty"`
	}
	var conv func(c *Category) cat
	conv = func(c *Category) cat {
		o := cat{ID: c.PublicID, Slug: c.Slug, Name: c.Name, Path: c.Path(), Services: []serviceJSON{}}
		for _, sv := range c.Services {
			o.Services = append(o.Services, serviceJSON{sv.PublicID, sv.Slug, sv.Name, sv.Path()})
		}
		for _, ch := range c.Children {
			o.Children = append(o.Children, conv(ch))
		}
		return o
	}
	out := []cat{}
	for _, c := range snap.Categories {
		out = append(out, conv(c))
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	response.OK(w, out)
}

func (h *APIHandler) createDemand(w http.ResponseWriter, r *http.Request) {
	var in DemandInput
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	id, err := h.svc.CreateDemand(r.Context(), in)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.JSON(w, http.StatusCreated, map[string]string{"id": id}, nil)
}

func (h *APIHandler) listDemand(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	list, next, err := h.svc.ListDemand(r.Context(), q.Get("status"), q.Get("cursor"), limit)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, list, map[string]any{"next_cursor": next})
}

func (h *APIHandler) updateDemand(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Status    string `json:"status"`
		StaffNote string `json:"staff_note"`
	}
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	if err := h.svc.UpdateDemand(r.Context(), r.PathValue("id"), in.Status, in.StaffNote); err != nil {
		response.Fail(w, r, err)
		return
	}
	response.NoContent(w)
}

// ---- Search gaps (zero-result report, §3.2) -------------------------------------------

// Gap is one zero-result combination with its demand.
type Gap struct {
	Service      *serviceJSON `json:"service,omitempty"`
	Query        string       `json:"query,omitempty"`
	Location     *placeJSON   `json:"location,omitempty"`
	LocationSlug string       `json:"location_slug,omitempty"`
	Searches     int          `json:"searches"`
	Requests     int          `json:"requests"`
	LastAt       time.Time    `json:"last_at"`
}

func (h *APIHandler) gaps(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 || days > 365 {
		days = 30
	}
	snap, err := h.svc.Snapshot(ctx)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	since := time.Now().UTC().AddDate(0, 0, -days)
	rows, err := h.svc.db.QueryContext(ctx, `SELECT COALESCE(JSON_UNQUOTE(JSON_EXTRACT(properties, '$.service')), ''),
			COALESCE(JSON_UNQUOTE(JSON_EXTRACT(properties, '$.q')), ''),
			COALESCE(JSON_UNQUOTE(JSON_EXTRACT(properties, '$.location')), ''), COUNT(*), MAX(created_at)
		FROM analytics_events WHERE event_type = 'search_zero_results' AND created_at >= ?
		GROUP BY 1, 2, 3 ORDER BY 4 DESC LIMIT 200`, since)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	type k struct{ svc, q, loc string }
	idx := map[k]*Gap{}
	var out []*Gap
	for rows.Next() {
		var key k
		g := &Gap{}
		if err := rows.Scan(&key.svc, &key.q, &key.loc, &g.Searches, &g.LastAt); err != nil {
			rows.Close()
			response.Fail(w, r, err)
			return
		}
		if sv := snap.Service(key.svc); sv != nil {
			g.Service = &serviceJSON{sv.PublicID, sv.Slug, sv.Name, sv.Path()}
		}
		g.Query, g.LocationSlug = key.q, key.loc
		g.Location = placeOf(snap.Location(key.loc))
		idx[key] = g
		out = append(out, g)
	}
	rows.Close()
	drows, err := h.svc.db.QueryContext(ctx, `SELECT COALESCE(s.slug, ''), COALESCE(d.service_text, ''), d.location_type, d.location_id, COUNT(*)
		FROM demand_requests d LEFT JOIN services s ON s.id = d.service_id WHERE d.created_at >= ?
		GROUP BY 1, 2, 3, 4`, since)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	defer drows.Close()
	for drows.Next() {
		var svc, stext string
		var lt sql.NullString
		var lid sql.NullInt64
		var n int
		if err := drows.Scan(&svc, &stext, &lt, &lid, &n); err != nil {
			response.Fail(w, r, err)
			return
		}
		loc := ""
		if lt.Valid {
			if l := snap.LocationByKey(lt.String, uint64(lid.Int64)); l != nil {
				loc = l.Slug
			}
		}
		q := ""
		if svc == "" {
			q = scrubQuery(stext)
		}
		if g := idx[k{svc, q, loc}]; g != nil {
			g.Requests += n
		}
	}
	if out == nil {
		out = []*Gap{}
	}
	response.OK(w, out)
}

// ---- Landing page administration (§9.2) ----------------------------------------------

// LandingRow is a landing page for staff.
type LandingRow struct {
	Service       serviceJSON `json:"service"`
	Location      placeJSON   `json:"location"`
	Path          string      `json:"path"`
	ProviderCount int         `json:"provider_count"`
	Indexed       bool        `json:"indexed"`
	RobotsMode    string      `json:"robots_mode"`
	MetaTitle     string      `json:"meta_title"`
	MetaDesc      string      `json:"meta_description"`
	Intro         string      `json:"intro"`
	BelowSince    *time.Time  `json:"below_since"`
	EvaluatedAt   *time.Time  `json:"evaluated_at"`
}

func (h *APIHandler) listLanding(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	snap, err := h.svc.Snapshot(ctx)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	q := r.URL.Query()
	after, ok := paging.Decode(q.Get("cursor"))
	if !ok {
		response.Fail(w, r, response.Validation(map[string]string{"cursor": "invalid"}))
		return
	}
	limit := paging.Limit(0, 50, 100)
	where, args := "id > ?", []any{after}
	if sv := snap.Service(q.Get("service")); sv != nil {
		where += " AND service_id = ?"
		args = append(args, sv.ID)
	}
	switch q.Get("state") {
	case "indexed":
		where += " AND provider_count > 0 AND (robots_mode = 'index' OR (robots_mode = 'auto' AND indexed = 1))"
	case "thin":
		where += " AND provider_count > 0 AND NOT (robots_mode = 'index' OR (robots_mode = 'auto' AND indexed = 1))"
	case "overridden":
		where += " AND (robots_mode <> 'auto' OR meta_title IS NOT NULL OR meta_description IS NOT NULL OR intro IS NOT NULL)"
	}
	args = append(args, limit+1)
	rows, err := h.svc.db.QueryContext(ctx, `SELECT id, service_id, location_type, location_id, provider_count, indexed, robots_mode,
		COALESCE(meta_title, ''), COALESCE(meta_description, ''), COALESCE(intro, ''), below_since, evaluated_at
		FROM seo_landing_pages WHERE `+where+` ORDER BY id LIMIT ?`, args...)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	defer rows.Close()
	var out []LandingRow
	var lastID uint64
	n := 0
	next := ""
	for rows.Next() {
		var id, sid, lid uint64
		var typ string
		var row LandingRow
		var bs, ev sql.NullTime
		if err := rows.Scan(&id, &sid, &typ, &lid, &row.ProviderCount, &row.Indexed, &row.RobotsMode, &row.MetaTitle,
			&row.MetaDesc, &row.Intro, &bs, &ev); err != nil {
			response.Fail(w, r, err)
			return
		}
		n++
		if n > limit {
			next = paging.Encode(lastID)
			break
		}
		lastID = id
		sv, l := snap.ServiceByID(sid), snap.LocationByKey(typ, lid)
		if sv == nil || l == nil {
			continue
		}
		row.Service = serviceJSON{sv.PublicID, sv.Slug, sv.Name, sv.Path()}
		row.Location = *placeOf(l)
		row.Path = "/services/" + sv.Slug + "/" + l.Slug
		row.Indexed = row.ProviderCount > 0 && (row.RobotsMode == "index" || (row.RobotsMode == "auto" && row.Indexed))
		if bs.Valid {
			row.BelowSince = &bs.Time
		}
		if ev.Valid {
			row.EvaluatedAt = &ev.Time
		}
		out = append(out, row)
	}
	if out == nil {
		out = []LandingRow{}
	}
	response.JSON(w, http.StatusOK, out, map[string]any{"next_cursor": next,
		"min_providers": h.svc.settings.Int(ctx, settings.KeyLandingMinProviders)})
}

// LandingInput overrides one service × location page.
type LandingInput struct {
	Service         string `json:"service"`  // slug
	Location        string `json:"location"` // slug
	MetaTitle       string `json:"meta_title"`
	MetaDescription string `json:"meta_description"`
	Intro           string `json:"intro"`
	RobotsMode      string `json:"robots_mode"`
}

func (h *APIHandler) putLanding(w http.ResponseWriter, r *http.Request) {
	var in LandingInput
	if err := response.Decode(w, r, &in); err != nil {
		response.Fail(w, r, err)
		return
	}
	if err := h.svc.PutLanding(r.Context(), in); err != nil {
		response.Fail(w, r, err)
		return
	}
	response.NoContent(w)
}

// PutLanding upserts overrides for a landing page; changes are audited (§9.7 SEO history).
func (s *Service) PutLanding(ctx context.Context, in LandingInput) error {
	snap, err := s.Snapshot(ctx)
	if err != nil {
		return err
	}
	errs := map[string]string{}
	sv := snap.Service(in.Service)
	if sv == nil {
		errs["service"] = "invalid"
	}
	l := snap.Location(in.Location)
	if l == nil || !l.Selectable {
		errs["location"] = "invalid"
	}
	if in.RobotsMode == "" {
		in.RobotsMode = "auto"
	}
	if in.RobotsMode != "auto" && in.RobotsMode != "index" && in.RobotsMode != "noindex" {
		errs["robots_mode"] = "invalid"
	}
	mt, ok := text.CleanLine(in.MetaTitle, 160)
	if !ok && mt != "" {
		errs["meta_title"] = "too_long"
	}
	md, ok := text.CleanLine(in.MetaDescription, 320)
	if !ok && md != "" {
		errs["meta_description"] = "too_long"
	}
	intro, ok := text.CleanText(in.Intro, 2000)
	if !ok {
		errs["intro"] = "too_long"
	}
	if len(errs) > 0 {
		return response.Validation(errs)
	}
	return database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO seo_landing_pages (service_id, location_type, location_id, meta_title, meta_description, intro, robots_mode)
			VALUES (?, ?, ?, ?, ?, ?, ?) AS new ON DUPLICATE KEY UPDATE meta_title = new.meta_title, meta_description = new.meta_description,
			intro = new.intro, robots_mode = new.robots_mode`, sv.ID, l.Type, l.ID, nullable(mt), nullable(md), nullable(intro), in.RobotsMode); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "seo.landing.updated", EntityType: "landing_page",
			EntityID: "/services/" + sv.Slug + "/" + l.Slug, NewValues: map[string]any{"robots_mode": in.RobotsMode,
				"meta_title": mt, "meta_description": md, "intro_length": len(intro)}})
	})
}

func (h *APIHandler) evaluate(w http.ResponseWriter, r *http.Request) {
	n, err := h.svc.EvaluateLanding(r.Context())
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	_ = audit.Record(r.Context(), h.svc.db, audit.Entry{Action: "seo.landing.evaluated", EntityType: "landing_page", EntityID: "all",
		NewValues: map[string]any{"rows": n}})
	response.OK(w, map[string]int{"rows": n})
}
