package discovery

import (
	"context"
	"encoding/xml"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/alumasinde/profy/internal/http/response"
	"github.com/alumasinde/profy/internal/modules/seo"
)

// sitemapSize is the URLs per file, well under the 50,000 protocol limit (§9.4).
const sitemapSize = 40000

type urlEntry struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod,omitempty"`
}

type urlSet struct {
	XMLName xml.Name   `xml:"urlset"`
	NS      string     `xml:"xmlns,attr"`
	URLs    []urlEntry `xml:"url"`
}

type sitemapIndex struct {
	XMLName  xml.Name   `xml:"sitemapindex"`
	NS       string     `xml:"xmlns,attr"`
	Sitemaps []urlEntry `xml:"sitemap"`
}

const sitemapNS = "http://www.sitemaps.org/schemas/sitemap/0.9"

func day(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02")
}

func writeXML(w http.ResponseWriter, r *http.Request, v any) {
	b, err := xml.Marshal(v)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write([]byte(xml.Header))
	_, _ = w.Write(b)
}

// sitemapIndex lists the per-type sitemap files (§9.4: split by type, ≤ 50,000 URLs each).
func (h *PublicHandler) sitemapIndex(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	origin := h.pub.SiteOrigin(r)
	idx := sitemapIndex{NS: sitemapNS}
	for _, f := range []string{"static", "categories", "services", "locations"} {
		idx.Sitemaps = append(idx.Sitemaps, urlEntry{Loc: origin + "/sitemaps/" + f + ".xml"})
	}
	for _, kind := range []string{"landing", "providers"} {
		n, err := h.svc.sitemapCount(ctx, kind)
		if err != nil {
			response.Fail(w, r, err)
			return
		}
		for i := 1; i == 1 || (i-1)*sitemapSize < n; i++ {
			idx.Sitemaps = append(idx.Sitemaps, urlEntry{Loc: origin + "/sitemaps/" + kind + "-" + strconv.Itoa(i) + ".xml"})
		}
	}
	writeXML(w, r, idx)
}

var sitemapFileRe = regexp.MustCompile(`^(static|categories|services|locations|landing|providers)(?:-([1-9][0-9]{0,3}))?\.xml$`)

func (h *PublicHandler) sitemapFile(w http.ResponseWriter, r *http.Request) {
	m := sitemapFileRe.FindStringSubmatch(r.PathValue("file"))
	if m == nil || ((m[1] == "landing" || m[1] == "providers") != (m[2] != "")) {
		h.pub.NotFound(w, r)
		return
	}
	page, _ := strconv.Atoi(m[2])
	urls, err := h.svc.sitemapURLs(r.Context(), m[1], page)
	if err != nil {
		response.Fail(w, r, err)
		return
	}
	origin := h.pub.SiteOrigin(r)
	set := urlSet{NS: sitemapNS}
	for _, u := range urls {
		u.Loc = origin + u.Loc
		set.URLs = append(set.URLs, u)
	}
	writeXML(w, r, set)
}

func (s *Service) sitemapCount(ctx context.Context, kind string) (int, error) {
	q := `SELECT COUNT(*) FROM providers WHERE listing_status = 'published'`
	if kind == "landing" {
		q = `SELECT COUNT(*) FROM seo_landing_pages WHERE provider_count > 0 AND (robots_mode = 'index' OR (robots_mode = 'auto' AND indexed = 1))`
	}
	var n int
	err := s.db.QueryRowContext(ctx, q).Scan(&n)
	return n, err
}

// sitemapURLs returns site-relative URLs. Only eligible, indexable pages are listed:
// unpublished, paused, suspended and thin pages never appear (§9.4).
func (s *Service) sitemapURLs(ctx context.Context, kind string, page int) ([]urlEntry, error) {
	snap, err := s.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	var out []urlEntry
	switch kind {
	case "static":
		out = append(out, urlEntry{Loc: "/"}, urlEntry{Loc: "/categories"}, urlEntry{Loc: "/badges"})
		if s.pages != nil {
			list, err := s.pages(ctx)
			if err != nil {
				return nil, err
			}
			for _, p := range list {
				out = append(out, urlEntry{Loc: p.Path, LastMod: day(p.UpdatedAt)})
			}
		}
	case "categories", "services":
		counts, err := s.serviceCounts(ctx)
		if err != nil {
			return nil, err
		}
		if kind == "services" {
			for _, sv := range snap.Services {
				if counts[sv.ID] > 0 {
					out = append(out, urlEntry{Loc: sv.Path(), LastMod: day(sv.UpdatedAt)})
				}
			}
			break
		}
		for _, c := range snap.categoryBy {
			n := 0
			for _, sv := range c.AllServices() {
				n += counts[sv.ID]
			}
			if n > 0 {
				out = append(out, urlEntry{Loc: c.Path(), LastMod: day(c.UpdatedAt)})
			}
		}
	case "locations":
		withProviders, err := s.locationsWithProviders(ctx)
		if err != nil {
			return nil, err
		}
		for key := range withProviders {
			if l := snap.locationKey[key]; l != nil && l.Selectable && l.Indexable {
				out = append(out, urlEntry{Loc: l.Path(), LastMod: day(l.UpdatedAt)})
			}
		}
	case "landing":
		rows, err := s.db.QueryContext(ctx, `SELECT service_id, location_type, location_id, COALESCE(evaluated_at, updated_at)
			FROM seo_landing_pages WHERE provider_count > 0 AND (robots_mode = 'index' OR (robots_mode = 'auto' AND indexed = 1))
			ORDER BY id LIMIT ? OFFSET ?`, sitemapSize, (page-1)*sitemapSize)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var sid, lid uint64
			var typ string
			var at time.Time
			if err := rows.Scan(&sid, &typ, &lid, &at); err != nil {
				return nil, err
			}
			sv, l := snap.ServiceByID(sid), snap.LocationByKey(typ, lid)
			if sv != nil && l != nil && l.Selectable {
				out = append(out, urlEntry{Loc: seo.LandingPath(sv.Slug, l.Slug), LastMod: day(at)})
			}
		}
		return out, rows.Err()
	case "providers":
		rows, err := s.db.QueryContext(ctx, `SELECT slug, updated_at FROM providers WHERE listing_status = 'published'
			ORDER BY id LIMIT ? OFFSET ?`, sitemapSize, (page-1)*sitemapSize)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var slug string
			var at time.Time
			if err := rows.Scan(&slug, &at); err != nil {
				return nil, err
			}
			out = append(out, urlEntry{Loc: seo.ProviderPath(slug), LastMod: day(at)})
		}
		return out, rows.Err()
	}
	return out, nil
}

// locationsWithProviders returns location keys that have at least one evaluated landing page
// with providers.
func (s *Service) locationsWithProviders(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT location_type, location_id FROM seo_landing_pages WHERE provider_count > 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var typ string
		var id uint64
		if err := rows.Scan(&typ, &id); err != nil {
			return nil, err
		}
		out[typ+":"+itoa(id)] = true
	}
	return out, rows.Err()
}
