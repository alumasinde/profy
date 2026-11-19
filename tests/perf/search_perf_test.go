//go:build perf

// Package perf measures search and public-page latency at launch scale (plan §19.1:
// search p95 ≤ 500 ms with 10,000 providers; public pages p95 ≤ 300 ms) and records
// EXPLAIN ANALYZE plans for the critical queries (§10.6, §14.2).
//
// It needs a disposable database: PERF_DATABASE_DSN. The data is synthetic test data
// in that database only; nothing here is ever loaded into a real environment.
//
//	PERF_DATABASE_DSN='user:pass@tcp(127.0.0.1:3306)/hudumalink_perf' go test -tags perf -run Perf -v ./tests/perf
package perf

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/alumasinde/profy/internal/app"
	"github.com/alumasinde/profy/internal/config"
	"github.com/alumasinde/profy/internal/database"
)

const (
	nProviders = 10000
	nTowns     = 24
	nAreas     = 240
	nServices  = 40
)

func TestPerfSearch(t *testing.T) {
	dsn := os.Getenv("PERF_DATABASE_DSN")
	if dsn == "" {
		t.Skip("PERF_DATABASE_DSN not set")
	}
	ctx := context.Background()
	cfg := &config.Config{Env: config.EnvLocal, SiteURL: "http://perf.test", DatabaseDSN: dsn, DBMaxOpenConns: 20, DBMaxIdleConns: 10,
		DBConnMaxLife: time.Minute, DBQueryTimeout: 10 * time.Second, OTPHMACKey: []byte(strings.Repeat("p", 48)),
		SessionIdleTTL: time.Hour, SessionAbsoluteTTL: 24 * time.Hour, EmailAdapter: "fake", SMSAdapter: "fake",
		WorkerConcurrency: 1, WorkerPollEvery: time.Second, MediaDir: t.TempDir()}
	db, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(database.MigrateReset(ctx, db))
	must(database.MigrateUp(ctx, db, log))
	must(app.Seed(ctx, db, log))

	start := time.Now()
	seed(t, db)
	t.Logf("seeded %d providers in %s", nProviders, time.Since(start).Round(time.Millisecond))
	a, err := app.Build(cfg, log, db)
	must(err)
	start = time.Now()
	n, err := a.Discovery.EvaluateLanding(ctx)
	must(err)
	t.Logf("landing evaluation: %d rows in %s", n, time.Since(start).Round(time.Millisecond))
	srv := httptest.NewServer(a.Handler)
	defer srv.Close()

	rnd := rand.New(rand.NewSource(7))
	measure := func(name string, urls func() string, budget time.Duration) {
		var d []time.Duration
		for i := 0; i < 200; i++ {
			u := urls()
			s := time.Now()
			res, err := http.Get(srv.URL + u)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, res.Body)
			res.Body.Close()
			if res.StatusCode != 200 {
				t.Fatalf("%s %s = %d", name, u, res.StatusCode)
			}
			d = append(d, time.Since(s))
		}
		sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
		p50, p95 := d[len(d)/2], d[len(d)*95/100]
		t.Logf("%-28s p50 %6s  p95 %6s  (budget %s)", name, p50.Round(100*time.Microsecond), p95.Round(100*time.Microsecond), budget)
		if p95 > budget {
			t.Errorf("%s p95 %s exceeds %s", name, p95, budget)
		}
	}
	measure("API search area", func() string {
		return fmt.Sprintf("/api/v1/providers/search?q=service-%d&location=area-%d", rnd.Intn(nServices), rnd.Intn(nAreas))
	}, 500*time.Millisecond)
	measure("API search town", func() string {
		return fmt.Sprintf("/api/v1/providers/search?q=service-%d&location=town-%d", rnd.Intn(nServices), rnd.Intn(nTowns))
	}, 500*time.Millisecond)
	measure("API search county", func() string {
		return fmt.Sprintf("/api/v1/providers/search?q=service-%d&location=nairobi", rnd.Intn(nServices))
	}, 500*time.Millisecond)
	measure("API search near me 10km", func() string {
		return fmt.Sprintf("/api/v1/providers/search?q=service-%d&lat=%.4f&lng=%.4f&radius_km=10", rnd.Intn(nServices),
			-1.28+rnd.Float64()*0.1, 36.75+rnd.Float64()*0.15)
	}, 500*time.Millisecond)
	measure("landing page (HTML)", func() string {
		return fmt.Sprintf("/services/service-%d/area-%d", rnd.Intn(nServices), rnd.Intn(nAreas))
	}, 300*time.Millisecond)
	measure("service page (HTML)", func() string { return fmt.Sprintf("/services/service-%d", rnd.Intn(nServices)) }, 300*time.Millisecond)
	measure("location page (HTML)", func() string { return fmt.Sprintf("/locations/town-%d", rnd.Intn(nTowns)) }, 300*time.Millisecond)
	measure("homepage (HTML)", func() string { return "/" }, 300*time.Millisecond)
	measure("provider profile (HTML)", func() string { return fmt.Sprintf("/professionals/pro-%d", 1+rnd.Intn(nProviders)) }, 300*time.Millisecond)

	// Plans for the critical queries.
	if out := os.Getenv("PERF_PLANS_OUT"); out != "" {
		var b strings.Builder
		for _, q := range []struct{ name, sql string }{
			{"coverage match (area keys)", `SELECT psa.provider_id, psa.location_id FROM provider_service_areas psa
				JOIN providers p ON p.id = psa.provider_id AND p.listing_status = 'published'
				WHERE psa.location_type = 'area' AND psa.location_id IN (1,2,3,4,5,6,7,8,9,10)`},
			{"base area match", `SELECT id, base_area_id FROM providers WHERE listing_status = 'published' AND base_area_id IN (1,2,3,4,5,6,7,8,9,10)`},
			{"spatial candidates (10 km box)", `SELECT pg.provider_id, ST_Distance_Sphere(pg.location, ST_SRID(POINT(36.8, -1.28), 4326)), COALESCE(p.service_radius_km, 0)
				FROM provider_geo pg JOIN providers p ON p.id = pg.provider_id
				WHERE MBRContains(ST_GeomFromText('POLYGON((36.35 -1.73,37.25 -1.73,37.25 -0.83,36.35 -0.83,36.35 -1.73))', 4326, 'axis-order=long-lat'), pg.location)
				AND p.listing_status = 'published'`},
			{"offerings for candidates", `SELECT provider_id, service_id FROM provider_services WHERE status = 'active' AND service_id IN (1,2) AND provider_id IN (1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20)`},
			{"offerings, no location", `SELECT ps.provider_id, ps.service_id FROM provider_services ps JOIN providers p ON p.id = ps.provider_id AND p.listing_status = 'published' WHERE ps.status = 'active' AND ps.service_id IN (1) LIMIT 20000`},
			{"landing state lookup", `SELECT id, robots_mode, indexed, provider_count FROM seo_landing_pages WHERE service_id = 1 AND location_type = 'area' AND location_id = 1`},
		} {
			var plan string
			if err := db.QueryRowContext(ctx, "EXPLAIN ANALYZE "+q.sql).Scan(&plan); err != nil {
				t.Fatalf("%s: %v", q.name, err)
			}
			fmt.Fprintf(&b, "### %s\n\n```sql\n%s\n```\n\n```text\n%s\n```\n\n", q.name, strings.Join(strings.Fields(q.sql), " "), plan)
		}
		must(os.WriteFile(out, []byte(b.String()), 0o644))
	}
}

// seed creates synthetic, published inventory directly in SQL.
func seed(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	rnd := rand.New(rand.NewSource(42))
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%v\n%.200s", err, q)
		}
	}
	bulk := func(prefix string, rows []string) {
		for len(rows) > 0 {
			n := min(len(rows), 1000)
			exec(prefix + strings.Join(rows[:n], ","))
			rows = rows[n:]
		}
	}
	var cats []string
	for i := 0; i < 8; i++ {
		cats = append(cats, fmt.Sprintf("(UUID_TO_BIN(UUID(),1),'cat-%d','Category %d','published',NOW())", i, i))
	}
	bulk("INSERT INTO categories (public_id, slug, name, status, published_at) VALUES ", cats)
	var svcs []string
	for i := 0; i < nServices; i++ {
		svcs = append(svcs, fmt.Sprintf("(UUID_TO_BIN(UUID(),1),(SELECT id FROM categories WHERE slug='cat-%d'),'service-%d','Service %d','published',NOW())", i%8, i, i))
	}
	for _, v := range svcs {
		exec("INSERT INTO services (public_id, category_id, slug, name, status, published_at) VALUES " + v)
	}
	var nairobi, kiambu uint64
	_ = db.QueryRowContext(ctx, "SELECT id FROM counties WHERE slug='nairobi'").Scan(&nairobi)
	_ = db.QueryRowContext(ctx, "SELECT id FROM counties WHERE slug='kiambu'").Scan(&kiambu)
	exec("UPDATE counties SET is_selectable = 1, is_indexable = 1 WHERE id IN (?, ?)", nairobi, kiambu)
	var towns []string
	for i := 0; i < nTowns; i++ {
		c := nairobi
		if i%3 == 0 {
			c = kiambu
		}
		towns = append(towns, fmt.Sprintf("(UUID_TO_BIN(UUID(),1),%d,'Town %d','town-%d',1,1)", c, i, i))
	}
	bulk("INSERT INTO towns (public_id, county_id, name, slug, is_selectable, is_indexable) VALUES ", towns)
	var areas, geo []string
	for i := 0; i < nAreas; i++ {
		areas = append(areas, fmt.Sprintf("(UUID_TO_BIN(UUID(),1),(SELECT id FROM towns WHERE slug='town-%d'),'Area %d','area-%d',1,1)", i%nTowns, i, i))
	}
	for _, a := range areas {
		exec("INSERT INTO areas (public_id, town_id, name, slug, is_selectable, is_indexable) VALUES " + a)
	}
	exec(`INSERT INTO location_slugs (slug, location_type, location_id) SELECT slug, 'town', id FROM towns`)
	exec(`INSERT INTO location_slugs (slug, location_type, location_id) SELECT slug, 'area', id FROM areas`)
	for i := 1; i <= nAreas; i++ {
		geo = append(geo, fmt.Sprintf("('area',%d,ST_SRID(POINT(%.5f,%.5f),4326))", i, 36.70+rnd.Float64()*0.25, -1.36+rnd.Float64()*0.2))
	}
	bulk("INSERT INTO location_geo (location_type, location_id, centroid) VALUES ", geo)

	var provs []string
	for i := 1; i <= nProviders; i++ {
		radius := "NULL"
		if rnd.Intn(10) < 4 {
			radius = fmt.Sprint(5 + rnd.Intn(26))
		}
		provs = append(provs, fmt.Sprintf("(UUID_TO_BIN(UUID(),1),'individual','Pro %d','pro-%d',%d,%s,'published',%d,DATE_SUB(NOW(), INTERVAL %d DAY))",
			i, i, 1+rnd.Intn(nAreas), radius, 30+rnd.Intn(71), rnd.Intn(400)))
	}
	bulk("INSERT INTO providers (public_id, provider_type, display_name, slug, base_area_id, service_radius_km, listing_status, completeness_score, published_at) VALUES ", provs)
	var offers, cover, pins []string
	for i := 1; i <= nProviders; i++ {
		seen := map[int]bool{}
		for k := 0; k < 1+rnd.Intn(3); k++ {
			sv := 1 + rnd.Intn(nServices)
			if !seen[sv] {
				seen[sv] = true
				offers = append(offers, fmt.Sprintf("(UUID_TO_BIN(UUID(),1),%d,%d,'from',%d,'active')", i, sv, 500+rnd.Intn(20)*250))
			}
		}
		cseen := map[string]bool{}
		for k := 0; k < 1+rnd.Intn(4); k++ {
			var row string
			switch x := rnd.Intn(100); {
			case x < 60:
				row = fmt.Sprintf("(%d,'area',%d)", i, 1+rnd.Intn(nAreas))
			case x < 85:
				row = fmt.Sprintf("(%d,'town',%d)", i, 1+rnd.Intn(nTowns))
			default:
				c := nairobi
				if rnd.Intn(3) == 0 {
					c = kiambu
				}
				row = fmt.Sprintf("(%d,'county',%d)", i, c)
			}
			if !cseen[row] {
				cseen[row] = true
				cover = append(cover, row)
			}
		}
		if rnd.Intn(10) < 4 {
			pins = append(pins, fmt.Sprintf("(%d,ST_SRID(POINT(%.5f,%.5f),4326),'provider_pin')", i, 36.70+rnd.Float64()*0.25, -1.36+rnd.Float64()*0.2))
		}
	}
	bulk("INSERT INTO provider_services (public_id, provider_id, service_id, pricing_type, price_from, status) VALUES ", offers)
	bulk("INSERT INTO provider_service_areas (provider_id, location_type, location_id) VALUES ", cover)
	bulk("INSERT INTO provider_geo (provider_id, location, source) VALUES ", pins)
	exec("ANALYZE TABLE providers, provider_services, provider_service_areas, provider_geo")
}
