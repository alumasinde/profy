package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/alumasinde/profy/internal/app"
)

// admins returns a super administrator, an administrator and a customer.
func admins(t *testing.T, e *env) (root, admin, cust *client) {
	t.Helper()
	if _, _, err := app.CreateSuperAdmin(context.Background(), testDB, app.AdminInput{FirstName: "Root", LastName: "Admin", Email: "root@example.test"}); err != nil {
		t.Fatal(err)
	}
	root = e.client()
	root.login("root@example.test")
	admin = e.userWithRoles("admin@example.test", "administrator")
	cust = e.userWithRoles("0700000099")
	return
}

func (e *env) audits(action string) int {
	return e.count(`SELECT COUNT(*) FROM audit_logs WHERE action = ?`, action)
}

func TestSettingsBrandingContrastAndPermissions(t *testing.T) {
	e := newEnv(t)
	root, admin, cust := admins(t, e)

	cust.expect(cust.do("GET", "/api/v1/admin/settings", nil), 403)
	list := admin.expect(admin.do("GET", "/api/v1/admin/settings", nil), 200).body["data"].([]any)
	if len(list) < 20 {
		t.Fatalf("settings = %d", len(list))
	}

	admin.expect(admin.do("PATCH", "/api/v1/admin/settings", map[string]any{
		"site_name": "Fundi Hub", "site_tagline": "Local pros, near you.", "support_email": " Help@FundiHub.co.ke ",
		"support_phone": "0712 000 111", "primary_color": "#1D4ED8",
	}), 200)
	if !strings.Contains(e.app.Settings.String(context.Background(), "support_email"), "help@fundihub.co.ke") ||
		e.app.Settings.String(context.Background(), "support_phone") != "+254712000111" {
		t.Fatal("settings were not normalized")
	}
	_, _, html := cust.page("/")
	if !strings.Contains(html, "Fundi Hub") || !strings.Contains(html, "Local pros, near you.") {
		t.Fatal("homepage does not reflect new branding")
	}
	if _, _, css := cust.page("/theme.css"); !strings.Contains(css, "--color-primary:#1d4ed8") {
		t.Fatalf("theme not updated: %s", css)
	}
	if e.audits("settings.updated") != 1 {
		t.Fatal("settings change not audited")
	}

	for body, want := range map[string]string{
		`{"primary_color":"#facc15"}`:                      "contrast_too_low", // yellow behind white text
		`{"site_name":""}`:                                 "required",
		`{"support_email":"not-an-email"}`:                 "invalid",
		`{"site_url":"javascript:alert(1)"}`:               "invalid",
		`{"logo_light":"http://x.ke/logo.png"}`:            "invalid",
		`{"default_timezone":"Mars/Base"}`:                 "invalid",
		`{"maintenance_mode":true}`:                        "unknown", // separate, separately-permitted endpoint
		`{"social_links":[{"label":"X","url":"ftp://x"}]}`: "invalid",
	} {
		var m map[string]any
		_ = jsonUnmarshal(body, &m)
		r := admin.do("PATCH", "/api/v1/admin/settings", m)
		if r.status != 422 || !strings.Contains(r.raw, want) {
			t.Errorf("%s → %d %s (want %s)", body, r.status, r.raw, want)
		}
	}

	// otp_channels needs notification_channels.configure (super admin only by default).
	admin.expect(admin.do("PATCH", "/api/v1/admin/settings", map[string]any{"otp_channels": []string{"email"}}), 403)
	root.expect(root.do("PATCH", "/api/v1/admin/settings", map[string]any{"otp_channels": []string{"sms", "email"}}), 200)
}

func TestFeatureFlagsAndMaintenanceMode(t *testing.T) {
	e := newEnv(t)
	root, admin, cust := admins(t, e)

	admin.expect(admin.do("GET", "/api/v1/admin/feature-flags", nil), 200)
	admin.expect(admin.do("PATCH", "/api/v1/admin/feature-flags", map[string]any{"jobs": true}), 403)
	root.expect(root.do("PATCH", "/api/v1/admin/feature-flags", map[string]any{"jobs": true}), 200)
	root.expect(root.do("PATCH", "/api/v1/admin/feature-flags", map[string]any{"made_up": true}), 422)
	if !e.app.Settings.Flag(context.Background(), "jobs") || e.audits("feature_flags.updated") != 1 {
		t.Fatal("flag not toggled/audited")
	}

	admin.expect(admin.do("PUT", "/api/v1/admin/maintenance", map[string]any{"enabled": true}), 403)
	root.expect(root.do("PUT", "/api/v1/admin/maintenance", map[string]any{"enabled": true}), 200)
	status, h, body := cust.page("/")
	if status != 503 || h.Get("Retry-After") == "" || !strings.Contains(body, "noindex") {
		t.Fatalf("maintenance page: %d %q", status, h.Get("Retry-After"))
	}
	// Sign-in and the admin API keep working so staff can turn maintenance off.
	cust.expect(cust.do("GET", "/api/v1/auth/session", nil), 200)
	root.expect(root.do("PUT", "/api/v1/admin/maintenance", map[string]any{"enabled": false}), 200)
	if s, _, _ := cust.page("/"); s != 200 {
		t.Fatalf("after maintenance: %d", s)
	}
	if e.audits("platform.maintenance.enabled") != 1 || e.audits("platform.maintenance.disabled") != 1 {
		t.Fatal("maintenance not audited")
	}
}

func TestManagedPagesLifecyclePreviewAndRedirects(t *testing.T) {
	e := newEnv(t)
	_, admin, cust := admins(t, e)
	anon := e.client()

	pages := admin.expect(admin.do("GET", "/api/v1/admin/pages", nil), 200).body["data"].([]any)
	var aboutID string
	for _, p := range pages {
		m := p.(map[string]any)
		if m["slug"] == "about" {
			aboutID = m["id"].(string)
		}
	}
	if aboutID == "" || len(pages) != 6 {
		t.Fatalf("seeded draft pages missing: %d", len(pages))
	}
	if s, _, _ := anon.page("/about"); s != 404 {
		t.Fatalf("draft page is public: %d", s)
	}
	// Preview: only for pages.view holders; never indexable.
	if s, _, _ := anon.page("/preview/pages/" + aboutID); s != 404 {
		t.Fatal("anonymous preview allowed")
	}
	s, h, body := admin.page("/preview/pages/" + aboutID)
	if s != 200 || !strings.Contains(body, "noindex") || !strings.Contains(body, "not published") || h.Get("Cache-Control") != "no-store" {
		t.Fatalf("preview: %d", s)
	}

	cust.expect(cust.do("PATCH", "/api/v1/admin/pages/"+aboutID, map[string]any{"status": "published"}), 403)
	admin.expect(admin.do("PATCH", "/api/v1/admin/pages/"+aboutID, map[string]any{
		"title": "About Fundi Hub", "body": "## Who we are\n\nWe connect **people** with [pros](/services).\n\n<script>alert(1)</script>",
		"status": "published", "meta_description": "Who we are and how we work.",
	}), 200)
	s, _, body = anon.page("/about")
	if s != 200 || strings.Count(body, "<h1") != 1 || !strings.Contains(body, "<h2>Who we are</h2>") ||
		!strings.Contains(body, `<a href="/services">pros</a>`) || strings.Contains(body, "<script>alert") ||
		!strings.Contains(body, `<link rel="canonical" href="http://example.test/about">`) ||
		!strings.Contains(body, `content="Who we are and how we work."`) {
		t.Fatalf("published page wrong:\n%s", body)
	}

	// Slug change on a published page → 301; changing back removes the shadowing redirect.
	admin.expect(admin.do("PATCH", "/api/v1/admin/pages/"+aboutID, map[string]any{"slug": "about-us"}), 200)
	if s, h, _ := anon.page("/about"); s != 301 || h.Get("Location") != "/about-us" {
		t.Fatalf("redirect: %d %s", s, h.Get("Location"))
	}
	admin.expect(admin.do("PATCH", "/api/v1/admin/pages/"+aboutID, map[string]any{"slug": "about"}), 200)
	if s, _, _ := anon.page("/about"); s != 200 {
		t.Fatalf("changed back: %d", s)
	}
	if s, h, _ := anon.page("/about-us"); s != 301 || h.Get("Location") != "/about" {
		t.Fatalf("reverse redirect: %d %s", s, h.Get("Location"))
	}
	if e.count(`SELECT COUNT(*) FROM slug_redirects`) != 1 {
		t.Fatal("redirect chain not collapsed")
	}

	for slug, code := range map[string]string{"admin": "reserved", "Bad Slug": "invalid", "contact": "taken"} {
		r := admin.do("PATCH", "/api/v1/admin/pages/"+aboutID, map[string]any{"slug": slug})
		if r.status != 422 || !strings.Contains(r.raw, code) {
			t.Errorf("slug %q: %s", slug, r.raw)
		}
	}
	created := admin.expect(admin.do("POST", "/api/v1/admin/pages", map[string]any{"title": "Safety tips"}), 201).data()
	if created["slug"] != "safety-tips" || created["status"] != "draft" {
		t.Fatalf("created = %v", created)
	}
	if e.audits("pages.updated") < 3 || e.audits("pages.created") != 1 {
		t.Fatal("page changes not audited")
	}
}

func TestNavigationIsDatabaseManaged(t *testing.T) {
	e := newEnv(t)
	_, admin, cust := admins(t, e)
	cust.expect(cust.do("PUT", "/api/v1/admin/navigation/header", map[string]any{"items": []any{}}), 403)
	admin.expect(admin.do("PUT", "/api/v1/admin/navigation/header", map[string]any{"items": []any{
		map[string]any{"label": "How it works", "url": "/how-it-works", "is_active": true},
		map[string]any{"label": "Blog", "url": "https://blog.example.co.ke", "is_active": true},
		map[string]any{"label": "Hidden", "url": "/hidden", "is_active": false},
	}}), 200)
	admin.expect(admin.do("PUT", "/api/v1/admin/navigation/footer", map[string]any{"items": []any{
		map[string]any{"label": "Terms", "url": "/terms", "is_active": true},
	}}), 200)
	_, _, html := cust.page("/")
	if !strings.Contains(html, `<a href="/how-it-works">How it works</a>`) ||
		!strings.Contains(html, `<a href="https://blog.example.co.ke" rel="noopener noreferrer">Blog</a>`) ||
		strings.Contains(html, "Hidden") || !strings.Contains(html, `<a href="/terms">Terms</a>`) {
		t.Fatalf("navigation not rendered from data:\n%s", html)
	}
	r := admin.do("PUT", "/api/v1/admin/navigation/header", map[string]any{"items": []any{
		map[string]any{"label": "Bad", "url": "javascript:alert(1)", "is_active": true},
	}})
	if r.status != 422 {
		t.Fatalf("unsafe url accepted: %s", r.raw)
	}
	admin.expect(admin.do("GET", "/api/v1/admin/navigation/sidebar", nil), 404)
	if e.audits("navigation.updated") != 2 {
		t.Fatal("navigation changes not audited")
	}
}

func TestCategoriesServicesSynonymsLifecycle(t *testing.T) {
	e := newEnv(t)
	_, admin, cust := admins(t, e)
	mod := e.userWithRoles("mod@example.test", "moderator")

	mod.expect(mod.do("POST", "/api/v1/admin/categories", map[string]any{"name": "Plumbing"}), 403)
	cust.expect(cust.do("GET", "/api/v1/admin/categories", nil), 403)

	home := admin.expect(admin.do("POST", "/api/v1/admin/categories", map[string]any{"name": "Home Repairs"}), 201).data()
	sub := admin.expect(admin.do("POST", "/api/v1/admin/categories", map[string]any{"name": "Plumbing", "parent_id": home["id"]}), 201).data()
	if sub["slug"] != "plumbing" || sub["parent_id"] != home["id"] {
		t.Fatalf("sub = %v", sub)
	}
	// Two levels only.
	admin.expect(admin.do("POST", "/api/v1/admin/categories", map[string]any{"name": "Taps", "parent_id": sub["id"]}), 422)
	admin.expect(admin.do("POST", "/api/v1/admin/categories", map[string]any{"name": "Plumbing"}), 422) // slug taken

	svc := admin.expect(admin.do("POST", "/api/v1/admin/services", map[string]any{
		"name": "Plumbing repairs", "slug": "plumbing", "category_id": sub["id"], "requires_licence": true,
	}), 201).data()
	id := svc["id"].(string)

	// Publishing order is enforced bottom-up.
	if r := admin.do("PATCH", "/api/v1/admin/services/"+id, map[string]any{"status": "published"}); r.status != 409 || r.errCode() != "parent_not_published" {
		t.Fatalf("service publish before category: %s", r.raw)
	}
	admin.expect(admin.do("PATCH", "/api/v1/admin/categories/"+sub["id"].(string), map[string]any{"status": "published"}), 409)
	admin.expect(admin.do("PATCH", "/api/v1/admin/categories/"+home["id"].(string), map[string]any{"status": "published"}), 200)
	admin.expect(admin.do("PATCH", "/api/v1/admin/categories/"+sub["id"].(string), map[string]any{"status": "published"}), 200)
	admin.expect(admin.do("PATCH", "/api/v1/admin/services/"+id, map[string]any{"status": "published"}), 200)
	// …and so is unpublishing/archiving top-down.
	if r := admin.do("PATCH", "/api/v1/admin/categories/"+sub["id"].(string), map[string]any{"status": "archived"}); r.errCode() != "has_dependents" {
		t.Fatalf("archive with live service: %s", r.raw)
	}
	admin.expect(admin.do("PATCH", "/api/v1/admin/categories/"+home["id"].(string), map[string]any{"status": "draft"}), 409)

	// Synonyms: normalized and de-duplicated, searchable.
	got := admin.expect(admin.do("PUT", "/api/v1/admin/services/"+id+"/synonyms", map[string]any{
		"synonyms": []string{"Fundi wa maji", "  fundi WA  maji ", "Plumber"},
	}), 200).data()
	if syn := got["synonyms"].([]any); len(syn) != 2 {
		t.Fatalf("synonyms = %v", syn)
	}
	found := admin.expect(admin.do("GET", "/api/v1/admin/services?q=fundi", nil), 200).body["data"].([]any)
	if len(found) != 1 {
		t.Fatalf("synonym search found %d", len(found))
	}

	// Slug change after publication → 301 from the old public URL.
	admin.expect(admin.do("PATCH", "/api/v1/admin/services/"+id, map[string]any{"slug": "plumbing-repairs"}), 200)
	if s, h, _ := cust.page("/services/plumbing"); s != 301 || h.Get("Location") != "/services/plumbing-repairs" {
		t.Fatalf("service redirect: %d %s", s, h.Get("Location"))
	}
	cats := admin.expect(admin.do("GET", "/api/v1/admin/categories", nil), 200).body["data"].([]any)
	for _, c := range cats {
		m := c.(map[string]any)
		if m["slug"] == "plumbing" && m["service_count"].(float64) != 1 {
			t.Fatalf("service_count = %v", m["service_count"])
		}
	}
	for _, a := range []string{"categories.created", "categories.updated", "services.created", "services.updated", "services.synonyms.updated"} {
		if e.audits(a) == 0 {
			t.Errorf("%s not audited", a)
		}
	}
}

func TestLocationsHierarchyAliasesCentroidsAndMerge(t *testing.T) {
	e := newEnv(t)
	_, admin, cust := admins(t, e)
	cust.expect(cust.do("GET", "/api/v1/admin/locations/counties", nil), 403)

	counties := admin.expect(admin.do("GET", "/api/v1/admin/locations/counties?limit=100", nil), 200).body["data"].([]any)
	if len(counties) != 47 {
		t.Fatalf("counties = %d", len(counties))
	}
	ids := map[string]string{}
	for _, c := range counties {
		m := c.(map[string]any)
		ids[m["slug"].(string)] = m["id"].(string)
	}
	admin.expect(admin.do("PATCH", "/api/v1/admin/locations/counties/"+ids["kiambu"], map[string]any{"name": "Kiambuu"}), 422)
	admin.expect(admin.do("PATCH", "/api/v1/admin/locations/counties/"+ids["kiambu"], map[string]any{"is_indexable": true}), 200)

	ruaka := admin.expect(admin.do("POST", "/api/v1/admin/locations/towns", map[string]any{"name": "Ruaka", "parent_id": ids["kiambu"], "is_selectable": true}), 201).data()
	dup := admin.expect(admin.do("POST", "/api/v1/admin/locations/towns", map[string]any{"name": "Ruaka", "parent_id": ids["nairobi"]}), 201).data()
	if ruaka["slug"] != "ruaka" || dup["slug"] != "ruaka-nairobi" {
		t.Fatalf("slug disambiguation: %v / %v", ruaka["slug"], dup["slug"])
	}
	// The namespace is shared with counties.
	k := admin.expect(admin.do("POST", "/api/v1/admin/locations/towns", map[string]any{"name": "Kiambu", "parent_id": ids["kiambu"]}), 201).data()
	if k["slug"] != "kiambu-kiambu" {
		t.Fatalf("county slug clash not resolved: %v", k["slug"])
	}
	area := admin.expect(admin.do("POST", "/api/v1/admin/locations/areas", map[string]any{"name": "Kahawa Sukari", "parent_id": dup["id"]}), 201).data()

	// Aliases resolve to exactly one location.
	admin.expect(admin.do("PUT", "/api/v1/admin/locations/towns/"+ruaka["id"].(string)+"/aliases", map[string]any{"aliases": []string{"Ruaka Town", "Two Rivers"}}), 200)
	r := admin.do("PUT", "/api/v1/admin/locations/towns/"+dup["id"].(string)+"/aliases", map[string]any{"aliases": []string{"ruaka  town"}})
	if r.status != 422 || !strings.Contains(r.raw, "taken") {
		t.Fatalf("duplicate alias accepted: %s", r.raw)
	}

	// Centroid round trip: axis order must survive ST_SRID(POINT(lng, lat), 4326).
	got := admin.expect(admin.do("PUT", "/api/v1/admin/locations/towns/"+ruaka["id"].(string)+"/centroid", map[string]any{"latitude": -1.2864, "longitude": 36.8172}), 200).data()
	c := got["centroid"].(map[string]any)
	if c["latitude"].(float64) != -1.2864 || c["longitude"].(float64) != 36.8172 {
		t.Fatalf("centroid = %v", c)
	}
	var lat, lng float64
	if err := testDB.QueryRow(`SELECT ST_Latitude(centroid), ST_Longitude(centroid) FROM location_geo WHERE location_type='town'`).Scan(&lat, &lng); err != nil || lat != -1.2864 || lng != 36.8172 {
		t.Fatalf("stored axis order wrong: %v %v %v", lat, lng, err)
	}
	var dist float64 // Nairobi CBD → JKIA is roughly 15 km; a swapped axis would be thousands of km.
	_ = testDB.QueryRow(`SELECT ST_Distance_Sphere(centroid, ST_SRID(POINT(36.9278, -1.3192), 4326)) FROM location_geo WHERE location_type='town'`).Scan(&dist)
	if dist < 10_000 || dist > 20_000 {
		t.Fatalf("distance %.0f m — axis order or SRID broken", dist)
	}
	admin.expect(admin.do("PUT", "/api/v1/admin/locations/towns/"+ruaka["id"].(string)+"/centroid", map[string]any{"latitude": 51.5, "longitude": -0.12}), 422)

	// Archiving with active children is refused; merging moves them.
	admin.expect(admin.do("PATCH", "/api/v1/admin/locations/towns/"+dup["id"].(string), map[string]any{"status": "archived"}), 409)
	merged := admin.expect(admin.do("POST", "/api/v1/admin/locations/merge", map[string]any{
		"type": "town", "source_id": dup["id"], "target_id": ruaka["id"],
	}), 200).data()
	if merged["child_count"].(float64) != 1 {
		t.Fatalf("children not moved: %v", merged)
	}
	src := admin.expect(admin.do("GET", "/api/v1/admin/locations/towns/"+dup["id"].(string), nil), 200).data()
	if src["status"] != "archived" || src["merged_into_id"] != ruaka["id"] {
		t.Fatalf("source = %v", src)
	}
	movedArea := admin.expect(admin.do("GET", "/api/v1/admin/locations/areas/"+area["id"].(string), nil), 200).data()
	if movedArea["parent_id"] != ruaka["id"] {
		t.Fatal("area not re-parented")
	}
	if s, h, _ := cust.page("/locations/ruaka-nairobi"); s != 301 || h.Get("Location") != "/locations/ruaka" {
		t.Fatalf("merge redirect: %d %s", s, h.Get("Location"))
	}
	admin.expect(admin.do("PATCH", "/api/v1/admin/locations/towns/"+dup["id"].(string), map[string]any{"name": "X"}), 409)

	// Slug rename keeps the namespace unique and redirects.
	admin.expect(admin.do("PATCH", "/api/v1/admin/locations/towns/"+ruaka["id"].(string), map[string]any{"slug": "nairobi"}), 422)
	admin.expect(admin.do("PATCH", "/api/v1/admin/locations/towns/"+ruaka["id"].(string), map[string]any{"slug": "ruaka-town"}), 200)
	if s, h, _ := cust.page("/locations/ruaka-nairobi"); s != 301 || h.Get("Location") != "/locations/ruaka-town" {
		t.Fatalf("chain not collapsed: %d %s", s, h.Get("Location"))
	}

	// Search by alias.
	found := admin.expect(admin.do("GET", "/api/v1/admin/locations/towns?q=two%20rivers", nil), 200).body["data"].([]any)
	if len(found) != 1 {
		t.Fatalf("alias search = %d", len(found))
	}
	for _, a := range []string{"locations.created", "locations.updated", "locations.aliases.updated", "locations.centroid.updated", "locations.merged"} {
		if e.audits(a) == 0 {
			t.Errorf("%s not audited", a)
		}
	}
}
