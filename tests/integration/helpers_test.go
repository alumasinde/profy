// Package integration runs API tests against a real MySQL 8 database (plan §20.1).
// Set TEST_DATABASE_DSN to a dedicated, disposable database; the tests reset it.
package integration

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/alumasinde/profy/internal/app"
	"github.com/alumasinde/profy/internal/config"
	"github.com/alumasinde/profy/internal/database"
	"github.com/alumasinde/profy/internal/modules/notifications"
)

var testDB *sql.DB

func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		// Unit tests elsewhere still run; these need MySQL.
		os.Exit(0)
	}
	cfg := testConfig(dsn)
	db, err := database.Open(context.Background(), cfg)
	if err != nil {
		panic(err)
	}
	testDB = db
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := database.MigrateReset(ctx, db); err != nil {
		panic(err)
	}
	if err := database.MigrateUp(ctx, db, log); err != nil {
		panic(err)
	}
	if err := app.Seed(ctx, db, log); err != nil {
		panic(err)
	}
	code := m.Run()
	db.Close()
	os.Exit(code)
}

func testConfig(dsn string) *config.Config {
	return &config.Config{
		Env: config.EnvLocal, HTTPAddr: "127.0.0.1:0", SiteURL: "http://example.test", DatabaseDSN: dsn,
		DBMaxOpenConns: 10, DBMaxIdleConns: 5, DBConnMaxLife: time.Minute, DBQueryTimeout: 5 * time.Second,
		OTPHMACKey: []byte(strings.Repeat("t", 48)), SessionIdleTTL: time.Hour, SessionAbsoluteTTL: 24 * time.Hour,
		EmailAdapter: "fake", SMSAdapter: "fake", WorkerConcurrency: 1, WorkerPollEvery: 50 * time.Millisecond,
		ShutdownTimeout: time.Second,
	}
}

type env struct {
	t   *testing.T
	app *app.App
	srv *httptest.Server
}

// newEnv truncates mutable tables and builds a fresh app (fresh in-memory rate limiters).
func newEnv(t *testing.T) *env {
	t.Helper()
	if testDB == nil {
		t.Skip("TEST_DATABASE_DSN not set")
	}
	ctx := context.Background()
	conn, err := testDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stmts := []string{"SET FOREIGN_KEY_CHECKS = 0"}
	for _, tbl := range []string{"otp_challenges", "user_sessions", "user_devices", "user_credentials", "user_consents",
		"user_roles", "audit_logs", "background_jobs", "users",
		"site_settings", "feature_flags", "managed_pages", "navigation_items", "slug_redirects",
		"service_synonyms", "services", "categories", "location_aliases", "location_geo", "areas", "towns",
		"providers", "provider_status_history", "provider_contacts", "provider_services", "provider_service_areas",
		"provider_geo", "provider_portfolio", "provider_verifications", "verification_documents", "provider_claims",
		"moderation_actions", "analytics_events", "media_files", "seo_landing_pages", "demand_requests"} {
		stmts = append(stmts, "TRUNCATE TABLE "+tbl)
	}
	stmts = append(stmts, "DELETE FROM location_slugs WHERE location_type <> 'county'",
		"UPDATE counties SET is_selectable = (slug IN ('nairobi','kiambu')), is_indexable = 0, meta_title = NULL, meta_description = NULL",
		"SET FOREIGN_KEY_CHECKS = 1")
	for _, s := range stmts {
		if _, err := conn.ExecContext(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	conn.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := app.Seed(ctx, testDB, log); err != nil { // restore default settings, flags and draft pages
		t.Fatal(err)
	}
	cfg := testConfig(os.Getenv("TEST_DATABASE_DSN"))
	cfg.MediaDir = t.TempDir()
	a, err := app.Build(cfg, log, testDB)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.Handler)
	t.Cleanup(func() { a.Auth.WaitDeliveries(); a.Providers.WaitDeliveries(); srv.Close() })
	return &env{t: t, app: a, srv: srv}
}

var sixDigits = regexp.MustCompile(`\b(\d{6})\b`)

// lastCode returns the most recent OTP delivered to a destination by the fake adapters.
func (e *env) lastCode(dest string) string {
	e.t.Helper()
	e.app.Auth.WaitDeliveries()
	for _, ch := range []string{"sms", "email"} {
		if f, ok := e.app.Senders[ch].(*notifications.FakeSender); ok {
			if m, ok := f.Last(dest); ok {
				if c := sixDigits.FindStringSubmatch(m.Body); c != nil {
					return c[1]
				}
			}
		}
	}
	e.t.Fatalf("no code delivered to %s", dest)
	return ""
}

func (e *env) sentCount(dest string) int {
	e.app.Auth.WaitDeliveries()
	n := 0
	for _, ch := range []string{"sms", "email"} {
		if f, ok := e.app.Senders[ch].(*notifications.FakeSender); ok {
			n += f.Count(dest)
		}
	}
	return n
}

type client struct {
	e    *env
	http *http.Client
}

func (e *env) client() *client {
	jar, _ := cookiejar.New(nil)
	c := &client{e: e, http: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}}
	c.do("GET", "/api/v1/auth/session", nil) // obtains the CSRF cookie
	return c
}

func (c *client) csrf() string {
	u, _ := url.Parse(c.e.srv.URL)
	for _, ck := range c.http.Jar.Cookies(u) {
		if ck.Name == "hl_csrf" {
			return ck.Value
		}
	}
	return ""
}

type resp struct {
	status int
	header http.Header
	body   map[string]any
	raw    string
}

func (r resp) data() map[string]any {
	m, _ := r.body["data"].(map[string]any)
	return m
}

func (r resp) errCode() string {
	if e, ok := r.body["error"].(map[string]any); ok {
		s, _ := e["code"].(string)
		return s
	}
	return ""
}

func (c *client) do(method, path string, body any, headers ...string) resp {
	c.e.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.e.srv.URL+path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != "GET" {
		req.Header.Set("X-CSRF-Token", c.csrf())
	}
	for i := 0; i+1 < len(headers); i += 2 {
		if headers[i+1] == "" {
			req.Header.Del(headers[i])
		} else {
			req.Header.Set(headers[i], headers[i+1])
		}
	}
	res, err := c.http.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := resp{status: res.StatusCode, header: res.Header, raw: string(raw)}
	_ = json.Unmarshal(raw, &out.body)
	return out
}

func (c *client) expect(r resp, status int) resp {
	c.e.t.Helper()
	if r.status != status {
		c.e.t.Fatalf("status %d, want %d: %s", r.status, status, r.raw)
	}
	return r
}

// register creates and signs in a customer, returning the normalized destination.
func (c *client) register(first, last, dest string) {
	c.e.t.Helper()
	r := c.expect(c.do("POST", "/api/v1/auth/register", map[string]any{
		"first_name": first, "last_name": last, "destination": dest, "accept_terms": true,
	}), http.StatusAccepted)
	id := r.data()["challenge_id"].(string)
	c.expect(c.do("POST", "/api/v1/auth/otp/verify", map[string]any{
		"challenge_id": id, "code": c.e.lastCode(normalize(dest)),
	}), http.StatusOK)
}

// login signs in with an OTP.
func (c *client) login(dest string) {
	c.e.t.Helper()
	r := c.expect(c.do("POST", "/api/v1/auth/otp/send", map[string]any{"destination": dest, "purpose": "login"}), http.StatusAccepted)
	c.expect(c.do("POST", "/api/v1/auth/otp/verify", map[string]any{
		"challenge_id": r.data()["challenge_id"], "code": c.e.lastCode(normalize(dest)),
	}), http.StatusOK)
}

func normalize(dest string) string {
	if strings.Contains(dest, "@") {
		return strings.ToLower(strings.TrimSpace(dest))
	}
	d := strings.NewReplacer(" ", "", "-", "").Replace(dest)
	switch {
	case strings.HasPrefix(d, "+254"):
		return d
	case strings.HasPrefix(d, "254"):
		return "+" + d
	default:
		return "+254" + d[1:]
	}
}

func (e *env) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := testDB.Exec(q, args...); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := testDB.QueryRow(q, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// userWithRoles registers a user and grants roles directly (test setup only).
func (e *env) userWithRoles(dest string, roles ...string) *client {
	e.t.Helper()
	c := e.client()
	c.register("Test", "User", dest)
	for _, r := range roles {
		e.exec(`INSERT IGNORE INTO user_roles (user_id, role_id) SELECT u.id, r.id FROM users u, roles r
			WHERE (u.email = ? OR u.phone_e164 = ?) AND r.role_key = ?`, normalize(dest), normalize(dest), r)
	}
	e.app.Authz.Invalidate()
	return c
}

// get fetches a public URL without following redirects.
func (c *client) page(path string) (int, http.Header, string) {
	c.e.t.Helper()
	req, _ := http.NewRequest("GET", c.e.srv.URL+path, nil)
	res, err := c.http.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, string(b)
}

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }

func jsonEncode(w io.Writer, v any) error { return json.NewEncoder(w).Encode(v) }
