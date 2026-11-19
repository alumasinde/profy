package integration

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alumasinde/profy/internal/database"
	"github.com/alumasinde/profy/internal/jobsqueue"
)

func TestMigrationsRollBackAndReapply(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := database.MigrateDown(ctx, testDB, log); err != nil {
		t.Fatal(err)
	}
	if ok, _ := database.MigrationsCurrent(ctx, testDB); ok {
		t.Fatal("expected a pending migration after down")
	}
	if err := database.MigrateUp(ctx, testDB, log); err != nil {
		t.Fatal(err)
	}
	if err := database.ApplySeeds(ctx, testDB, log); err != nil { // idempotent
		t.Fatal(err)
	}
	if n := e.count(`SELECT COUNT(*) FROM counties`); n != 47 {
		t.Fatalf("counties = %d", n)
	}
	if n := e.count(`SELECT COUNT(*) FROM counties WHERE is_selectable = 1`); n != 2 {
		t.Fatalf("pilot counties = %d", n)
	}
}

func TestSeedsDoNotOverwriteAdminChanges(t *testing.T) {
	e := newEnv(t)
	e.exec(`UPDATE site_settings SET value_json = JSON_QUOTE('Rebranded') WHERE setting_key = 'site_name'`)
	if err := database.ApplySeeds(context.Background(), testDB, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	var name string
	_ = testDB.QueryRow(`SELECT value_json->>'$' FROM site_settings WHERE setting_key='site_name'`).Scan(&name)
	if name != "Rebranded" {
		t.Fatalf("seed overwrote admin setting: %q", name)
	}
	// Branding is data: the homepage reflects it without a rebuild.
	e.app.Settings.Invalidate()
	res, err := http.Get(e.srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(b), "Rebranded") {
		t.Fatal("homepage does not use configured site_name")
	}
}

func TestPublicPagesSEOBasics(t *testing.T) {
	e := newEnv(t)
	get := func(path string) (*http.Response, string) {
		c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		res, err := c.Get(e.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		return res, string(b)
	}
	res, html := get("/")
	if res.StatusCode != 200 || strings.Count(html, "<h1") != 1 || !strings.Contains(html, `<link rel="canonical" href="http://example.test/">`) ||
		!strings.Contains(html, `<meta name="robots" content="index,follow">`) || !strings.Contains(html, `og:title`) {
		t.Fatalf("homepage SEO basics missing:\n%s", html)
	}
	if csp := res.Header.Get("Content-Security-Policy"); strings.Contains(csp, "unsafe-inline") || !strings.Contains(csp, "script-src 'self'") {
		t.Fatalf("CSP = %q", csp)
	}
	if res, _ := get("/does-not-exist"); res.StatusCode != 404 || res.Header.Get("X-Robots-Tag") == "" {
		t.Fatalf("404 page: %d %q", res.StatusCode, res.Header.Get("X-Robots-Tag"))
	}
	if res, _ := get("/How-It-Works/"); res.StatusCode != 301 || res.Header.Get("Location") != "/how-it-works" {
		t.Fatalf("canonical redirect: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	if res, _ := get("/admin/users"); !strings.Contains(res.Header.Get("X-Robots-Tag"), "noindex") {
		t.Fatal("application shell must be noindex")
	}
	if _, body := get("/robots.txt"); !strings.Contains(body, "Disallow: /admin/") {
		t.Fatal("robots.txt must disallow private areas")
	}
	if res, body := get("/readyz"); res.StatusCode != 200 || !strings.Contains(body, "ready") {
		t.Fatalf("readyz = %d %s", res.StatusCode, body)
	}
	if _, body := get("/theme.css"); !strings.Contains(body, "--color-primary:#") {
		t.Fatal("theme variables missing")
	}
}

func TestOutboxRetriesBackoffDedupeAndFailure(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	q := jobsqueue.New(testDB, slog.New(slog.NewTextHandler(io.Discard, nil)))
	var ok, flaky atomic.Int32
	q.Handle("test.ok", func(ctx context.Context, p json.RawMessage) error { ok.Add(1); return nil })
	q.Handle("test.fail", func(ctx context.Context, p json.RawMessage) error { flaky.Add(1); return errors.New("boom") })

	if err := jobsqueue.Enqueue(ctx, testDB, "test.ok", map[string]int{"n": 1}, jobsqueue.Options{DedupeKey: "once"}); err != nil {
		t.Fatal(err)
	}
	if err := jobsqueue.Enqueue(ctx, testDB, "test.ok", map[string]int{"n": 2}, jobsqueue.Options{DedupeKey: "once"}); err != nil {
		t.Fatal(err)
	}
	if err := jobsqueue.Enqueue(ctx, testDB, "test.fail", nil, jobsqueue.Options{MaxAttempts: 2}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := q.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
		e.exec(`UPDATE background_jobs SET run_at = NOW(3) - INTERVAL 1 SECOND WHERE status = 'pending'`) // skip backoff
	}
	if ok.Load() != 1 {
		t.Fatalf("dedupe failed: ran %d times", ok.Load())
	}
	if flaky.Load() != 2 {
		t.Fatalf("expected 2 attempts, got %d", flaky.Load())
	}
	if e.count(`SELECT COUNT(*) FROM background_jobs WHERE kind='test.fail' AND status='failed' AND last_error='boom'`) != 1 {
		t.Fatal("failed job not marked failed")
	}
	if jobsqueue.Backoff(1) <= 0 || jobsqueue.Backoff(3) <= jobsqueue.Backoff(2) {
		t.Fatal("backoff must grow")
	}
}
