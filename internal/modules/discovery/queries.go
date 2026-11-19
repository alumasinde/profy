package discovery

import (
	"context"
	"sort"

	"github.com/alumasinde/profy/internal/modules/seo"
)

// serviceCounts returns published providers per service (active offerings).
func (s *Service) serviceCounts(ctx context.Context) (map[uint64]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT ps.service_id, COUNT(*) FROM provider_services ps
		JOIN providers p ON p.id = ps.provider_id AND p.listing_status = 'published'
		WHERE ps.status = 'active' GROUP BY ps.service_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uint64]int{}
	for rows.Next() {
		var id uint64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

func (s *Service) idList(ctx context.Context, q string, args ...any) ([]uint64, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uint64
	for rows.Next() {
		var id uint64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// recentProviders returns recently published listings with at least one live offering.
func (s *Service) recentProviders(ctx context.Context, n int) ([]uint64, error) {
	return s.idList(ctx, `SELECT p.id FROM providers p WHERE p.listing_status = 'published'
		AND EXISTS (SELECT 1 FROM provider_services ps WHERE ps.provider_id = p.id AND ps.status = 'active')
		ORDER BY p.published_at DESC, p.id DESC LIMIT ?`, n)
}

// featuredProviders returns explicitly featured, published listings (§7.3).
func (s *Service) featuredProviders(ctx context.Context, n int) ([]uint64, error) {
	return s.idList(ctx, `SELECT id FROM providers WHERE featured_until > UTC_TIMESTAMP(3) AND listing_status = 'published'
		ORDER BY featured_at DESC, id DESC LIMIT ?`, n)
}

type landingCount struct {
	loc   *Location
	count int
}

// landingCountsFor reads evaluated provider counts for a service's landing pages.
func (s *Service) landingCountsFor(ctx context.Context, snap *Snapshot, sv *CatService) ([]landingCount, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT location_type, location_id, provider_count FROM seo_landing_pages
		WHERE service_id = ? AND provider_count > 0`, sv.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []landingCount
	for rows.Next() {
		var typ string
		var id uint64
		var n int
		if err := rows.Scan(&typ, &id, &n); err != nil {
			return nil, err
		}
		if l := snap.LocationByKey(typ, id); l != nil && l.Selectable {
			out = append(out, landingCount{l, n})
		}
	}
	return out, rows.Err()
}

// landingLinks lists where a service is offered. With near set, only other places in the
// same county (excluding the place itself and its ancestors) are returned.
func (s *Service) landingLinks(ctx context.Context, snap *Snapshot, sv *CatService, near *Location, limit int) ([]link, error) {
	counts, err := s.landingCountsFor(ctx, snap, sv)
	if err != nil {
		return nil, err
	}
	rank := map[string]int{County: 0, Town: 1, Area: 2}
	var keep []landingCount
	for _, c := range counts {
		if near != nil {
			county := near.County()
			if c.loc == near || c.loc.Contains(near) || county == nil || !county.Contains(c.loc) || c.loc.Type == County {
				continue
			}
		}
		keep = append(keep, c)
	}
	sort.SliceStable(keep, func(i, j int) bool {
		if near == nil && rank[keep[i].loc.Type] != rank[keep[j].loc.Type] {
			return rank[keep[i].loc.Type] < rank[keep[j].loc.Type]
		}
		if keep[i].count != keep[j].count {
			return keep[i].count > keep[j].count
		}
		return keep[i].loc.Label < keep[j].loc.Label
	})
	var out []link
	for _, c := range keep {
		if len(out) >= limit {
			break
		}
		out = append(out, link{Label: c.loc.Name, URL: seo.LandingPath(sv.Slug, c.loc.Slug), Count: c.count})
	}
	return out, nil
}

// servicesIn counts live services offered by providers matching a location.
func (s *Service) servicesIn(ctx context.Context, snap *Snapshot, res *Result, loc *Location) ([]link, error) {
	counts, err := s.serviceCountsAmong(ctx, res.matchedLoc)
	if err != nil {
		return nil, err
	}
	var out []link
	for _, sv := range snap.Services {
		if n := counts[sv.ID]; n > 0 {
			out = append(out, link{Label: sv.Name, URL: seo.LandingPath(sv.Slug, loc.Slug), Count: n})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	if len(out) > 24 {
		out = out[:24]
	}
	return out, nil
}

func (s *Service) serviceCountsAmong(ctx context.Context, providersSet map[uint64]float64) (map[uint64]int, error) {
	out := map[uint64]int{}
	pids := make([]uint64, 0, len(providersSet))
	for id := range providersSet {
		pids = append(pids, id)
	}
	for len(pids) > 0 {
		chunk := pids[:min(len(pids), 1000)]
		pids = pids[len(chunk):]
		ph, args := inList(chunk)
		rows, err := s.db.QueryContext(ctx, `SELECT service_id, COUNT(*) FROM provider_services
			WHERE status = 'active' AND provider_id IN (`+ph+`) GROUP BY service_id`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id uint64
			var n int
			if err := rows.Scan(&id, &n); err != nil {
				rows.Close()
				return nil, err
			}
			out[id] += n
		}
		rows.Close()
	}
	return out, nil
}

// alternatives builds the zero-result experience (§3.2): broader coverage, nearby places,
// related services and popular services. Titles are catalogue keys.
func (s *Service) alternatives(ctx context.Context, snap *Snapshot, res *Result, sv *CatService, loc *Location, q string) ([]linkGroup, error) {
	var groups []linkGroup
	if sv != nil && loc != nil {
		g := linkGroup{Title: "discovery.zero_broader"}
		for _, a := range ancestors(loc) {
			r, err := s.Search(ctx, snap, Query{Service: sv, Location: a, CountOnly: true})
			if err != nil {
				return nil, err
			}
			if r.Total > 0 {
				g.Links = append(g.Links, link{Label: a.Label, URL: seo.LandingPath(sv.Slug, a.Slug), Count: r.Total})
			}
		}
		if len(g.Links) > 0 {
			groups = append(groups, g)
		}
		near, err := s.landingLinks(ctx, snap, sv, loc, 8)
		if err != nil {
			return nil, err
		}
		if len(near) > 0 {
			groups = append(groups, linkGroup{Title: "discovery.zero_nearby", Links: near})
		}
	}
	if loc != nil && len(res.matchedLoc) > 0 {
		counts, err := s.serviceCountsAmong(ctx, res.matchedLoc)
		if err != nil {
			return nil, err
		}
		g := linkGroup{Title: "discovery.zero_related"}
		var pool []*CatService
		if sv != nil {
			pool = sv.Category.root().AllServices()
		} else {
			pool = snap.Services
		}
		for _, o := range pool {
			if o != sv && counts[o.ID] > 0 {
				g.Links = append(g.Links, link{Label: o.Name, URL: seo.LandingPath(o.Slug, loc.Slug), Count: counts[o.ID]})
			}
		}
		if len(g.Links) > 8 {
			g.Links = g.Links[:8]
		}
		if len(g.Links) > 0 {
			groups = append(groups, g)
		}
	}
	if len(groups) == 0 {
		counts, err := s.serviceCounts(ctx)
		if err != nil {
			return nil, err
		}
		g := linkGroup{Title: "discovery.zero_popular"}
		for _, o := range snap.Services {
			if counts[o.ID] > 0 {
				g.Links = append(g.Links, link{Label: o.Name, URL: o.Path(), Count: counts[o.ID]})
			}
		}
		sort.SliceStable(g.Links, func(i, j int) bool { return g.Links[i].Count > g.Links[j].Count })
		if len(g.Links) > 8 {
			g.Links = g.Links[:8]
		}
		if len(g.Links) > 0 {
			groups = append(groups, g)
		}
	}
	return groups, nil
}
