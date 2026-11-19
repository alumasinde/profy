package discovery

import (
	"context"
	"database/sql"
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/alumasinde/profy/internal/modules/settings"
)

// Search limits (§10.4: bounded radius and page size).
const (
	MaxRadiusKm      = 50
	DefaultRadiusKm  = 10
	DefaultPerPage   = 20
	MaxPerPage       = 50
	MaxPage          = 50
	maxCandidates    = 5000
	providerMaxRange = 50 // providers.service_radius_km is capped at 50
	explorationDays  = 45
)

// Location match strengths (§10.5: explicit coverage > radius > broader).
const (
	locExact  = 1.0  // covers or is based in exactly the searched location
	locParent = 0.85 // covers the searched area's town/county, or is based inside the searched town/county
	locChild  = 0.75 // covers part of the searched town/county
	locRadius = 0.5  // reaches the point by radius (0.5–0.65 by distance)
)

// Query is a search request. Service and location may come from slugs (links, landing
// pages) or free text (the search form).
type Query struct {
	Text      string
	Service   *CatService // explicit service (landing pages); overrides Text matching
	Location  *Location
	Lat, Lng  *float64
	RadiusKm  int
	Page      int
	PerPage   int
	CountOnly bool
}

// Hit is one ranked provider.
type Hit struct {
	ProviderID uint64
	ServiceID  uint64 // best-matching offered service
	Score      float64
	LocScore   float64
	Serves     string
	parts      [6]float64
}

// Result is a ranked, paginated result.
type Result struct {
	Hits     []Hit
	Total    int
	Page     int
	Pages    int
	PerPage  int
	Services map[uint64]float64 // matched services and relevance
	// matchedLoc are providers matching the location (any service); used for related-service
	// counts on zero results.
	matchedLoc map[uint64]float64
}

type candidate struct {
	id           uint64
	completeness int
	ratingAvg    sql.NullFloat64
	ratingN      int
	updatedAt    time.Time
	publishedAt  sql.NullTime
}

// weights are the ranking weights in points (settings, bounded by the settings schema).
type weights struct{ service, location, completeness, rating, activity, exploration float64 }

func (s *Service) weights(ctx context.Context) weights {
	g := func(k string) float64 { return float64(s.settings.Int(ctx, k)) }
	return weights{g(settings.KeyRankService), g(settings.KeyRankLocation), g(settings.KeyRankCompleteness),
		g(settings.KeyRankRating), g(settings.KeyRankActivity), g(settings.KeyRankExploration)}
}

// Search runs the staged search (§10.6): relational coverage matches and spatial candidates
// via the spatial index, exact distance for candidates only, then eligibility filtering by
// published status and live service offerings, then ranking.
func (s *Service) Search(ctx context.Context, snap *Snapshot, q Query) (*Result, error) {
	res := &Result{Services: map[uint64]float64{}}
	// 1. Services.
	switch {
	case q.Service != nil:
		res.Services[q.Service.ID] = relExact
	case strings.TrimSpace(q.Text) != "":
		res.Services = snap.MatchServices(q.Text)
	default:
		for _, sv := range snap.Services {
			res.Services[sv.ID] = relExact
		}
	}
	nameIDs := map[uint64]bool{}
	if len(res.Services) == 0 && strings.TrimSpace(q.Text) != "" {
		// No service matched: fall back to provider names so people can find a pro they know.
		ids, err := s.providersByName(ctx, q.Text)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			nameIDs[id] = true
		}
		if len(nameIDs) == 0 {
			return res.paginate(q), nil
		}
		for _, sv := range snap.Services {
			res.Services[sv.ID] = 0.6
		}
	}
	if len(res.Services) == 0 {
		return res.paginate(q), nil
	}

	// 2. Location candidates (nil map = no location filter).
	var locMatch map[uint64]float64
	var serves map[uint64]string
	if q.Location != nil || (q.Lat != nil && q.Lng != nil) {
		var err error
		locMatch, serves, err = s.matchLocation(ctx, snap, q)
		if err != nil {
			return nil, err
		}
		res.matchedLoc = locMatch
		if len(locMatch) == 0 {
			return res.paginate(q), nil
		}
	}

	// 3. Eligibility: published providers with an active offering of a matched live service.
	offers, err := s.offerings(ctx, res.Services, locMatch)
	if err != nil {
		return nil, err
	}
	if len(nameIDs) > 0 {
		for id := range offers {
			if !nameIDs[id] {
				delete(offers, id)
			}
		}
	}
	if len(offers) == 0 {
		return res.paginate(q), nil
	}
	if q.CountOnly {
		res.Total = len(offers)
		return res, nil
	}
	cands, err := s.candidates(ctx, offers)
	if err != nil {
		return nil, err
	}

	// 4. Rank.
	w := s.weights(ctx)
	now := time.Now()
	day := now.UTC().Format("2006-01-02")
	for _, c := range cands {
		h := Hit{ProviderID: c.id}
		best := 0.0
		for sid := range offers[c.id] {
			if rel := res.Services[sid]; rel > best || (rel == best && sid < h.ServiceID) {
				best, h.ServiceID = rel, sid
			}
		}
		loc := 0.0
		if locMatch != nil {
			loc = locMatch[c.id]
			h.Serves = serves[c.id]
		}
		h.LocScore = loc
		// Bayesian-smoothed rating (§10.5): prior mean 4.0 weighted as 5 reviews.
		rating := 4.0 / 5
		if c.ratingN > 0 && c.ratingAvg.Valid {
			rating = (5*4.0 + float64(c.ratingN)*c.ratingAvg.Float64) / float64(5+c.ratingN) / 5
		}
		age := now.Sub(c.updatedAt).Hours() / 24
		activity := math.Max(0, math.Min(1, 1-(age-30)/150)) // 1 for 30 days, 0 at 180
		explore := 0.0
		if c.publishedAt.Valid && now.Sub(c.publishedAt.Time) < explorationDays*24*time.Hour {
			explore = w.exploration * unit(c.id, day) // deterministic per day: stable pagination
		}
		h.parts = [6]float64{w.service * best, w.location * loc, w.completeness * float64(c.completeness) / 100,
			w.rating * rating, w.activity * activity, explore}
		for _, p := range h.parts {
			h.Score += p
		}
		res.Hits = append(res.Hits, h)
	}
	sort.SliceStable(res.Hits, func(i, j int) bool {
		a, b := res.Hits[i], res.Hits[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		return unit(a.ProviderID, day+"t") > unit(b.ProviderID, day+"t")
	})
	s.sampleRanking(ctx, q, res.Hits)
	return res.paginate(q), nil
}

func (r *Result) paginate(q Query) *Result {
	per := q.PerPage
	if per <= 0 {
		per = DefaultPerPage
	}
	if per > MaxPerPage {
		per = MaxPerPage
	}
	page := q.Page
	if page < 1 {
		page = 1
	}
	if page > MaxPage {
		page = MaxPage
	}
	if r.Total == 0 {
		r.Total = len(r.Hits)
	}
	r.PerPage, r.Page = per, page
	r.Pages = (r.Total + per - 1) / per
	from := (page - 1) * per
	if from >= len(r.Hits) {
		r.Hits = nil
		return r
	}
	to := min(from+per, len(r.Hits))
	r.Hits = r.Hits[from:to]
	return r
}

// unit is a deterministic pseudo-random number in [0,1) for a provider and a salt.
func unit(id uint64, salt string) float64 {
	h := fnv.New64a()
	fmt.Fprintf(h, "%d|%s", id, salt)
	return float64(h.Sum64()%1_000_000) / 1_000_000
}

// sampleRanking logs ranking inputs for about 2% of searches so weights can be tuned
// (§10.5). It contains provider IDs and scores only — no query text or user data.
func (s *Service) sampleRanking(ctx context.Context, q Query, hits []Hit) {
	if len(hits) == 0 || unit(uint64(time.Now().UnixNano()), "sample") > 0.02 {
		return
	}
	top := hits
	if len(top) > 5 {
		top = top[:5]
	}
	parts := make([]string, 0, len(top))
	for _, h := range top {
		parts = append(parts, fmt.Sprintf("%d:%.1f[s%.1f l%.1f c%.1f r%.1f a%.1f e%.1f]", h.ProviderID, h.Score,
			h.parts[0], h.parts[1], h.parts[2], h.parts[3], h.parts[4], h.parts[5]))
	}
	loc := ""
	if q.Location != nil {
		loc = q.Location.Slug
	}
	s.log.InfoContext(ctx, "search ranking sample", "location", loc, "candidates", len(hits), "top", strings.Join(parts, " "))
}

// matchLocation returns published providers matching the searched place with a strength,
// plus a "serves" note naming the coverage that matched when it is not the base area.
func (s *Service) matchLocation(ctx context.Context, snap *Snapshot, q Query) (map[uint64]float64, map[uint64]string, error) {
	match := map[uint64]float64{}
	serves := map[uint64]string{}
	set := func(id uint64, v float64, note string) {
		if v > match[id] {
			match[id] = v
			serves[id] = note
		}
	}
	if l := q.Location; l != nil {
		// Explicit coverage (§10.2): exact level, parents (town/county coverage covers an
		// area) and children (area coverage counts toward its town and county).
		keys := map[string]float64{l.Key(): locExact}
		for p := l.Parent; p != nil; p = p.Parent {
			keys[p.Key()] = locParent
		}
		var walk func(*Location)
		walk = func(x *Location) {
			for _, ch := range x.Children {
				keys[ch.Key()] = locChild
				walk(ch)
			}
		}
		walk(l)
		if err := s.coverageMatches(ctx, keys, func(id uint64, v float64) { set(id, v, l.Name) }); err != nil {
			return nil, nil, err
		}
		// Base area inside the searched location.
		var areas []uint64
		if l.Type == Area {
			areas = []uint64{l.ID}
		} else {
			for _, k := range keysOfType(keys, Area) {
				areas = append(areas, k)
			}
		}
		if err := s.baseMatches(ctx, areas, func(id, area uint64) {
			v := locParent
			if l.Type == Area && area == l.ID {
				v = locExact
			}
			set(id, v, "")
		}); err != nil {
			return nil, nil, err
		}
	}
	// Radius (§10.3): from the visitor's point, or the searched area/town centroid.
	var lat, lng float64
	var havePoint bool
	if q.Lat != nil && q.Lng != nil {
		lat, lng, havePoint = *q.Lat, *q.Lng, true
	} else if q.Location != nil && q.Location.Type != County {
		lat, lng, havePoint = q.Location.Centroid()
	}
	if havePoint {
		r := q.RadiusKm
		if q.Lat != nil && r <= 0 {
			r = DefaultRadiusKm
		}
		note := ""
		if q.Location != nil {
			note = q.Location.Name
		}
		if err := s.radiusMatches(ctx, lat, lng, r, func(id uint64, v float64) { set(id, v, note) }); err != nil {
			return nil, nil, err
		}
	}
	return match, serves, nil
}

func keysOfType(keys map[string]float64, typ string) []uint64 {
	var out []uint64
	for k := range keys {
		if strings.HasPrefix(k, typ+":") {
			if n, err := strconv.ParseUint(k[len(typ)+1:], 10, 64); err == nil {
				out = append(out, n)
			}
		}
	}
	return out
}

// coverageMatches reads provider_service_areas through its (location_type, location_id)
// index for every relevant key, restricted to published providers.
func (s *Service) coverageMatches(ctx context.Context, keys map[string]float64, fn func(uint64, float64)) error {
	for _, typ := range []string{Area, Town, County} {
		idList := keysOfType(keys, typ)
		for len(idList) > 0 {
			chunk := idList[:min(len(idList), 1000)]
			idList = idList[len(chunk):]
			ph, args := inList(chunk)
			rows, err := s.db.QueryContext(ctx, `SELECT psa.provider_id, psa.location_id FROM provider_service_areas psa
				JOIN providers p ON p.id = psa.provider_id AND p.listing_status = 'published'
				WHERE psa.location_type = ? AND psa.location_id IN (`+ph+`)`, append([]any{typ}, args...)...)
			if err != nil {
				return err
			}
			for rows.Next() {
				var pid, lid uint64
				if err := rows.Scan(&pid, &lid); err != nil {
					rows.Close()
					return err
				}
				fn(pid, keys[typ+":"+itoa(lid)])
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) baseMatches(ctx context.Context, areas []uint64, fn func(id, area uint64)) error {
	for len(areas) > 0 {
		chunk := areas[:min(len(areas), 1000)]
		areas = areas[len(chunk):]
		ph, args := inList(chunk)
		rows, err := s.db.QueryContext(ctx, `SELECT id, base_area_id FROM providers
			WHERE listing_status = 'published' AND base_area_id IN (`+ph+`)`, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id, a uint64
			if err := rows.Scan(&id, &a); err != nil {
				rows.Close()
				return err
			}
			fn(id, a)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}
	return nil
}

// radiusMatches finds providers whose own travel radius reaches the point, or (when the
// visitor chose a radius) who are within it. The bounding box uses the spatial index;
// exact distances are computed for candidates only (§10.6).
func (s *Service) radiusMatches(ctx context.Context, lat, lng float64, visitorKm int, fn func(uint64, float64)) error {
	if visitorKm > MaxRadiusKm {
		visitorKm = MaxRadiusKm
	}
	reach := float64(max(visitorKm, providerMaxRange))
	box := envelope(lat, lng, reach)
	rows, err := s.db.QueryContext(ctx, `SELECT pg.provider_id, ST_Distance_Sphere(pg.location, ST_SRID(POINT(?, ?), 4326)),
			COALESCE(p.service_radius_km, 0)
		FROM provider_geo pg JOIN providers p ON p.id = pg.provider_id
		WHERE MBRContains(ST_GeomFromText(?, 4326, 'axis-order=long-lat'), pg.location) AND p.listing_status = 'published'`,
		lng, lat, box)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id uint64
		var d float64
		var own int
		if err := rows.Scan(&id, &d, &own); err != nil {
			return err
		}
		km := d / 1000
		switch {
		case own > 0 && km <= float64(own):
			fn(id, locRadius+0.15*(1-km/float64(own)))
		case visitorKm > 0 && km <= float64(visitorKm):
			fn(id, locRadius-0.05+0.15*(1-km/float64(visitorKm)))
		}
	}
	return rows.Err()
}

// envelope returns a WKT polygon (long-lat order) bounding a circle of radius km.
func envelope(lat, lng, km float64) string {
	dLat := km / 111.32
	dLng := km / (111.32 * math.Max(0.01, math.Cos(lat*math.Pi/180)))
	minLat, maxLat := math.Max(-90, lat-dLat), math.Min(90, lat+dLat)
	minLng, maxLng := math.Max(-180, lng-dLng), math.Min(180, lng+dLng)
	return fmt.Sprintf("POLYGON((%f %f,%f %f,%f %f,%f %f,%f %f))",
		minLng, minLat, maxLng, minLat, maxLng, maxLat, minLng, maxLat, minLng, minLat)
}

// offerings returns active offerings of the given services by published providers,
// restricted to locMatch when non-nil: provider → service IDs.
func (s *Service) offerings(ctx context.Context, services map[uint64]float64, locMatch map[uint64]float64) (map[uint64]map[uint64]bool, error) {
	sids := make([]uint64, 0, len(services))
	for id := range services {
		sids = append(sids, id)
	}
	sph, sargs := inList(sids)
	out := map[uint64]map[uint64]bool{}
	add := func(rows *sql.Rows) error {
		defer rows.Close()
		for rows.Next() {
			var pid, sid uint64
			if err := rows.Scan(&pid, &sid); err != nil {
				return err
			}
			if out[pid] == nil {
				out[pid] = map[uint64]bool{}
			}
			out[pid][sid] = true
		}
		return rows.Err()
	}
	if locMatch == nil {
		rows, err := s.db.QueryContext(ctx, `SELECT ps.provider_id, ps.service_id FROM provider_services ps
			JOIN providers p ON p.id = ps.provider_id AND p.listing_status = 'published'
			WHERE ps.status = 'active' AND ps.service_id IN (`+sph+`) LIMIT `+strconv.Itoa(maxCandidates*4), sargs...)
		if err != nil {
			return nil, err
		}
		return out, add(rows)
	}
	pids := make([]uint64, 0, len(locMatch))
	for id := range locMatch {
		pids = append(pids, id)
	}
	sort.Slice(pids, func(i, j int) bool { return pids[i] < pids[j] })
	for len(pids) > 0 {
		chunk := pids[:min(len(pids), 1000)]
		pids = pids[len(chunk):]
		pph, pargs := inList(chunk)
		rows, err := s.db.QueryContext(ctx, `SELECT provider_id, service_id FROM provider_services
			WHERE status = 'active' AND service_id IN (`+sph+`) AND provider_id IN (`+pph+`)`, append(sargs, pargs...)...)
		if err != nil {
			return nil, err
		}
		if err := add(rows); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// candidates loads ranking inputs for providers.
func (s *Service) candidates(ctx context.Context, offers map[uint64]map[uint64]bool) ([]candidate, error) {
	pids := make([]uint64, 0, len(offers))
	for id := range offers {
		pids = append(pids, id)
	}
	sort.Slice(pids, func(i, j int) bool { return pids[i] < pids[j] })
	if len(pids) > maxCandidates {
		pids = pids[:maxCandidates]
	}
	var out []candidate
	for len(pids) > 0 {
		chunk := pids[:min(len(pids), 1000)]
		pids = pids[len(chunk):]
		ph, args := inList(chunk)
		rows, err := s.db.QueryContext(ctx, `SELECT id, completeness_score, rating_average, rating_count, updated_at, published_at
			FROM providers WHERE listing_status = 'published' AND id IN (`+ph+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var c candidate
			if err := rows.Scan(&c.id, &c.completeness, &c.ratingAvg, &c.ratingN, &c.updatedAt, &c.publishedAt); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, c)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Service) providersByName(ctx context.Context, q string) ([]uint64, error) {
	q = strings.TrimSpace(q)
	if len([]rune(q)) < 3 {
		return nil, nil
	}
	like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q) + "%"
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM providers WHERE listing_status = 'published' AND display_name LIKE ? LIMIT 200`, like)
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

func inList(idList []uint64) (string, []any) {
	args := make([]any, len(idList))
	for i, id := range idList {
		args[i] = id
	}
	return strings.TrimSuffix(strings.Repeat("?,", len(idList)), ","), args
}
