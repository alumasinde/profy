package discovery

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/alumasinde/profy/internal/database"
	"github.com/alumasinde/profy/internal/jobsqueue"
	"github.com/alumasinde/profy/internal/modules/settings"
)

// JobEvaluateLanding re-evaluates landing-page indexing daily (§9.6).
const JobEvaluateLanding = "discovery.evaluate_landing"

// Landing is the stored state of a service × location page.
type Landing struct {
	ID            uint64
	MetaTitle     string
	MetaDesc      string
	Intro         string
	RobotsMode    string // auto | index | noindex
	Indexed       bool
	ProviderCount int
	BelowSince    *time.Time
	EvaluatedAt   *time.Time
}

func (s *Service) landing(ctx context.Context, ex database.Execer, serviceID uint64, loc *Location) (*Landing, error) {
	var l Landing
	var bs, ev sql.NullTime
	err := ex.QueryRowContext(ctx, `SELECT id, COALESCE(meta_title,''), COALESCE(meta_description,''), COALESCE(intro,''),
		robots_mode, indexed, provider_count, below_since, evaluated_at
		FROM seo_landing_pages WHERE service_id = ? AND location_type = ? AND location_id = ?`, serviceID, loc.Type, loc.ID).
		Scan(&l.ID, &l.MetaTitle, &l.MetaDesc, &l.Intro, &l.RobotsMode, &l.Indexed, &l.ProviderCount, &bs, &ev)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if bs.Valid {
		l.BelowSince = &bs.Time
	}
	if ev.Valid {
		l.EvaluatedAt = &ev.Time
	}
	return &l, nil
}

// landingIndexable applies the indexing policy (§9.6) to a live provider count:
//   - never index an empty page;
//   - an explicit override wins;
//   - otherwise the evaluated, hysteresis-smoothed state; before the first evaluation, the
//     live count against the threshold.
func (s *Service) landingIndexable(ctx context.Context, l *Landing, live int) bool {
	if live == 0 {
		return false
	}
	if l != nil {
		switch l.RobotsMode {
		case "index":
			return true
		case "noindex":
			return false
		}
		if l.EvaluatedAt != nil {
			return l.Indexed
		}
	}
	return live >= s.settings.Int(ctx, settings.KeyLandingMinProviders)
}

// EvaluateLanding recomputes provider counts for every service × selectable location with
// at least one provider (plus existing rows) and updates indexing state with hysteresis:
// a page reaching the threshold is indexed at once; a page dropping below it stays indexed
// until it has been below for the configured number of days; a page with no providers is
// never indexed. It is idempotent and safe to run at any time.
func (s *Service) EvaluateLanding(ctx context.Context) (int, error) {
	snap, err := s.Snapshot(ctx)
	if err != nil {
		return 0, err
	}
	counts, err := s.landingCounts(ctx, snap)
	if err != nil {
		return 0, err
	}
	n := s.settings.Int(ctx, settings.KeyLandingMinProviders)
	hold := time.Duration(s.settings.Int(ctx, settings.KeyLandingHysteresisDay)) * 24 * time.Hour
	now := time.Now().UTC()

	type key struct {
		sid uint64
		typ string
		lid uint64
	}
	existing := map[key]struct {
		indexed bool
		below   sql.NullTime
	}{}
	rows, err := s.db.QueryContext(ctx, `SELECT service_id, location_type, location_id, indexed, below_since FROM seo_landing_pages`)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var k key
		var v struct {
			indexed bool
			below   sql.NullTime
		}
		if err := rows.Scan(&k.sid, &k.typ, &k.lid, &v.indexed, &v.below); err != nil {
			rows.Close()
			return 0, err
		}
		existing[k] = v
	}
	rows.Close()
	for k := range existing {
		if _, ok := counts[lkey{k.sid, k.typ, k.lid}]; !ok {
			counts[lkey{k.sid, k.typ, k.lid}] = 0
		}
	}
	updated := 0
	err = database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		for k, c := range counts {
			if snap.ServiceByID(k.sid) == nil {
				continue // service gone or unpublished: FK cascade / stale row, leave it
			}
			prev, had := existing[key{k.sid, k.typ, k.lid}]
			indexed, below := false, any(nil)
			switch {
			case c >= n:
				indexed = true
			case c == 0:
				indexed = false
			case had && prev.indexed:
				since := now
				if prev.below.Valid {
					since = prev.below.Time
				}
				indexed = now.Sub(since) < hold
				if indexed {
					below = since
				}
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO seo_landing_pages (service_id, location_type, location_id, indexed, provider_count, below_since, evaluated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?) AS new
				ON DUPLICATE KEY UPDATE indexed = new.indexed, provider_count = new.provider_count, below_since = new.below_since,
					evaluated_at = new.evaluated_at`, k.sid, k.typ, k.lid, indexed, c, below, now); err != nil {
				return err
			}
			updated++
		}
		return nil
	})
	return updated, err
}

type lkey struct {
	sid uint64
	typ string
	lid uint64
}

// landingCounts counts published providers per live service × selectable location using the
// same matching rules as search: explicit coverage (with parents and children), base area,
// and the provider's own radius reaching a location's centroid.
func (s *Service) landingCounts(ctx context.Context, snap *Snapshot) (map[lkey]int, error) {
	type prov struct {
		services []uint64
		locs     map[*Location]bool
	}
	ps := map[uint64]*prov{}
	rows, err := s.db.QueryContext(ctx, `SELECT ps.provider_id, ps.service_id FROM provider_services ps
		JOIN providers p ON p.id = ps.provider_id AND p.listing_status = 'published' WHERE ps.status = 'active'`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var pid, sid uint64
		if err := rows.Scan(&pid, &sid); err != nil {
			rows.Close()
			return nil, err
		}
		if snap.ServiceByID(sid) == nil {
			continue
		}
		if ps[pid] == nil {
			ps[pid] = &prov{locs: map[*Location]bool{}}
		}
		ps[pid].services = append(ps[pid].services, sid)
	}
	rows.Close()
	cover := func(p *prov, l *Location) {
		if l == nil || !l.Selectable {
			return
		}
		// A location matches when it is the covered one, an ancestor (child coverage counts
		// toward the town/county page) or a descendant (town/county coverage covers areas).
		for a := l; a != nil; a = a.Parent {
			if a.Selectable {
				p.locs[a] = true
			}
		}
		var walk func(*Location)
		walk = func(x *Location) {
			for _, ch := range x.Children {
				if ch.Selectable {
					p.locs[ch] = true
				}
				walk(ch)
			}
		}
		walk(l)
	}
	rows, err = s.db.QueryContext(ctx, `SELECT provider_id, location_type, location_id FROM provider_service_areas`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var pid, lid uint64
		var typ string
		if err := rows.Scan(&pid, &typ, &lid); err != nil {
			rows.Close()
			return nil, err
		}
		if p := ps[pid]; p != nil {
			cover(p, snap.LocationByKey(typ, lid))
		}
	}
	rows.Close()
	rows, err = s.db.QueryContext(ctx, `SELECT id, base_area_id FROM providers WHERE listing_status = 'published' AND base_area_id IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var pid, aid uint64
		if err := rows.Scan(&pid, &aid); err != nil {
			rows.Close()
			return nil, err
		}
		if p := ps[pid]; p != nil {
			if a := snap.LocationByKey(Area, aid); a != nil && a.Selectable {
				for x := a; x != nil; x = x.Parent {
					if x.Selectable {
						p.locs[x] = true
					}
				}
			}
		}
	}
	rows.Close()
	// Own radius reaching area/town centroids.
	var centroids []*Location
	for _, l := range snap.locationKey {
		if l.Selectable && l.HasCentroid && l.Type != County {
			centroids = append(centroids, l)
		}
	}
	if len(centroids) > 0 {
		rows, err = s.db.QueryContext(ctx, `SELECT g.provider_id, ST_Latitude(g.location), ST_Longitude(g.location), p.service_radius_km
			FROM provider_geo g JOIN providers p ON p.id = g.provider_id
			WHERE p.listing_status = 'published' AND p.service_radius_km > 0`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var pid uint64
			var lat, lng float64
			var r int
			if err := rows.Scan(&pid, &lat, &lng, &r); err != nil {
				rows.Close()
				return nil, err
			}
			p := ps[pid]
			if p == nil {
				continue
			}
			for _, l := range centroids {
				if haversineKm(lat, lng, l.Lat, l.Lng) <= float64(r) {
					p.locs[l] = true
				}
			}
		}
		rows.Close()
	}
	out := map[lkey]int{}
	for _, p := range ps {
		for l := range p.locs {
			for _, sid := range p.services {
				out[lkey{sid, l.Type, l.ID}]++
			}
		}
	}
	return out, nil
}

// RegisterJobs registers the daily evaluation.
func (s *Service) RegisterJobs(q *jobsqueue.Queue) {
	q.Handle(JobEvaluateLanding, func(ctx context.Context, _ json.RawMessage) error {
		n, err := s.EvaluateLanding(ctx)
		if err == nil {
			s.log.InfoContext(ctx, "landing pages evaluated", "rows", n)
		}
		return err
	})
}
