package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/alumasinde/profy/internal/jobsqueue"
	"github.com/alumasinde/profy/internal/modules/discovery"
)

// world is a small marketplace: Nairobi › Westlands (Parklands, Highridge) and
// Nairobi › Kilimani (Yaya); Kiambu › Ruaka (Ruaka Town). Plumbing and Electrical services.
type world struct {
	admin                                 *client
	plumbing, electrical, cleaning        string // service slugs
	plumbingID                            string
	nairobi, westlands, kilimani, ruaka   map[string]any
	parklands, highridge, yaya, ruakaTown map[string]any
	next                                  int
}

func newWorld(t *testing.T, e *env) *world {
	t.Helper()
	_, admin, _ := admins(t, e)
	w := &world{admin: admin}
	cat := admin.expect(admin.do("POST", "/api/v1/admin/categories", map[string]any{"name": "Home Repairs"}), 201).data()
	admin.expect(admin.do("PATCH", "/api/v1/admin/categories/"+cat["id"].(string), map[string]any{"status": "published"}), 200)
	mk := func(name string) map[string]any {
		s := admin.expect(admin.do("POST", "/api/v1/admin/services", map[string]any{"name": name, "category_id": cat["id"]}), 201).data()
		admin.expect(admin.do("PATCH", "/api/v1/admin/services/"+s["id"].(string), map[string]any{"status": "published"}), 200)
		return s
	}
	p := mk("Plumbing")
	w.plumbing, w.plumbingID = p["slug"].(string), p["id"].(string)
	admin.expect(admin.do("PUT", "/api/v1/admin/services/"+w.plumbingID+"/synonyms", map[string]any{"synonyms": []string{"Fundi wa maji"}}), 200)
	w.electrical = mk("Electrical")["slug"].(string)
	w.cleaning = mk("House cleaning")["slug"].(string)
	counties := admin.expect(admin.do("GET", "/api/v1/admin/locations/counties?limit=100", nil), 200).body["data"].([]any)
	byslug := map[string]map[string]any{}
	for _, c := range counties {
		m := c.(map[string]any)
		byslug[m["slug"].(string)] = m
	}
	w.nairobi = byslug["nairobi"]
	town := func(name string, county map[string]any) map[string]any {
		return admin.expect(admin.do("POST", "/api/v1/admin/locations/towns", map[string]any{"name": name, "parent_id": county["id"], "is_selectable": true}), 201).data()
	}
	area := func(name string, town map[string]any) map[string]any {
		return admin.expect(admin.do("POST", "/api/v1/admin/locations/areas", map[string]any{"name": name, "parent_id": town["id"], "is_selectable": true}), 201).data()
	}
	w.westlands, w.kilimani, w.ruaka = town("Westlands", w.nairobi), town("Kilimani", w.nairobi), town("Ruaka", byslug["kiambu"])
	w.parklands, w.highridge, w.yaya, w.ruakaTown = area("Parklands", w.westlands), area("Highridge", w.westlands), area("Yaya", w.kilimani), area("Ruaka Town", w.ruaka)
	// Centroids: Parklands (-1.2635, 36.8155) and Yaya (-1.2925, 36.7870), ~4.6 km apart.
	admin.expect(admin.do("PUT", "/api/v1/admin/locations/areas/"+w.parklands["id"].(string)+"/centroid", map[string]any{"latitude": -1.2635, "longitude": 36.8155}), 200)
	admin.expect(admin.do("PUT", "/api/v1/admin/locations/areas/"+w.yaya["id"].(string)+"/centroid", map[string]any{"latitude": -1.2925, "longitude": 36.7870}), 200)
	admin.expect(admin.do("PATCH", "/api/v1/admin/locations/towns/"+w.westlands["id"].(string), map[string]any{"is_indexable": true}), 200)
	return w
}

// fix for the mk() helper returning a map (keeps call sites terse).
type anyMap = map[string]any

type proSpec struct {
	name     string
	service  string
	base     map[string]any
	coverage []map[string]any // {"type","id"}
	pin      []float64        // lat, lng
	radius   int
	publish  bool
	public   bool // phone visibility public
}

// provider creates a listing through the real APIs and returns its slug and public ID.
func (w *world) provider(t *testing.T, e *env, s proSpec) (slug, id string) {
	t.Helper()
	w.next++
	c := e.client()
	c.register("Pro", "Owner"+string(rune('a'+w.next%26)), fmt.Sprintf("07%08d", 20000000+w.next))
	v := c.expect(c.do("POST", "/api/v1/provider/profile", anyMap{"provider_type": "individual", "display_name": s.name, "accept_terms": true}), 201).data()
	svc := w.admin.expect(w.admin.do("GET", "/api/v1/services?q="+url.QueryEscape(s.service), nil), 200).body["data"].([]any)[0].(anyMap)
	c.expect(c.do("POST", "/api/v1/provider/services", anyMap{"service_id": svc["id"], "pricing_type": "from", "price_from": 1500}), 201)
	areas := anyMap{"base_area_id": s.base["id"], "coverage": s.coverage}
	if s.pin != nil {
		areas["pin"] = anyMap{"latitude": s.pin[0], "longitude": s.pin[1]}
	}
	if s.radius > 0 {
		areas["service_radius_km"] = s.radius
	}
	if s.coverage == nil {
		areas["coverage"] = []anyMap{{"type": "area", "id": s.base["id"]}}
	}
	c.expect(c.do("PUT", "/api/v1/provider/service-areas", areas), 200)
	if s.public {
		c.expect(c.do("PUT", "/api/v1/provider/contacts", anyMap{"contacts": []anyMap{{"channel": "phone", "value": fmt.Sprintf("07%08d", 20000000+w.next), "visibility": "public"}}}), 200)
	}
	if s.publish {
		c.expect(c.do("POST", "/api/v1/provider/profile/submit", nil), 200)
		w.admin.expect(w.admin.do("POST", "/api/v1/admin/providers/"+v["id"].(string)+"/status", anyMap{"action": "approve"}), 200)
	}
	return v["slug"].(string), v["id"].(string)
}

func cov(typ string, loc map[string]any) map[string]any { return anyMap{"type": typ, "id": loc["id"]} }

func (c *client) searchAPI(q string) (names []string, meta map[string]any) {
	c.e.t.Helper()
	r := c.expect(c.do("GET", "/api/v1/providers/search?"+q, nil), 200)
	for _, it := range r.body["data"].(anyMap)["items"].([]any) {
		names = append(names, it.(anyMap)["name"].(string))
	}
	m, _ := r.body["meta"].(anyMap)
	return names, m
}

func has(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestSearchCoverageRadiusAndEligibility(t *testing.T) {
	e := newEnv(t)
	w := newWorld(t, e)
	// Based in Ruaka (Kiambu) but explicitly serves Westlands town → must appear in Parklands.
	w.provider(t, e, proSpec{name: "Ruaka Plumbers", service: "Plumbing", base: w.ruakaTown,
		coverage: []anyMap{cov("area", w.ruakaTown), cov("town", w.westlands)}, publish: true})
	// Exact area coverage in Parklands.
	w.provider(t, e, proSpec{name: "Parklands Plumbing", service: "Plumbing", base: w.parklands, publish: true, public: true})
	// County-wide coverage (serves all of Nairobi).
	w.provider(t, e, proSpec{name: "Nairobi Wide", service: "Plumbing", base: w.yaya, coverage: []anyMap{cov("county", w.nairobi)}, publish: true})
	// No coverage of Westlands, but pin in Yaya with an 8 km radius reaching Parklands' centroid.
	w.provider(t, e, proSpec{name: "Yaya Radius", service: "Plumbing", base: w.yaya, pin: []float64{-1.2925, 36.7870}, radius: 8, publish: true})
	// Pin in Yaya with only 2 km radius: does not reach Parklands.
	w.provider(t, e, proSpec{name: "Yaya Short", service: "Plumbing", base: w.yaya, pin: []float64{-1.2925, 36.7870}, radius: 2, publish: true})
	// Draft (unpublished) listing covering Parklands must never appear.
	w.provider(t, e, proSpec{name: "Draft Plumbing", service: "Plumbing", base: w.parklands, publish: false})
	// Electrician in Parklands: different service.
	w.provider(t, e, proSpec{name: "Sparky", service: "Electrical", base: w.parklands, publish: true})

	anon := e.client()
	names, meta := anon.searchAPI("q=plumbing&location=" + w.parklands["slug"].(string))
	for _, want := range []string{"Ruaka Plumbers", "Parklands Plumbing", "Nairobi Wide", "Yaya Radius"} {
		if !has(names, want) {
			t.Errorf("missing %q in %v", want, names)
		}
	}
	for _, no := range []string{"Yaya Short", "Draft Plumbing", "Sparky"} {
		if has(names, no) {
			t.Errorf("unexpected %q in %v", no, names)
		}
	}
	if meta["total"].(float64) != 4 {
		t.Fatalf("total = %v", meta["total"])
	}
	// Explicit coverage outranks radius (§10.5).
	if names[len(names)-1] != "Yaya Radius" {
		t.Errorf("radius match should rank last: %v", names)
	}
	// Synonyms resolve ("fundi wa maji" → Plumbing) and free-text place names resolve via the hierarchy.
	names, _ = anon.searchAPI("q=fundi+wa+maji&where=Parklands")
	if len(names) != 4 {
		t.Fatalf("synonym search = %v", names)
	}
	// Town search includes providers covering any area within it, and county coverage.
	names, _ = anon.searchAPI("q=plumbing&location=" + w.westlands["slug"].(string))
	if !has(names, "Parklands Plumbing") || !has(names, "Nairobi Wide") || !has(names, "Ruaka Plumbers") {
		t.Fatalf("town search = %v", names)
	}
	// Kiambu search: only the Ruaka-based provider.
	names, _ = anon.searchAPI("q=plumbing&location=kiambu")
	if len(names) != 1 || names[0] != "Ruaka Plumbers" {
		t.Fatalf("county search = %v", names)
	}
	// One-time coordinates near Yaya with a 3 km radius: the Yaya pins (axis order matters:
	// a swapped lat/lng would put the point in the Indian Ocean).
	names, _ = anon.searchAPI("q=plumbing&lat=-1.2930&lng=36.7880&radius_km=3")
	if !has(names, "Yaya Radius") || !has(names, "Yaya Short") || has(names, "Ruaka Plumbers") {
		t.Fatalf("near-me search = %v", names)
	}
	// Coordinates are never stored.
	if e.count(`SELECT COUNT(*) FROM analytics_events WHERE properties LIKE '%1.293%'`) != 0 {
		t.Fatal("coordinates leaked into analytics")
	}
	// Card content: public phone shows, reveal_on_click does not.
	r := anon.expect(anon.do("GET", "/api/v1/providers/search?q=plumbing&location="+w.parklands["slug"].(string), nil), 200)
	raw := r.raw
	if !strings.Contains(raw, `"phone_href":"tel:`) || strings.Contains(raw, "Draft Plumbing") {
		t.Fatalf("cards: %s", raw)
	}
	// Limits are bounded.
	_, meta = anon.searchAPI("q=plumbing&limit=1000&page=999")
	if meta["per_page"].(float64) != 50 || meta["page"].(float64) != 50 {
		t.Fatalf("bounds: %v", meta)
	}
	// Paused listings disappear from search.
	pid := e.count(`SELECT id FROM providers WHERE display_name = 'Parklands Plumbing'`)
	e.exec(`UPDATE providers SET listing_status = 'paused' WHERE id = ?`, pid)
	names, _ = anon.searchAPI("q=plumbing&location=" + w.parklands["slug"].(string))
	if has(names, "Parklands Plumbing") {
		t.Fatal("paused listing in search")
	}
}

var ldRe = regexp.MustCompile(`(?s)<script type="application/ld\+json">(.*?)</script>`)

func jsonLD(t *testing.T, body string) map[string]any {
	t.Helper()
	m := ldRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no JSON-LD")
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(m[1]), &v); err != nil {
		t.Fatalf("invalid JSON-LD: %v\n%s", err, m[1])
	}
	return v
}

func metaRobots(body string) string {
	m := regexp.MustCompile(`<meta name="robots" content="([^"]+)">`).FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return m[1]
}

func titleOf(body string) string {
	m := regexp.MustCompile(`<title>([^<]*)</title>`).FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return m[1]
}

func TestLandingPagesIndexingHysteresisAndSitemap(t *testing.T) {
	e := newEnv(t)
	w := newWorld(t, e)
	anon := e.client()
	path := "/services/" + w.plumbing + "/" + w.westlands["slug"].(string)

	// Thin page: one provider → served, noindex, with providers listed.
	w.provider(t, e, proSpec{name: "Alpha Plumbing", service: "Plumbing", base: w.parklands, publish: true})
	s, _, body := anon.page(path)
	if s != 200 || !strings.Contains(body, "Alpha Plumbing") || metaRobots(body) != "noindex,follow" {
		t.Fatalf("thin landing: %d %s", s, metaRobots(body))
	}
	if !strings.Contains(body, `<link rel="canonical" href="http://example.test`+path+`">`) {
		t.Fatal("canonical missing")
	}
	// Reaching the threshold (3) makes it indexable after evaluation, with structured data.
	w.provider(t, e, proSpec{name: "Beta Plumbing", service: "Plumbing", base: w.highridge, publish: true})
	_, betaID := w.provider(t, e, proSpec{name: "Gamma Plumbing", service: "Plumbing", base: w.parklands, publish: true})
	if _, err := e.app.Discovery.EvaluateLanding(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, _, body = anon.page(path)
	if metaRobots(body) != "index,follow" {
		t.Fatalf("indexable landing robots = %s", metaRobots(body))
	}
	ld := jsonLD(t, body)
	g := fmt.Sprint(ld["@graph"])
	if !strings.Contains(g, "BreadcrumbList") || !strings.Contains(g, "ItemList") || strings.Contains(g, "aggregateRating") {
		t.Fatalf("landing JSON-LD = %s", g)
	}
	if titleOf(body) == "" || !strings.Contains(titleOf(body), "Westlands") {
		t.Fatalf("title = %q", titleOf(body))
	}
	_, _, sm := anon.page("/sitemaps/landing-1.xml")
	if !strings.Contains(sm, path) {
		t.Fatalf("landing missing from sitemap: %s", sm)
	}
	// Dropping below the threshold keeps the page indexed during the hysteresis window...
	w.admin.expect(w.admin.do("POST", "/api/v1/admin/providers/"+betaID+"/status", anyMap{"action": "suspend", "reason": "Test"}), 200)
	if _, err := e.app.Discovery.EvaluateLanding(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, _, body = anon.page(path); metaRobots(body) != "index,follow" || strings.Contains(body, "Gamma Plumbing") {
		t.Fatalf("hysteresis: robots=%s", metaRobots(body))
	}
	// ...and drops out once it has stayed below for the configured period.
	e.exec(`UPDATE seo_landing_pages SET below_since = DATE_SUB(UTC_TIMESTAMP(3), INTERVAL 15 DAY) WHERE below_since IS NOT NULL`)
	if _, err := e.app.Discovery.EvaluateLanding(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, _, body = anon.page(path); metaRobots(body) != "noindex,follow" {
		t.Fatalf("after hysteresis robots = %s", metaRobots(body))
	}
	if _, _, sm = anon.page("/sitemaps/landing-1.xml"); strings.Contains(sm, path) {
		t.Fatal("thin landing still in sitemap")
	}
	// Manual override (seo.update_pages) forces indexing and custom metadata; audited.
	cust := e.userWithRoles("0700000777")
	cust.expect(cust.do("PUT", "/api/v1/admin/landing-pages", anyMap{"service": w.plumbing, "location": w.westlands["slug"], "robots_mode": "index"}), 403)
	w.admin.expect(w.admin.do("PUT", "/api/v1/admin/landing-pages", anyMap{"service": w.plumbing, "location": w.westlands["slug"],
		"robots_mode": "index", "meta_title": "Trusted plumbers in Westlands", "intro": "**Same-day** repairs."}), 204)
	_, _, body = anon.page(path)
	if metaRobots(body) != "index,follow" || titleOf(body) != "Trusted plumbers in Westlands" || !strings.Contains(body, "<strong>Same-day</strong>") {
		t.Fatalf("override: %s %q", metaRobots(body), titleOf(body))
	}
	if e.audits("seo.landing.updated") != 1 {
		t.Fatal("override not audited")
	}
	list := w.admin.expect(w.admin.do("GET", "/api/v1/admin/landing-pages?state=overridden", nil), 200).body["data"].([]any)
	if len(list) != 1 {
		t.Fatalf("admin landing list = %v", list)
	}

	// Unknown and unselectable locations 404; a merged location 301s to its target.
	if s, _, _ := anon.page("/services/" + w.plumbing + "/nowhere"); s != 404 {
		t.Fatalf("unknown location = %d", s)
	}
	if s, _, _ := anon.page("/services/" + w.plumbing + "/mombasa"); s != 404 {
		t.Fatalf("unselectable county = %d", s)
	}
	w.admin.expect(w.admin.do("POST", "/api/v1/admin/locations/merge", anyMap{"type": "area", "source_id": w.highridge["id"], "target_id": w.parklands["id"]}), 200)
	if s, h, _ := anon.page("/services/" + w.plumbing + "/" + w.highridge["slug"].(string)); s != 301 ||
		h.Get("Location") != "/services/"+w.plumbing+"/"+w.parklands["slug"].(string) {
		t.Fatalf("merged location: %d %s", s, h.Get("Location"))
	}
	// A renamed service keeps its landing URLs via the slug redirect.
	w.admin.expect(w.admin.do("PATCH", "/api/v1/admin/services/"+w.plumbingID, anyMap{"slug": "plumbers"}), 200)
	if s, h, _ := anon.page(path); s != 301 || h.Get("Location") != "/services/plumbers/"+w.westlands["slug"].(string) {
		t.Fatalf("renamed service: %d %s", s, h.Get("Location"))
	}
}

func TestPublicPagesMetadataSitemapsAndRobots(t *testing.T) {
	e := newEnv(t)
	w := newWorld(t, e)
	slug, _ := w.provider(t, e, proSpec{name: "Omega Plumbing", service: "Plumbing", base: w.parklands, publish: true})
	pausedSlug, pausedID := w.provider(t, e, proSpec{name: "Paused Pro", service: "Plumbing", base: w.parklands, publish: true})
	_ = pausedID
	e.exec(`UPDATE providers SET listing_status = 'paused' WHERE slug = ?`, pausedSlug)
	if _, err := e.app.Discovery.EvaluateLanding(context.Background()); err != nil {
		t.Fatal(err)
	}
	anon := e.client()
	titles := map[string]string{}
	for _, p := range []string{"/", "/categories", "/categories/home-repairs", "/services/" + w.plumbing,
		"/services/" + w.plumbing + "/" + w.parklands["slug"].(string), "/locations/" + w.westlands["slug"].(string)} {
		s, _, body := anon.page(p)
		if s != 200 {
			t.Fatalf("%s = %d", p, s)
		}
		if strings.Count(body, "<h1") != 1 || !strings.Contains(body, `rel="canonical"`) || !strings.Contains(body, `og:title`) {
			t.Fatalf("%s: h1/canonical/og missing", p)
		}
		tt := titleOf(body)
		if prev, dup := titles[tt]; dup {
			t.Fatalf("duplicate title %q on %s and %s", tt, prev, p)
		}
		titles[tt] = p
		jsonLD(t, body) // valid JSON
		if !strings.Contains(body, "Omega Plumbing") && p != "/categories" && p != "/categories/home-repairs" {
			t.Fatalf("%s: provider missing", p)
		}
		if strings.Contains(body, "Paused Pro") {
			t.Fatalf("%s shows a paused provider", p)
		}
	}
	// Homepage structured data.
	_, _, home := anon.page("/")
	g := fmt.Sprint(jsonLD(t, home)["@graph"])
	if !strings.Contains(g, "Organization") || !strings.Contains(g, "SearchAction") {
		t.Fatalf("home JSON-LD = %s", g)
	}
	// Service with no providers: 200, noindex, request form; the empty category page is noindex.
	_, _, body := anon.page("/services/" + w.cleaning)
	if metaRobots(body) != "noindex,follow" || !strings.Contains(body, `action="/requests"`) {
		t.Fatal("empty service page should be noindex with a request form")
	}
	// /search is noindex and works without JavaScript.
	s, _, body := anon.page("/search?q=plumbing&where=Westlands")
	if s != 200 || metaRobots(body) != "noindex,follow" || !strings.Contains(body, "Omega Plumbing") {
		t.Fatalf("search page %d %s", s, metaRobots(body))
	}
	// Sitemaps: index + files, only eligible URLs.
	_, h, idx := anon.page("/sitemap.xml")
	if !strings.HasPrefix(h.Get("Content-Type"), "application/xml") || !strings.Contains(idx, "/sitemaps/providers-1.xml") {
		t.Fatalf("sitemap index: %s", idx)
	}
	_, _, prov := anon.page("/sitemaps/providers-1.xml")
	if !strings.Contains(prov, "/professionals/"+slug) || strings.Contains(prov, pausedSlug) {
		t.Fatalf("provider sitemap: %s", prov)
	}
	_, _, svcs := anon.page("/sitemaps/services.xml")
	if !strings.Contains(svcs, "/services/"+w.plumbing+"<") || strings.Contains(svcs, w.cleaning) {
		t.Fatalf("service sitemap: %s", svcs)
	}
	_, _, locs := anon.page("/sitemaps/locations.xml")
	if !strings.Contains(locs, "/locations/"+w.westlands["slug"].(string)) || strings.Contains(locs, "/locations/kilimani") {
		t.Fatalf("location sitemap (indexable only): %s", locs)
	}
	if s, _, _ := anon.page("/sitemaps/providers.xml"); s != 404 {
		t.Fatal("unnumbered provider sitemap should 404")
	}
	_, _, robots := anon.page("/robots.txt")
	if !strings.Contains(robots, "Sitemap: http://example.test/sitemap.xml") || !strings.Contains(robots, "Disallow: /search") ||
		!strings.Contains(robots, "Disallow: /\n") {
		t.Fatalf("robots: %s", robots)
	}
	// Breadcrumbs and BreadcrumbList on provider profiles.
	_, _, pbody := anon.page("/professionals/" + slug)
	if !strings.Contains(fmt.Sprint(jsonLD(t, pbody)["@graph"]), "BreadcrumbList") {
		t.Fatal("profile breadcrumbs missing")
	}
	// Pagination beyond page 1 is not indexed.
	_, _, p2 := anon.page("/services/" + w.plumbing + "?page=2")
	if metaRobots(p2) != "noindex,follow" {
		t.Fatal("page 2 indexable")
	}
}

func TestZeroResultsDemandRequestsAndGaps(t *testing.T) {
	e := newEnv(t)
	w := newWorld(t, e)
	w.provider(t, e, proSpec{name: "Kilimani Plumbing", service: "Plumbing", base: w.yaya, publish: true})
	w.provider(t, e, proSpec{name: "Parklands Sparks", service: "Electrical", base: w.parklands, publish: true})
	if _, err := e.app.Discovery.EvaluateLanding(context.Background()); err != nil {
		t.Fatal(err)
	}
	anon := e.client()
	// Plumbing in Parklands has nobody: broader (Nairobi), nearby (Kilimani/Yaya) and related (Electrical) alternatives.
	s, _, body := anon.page("/services/" + w.plumbing + "/" + w.parklands["slug"].(string))
	if s != 200 || metaRobots(body) != "noindex,follow" {
		t.Fatalf("zero landing %d %s", s, metaRobots(body))
	}
	for _, want := range []string{"/services/" + w.plumbing + "/nairobi", "/services/" + w.plumbing + "/kilimani", "/services/" + w.electrical + "/" + w.parklands["slug"].(string), `name="csrf_token"`} {
		if !strings.Contains(body, want) {
			t.Errorf("zero page missing %s", want)
		}
	}
	// Zero-result searches are logged.
	anon.page("/search?q=plumbing&where=Parklands")
	anon.page("/search?q=borehole+drilling+0712345678&where=Parklands")
	if e.count(`SELECT COUNT(*) FROM analytics_events WHERE event_type = 'search_zero_results'`) != 2 {
		t.Fatal("zero-result searches not logged")
	}
	if e.count(`SELECT COUNT(*) FROM analytics_events WHERE properties LIKE '%0712345678%'`) != 0 {
		t.Fatal("digits from the query stored in analytics")
	}

	// No-JS form: CSRF token in a hidden field, redirect back with a thank-you.
	tok := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindStringSubmatch(body)[1]
	form := url.Values{"csrf_token": {tok}, "return": {"/services/" + w.plumbing + "/" + w.parklands["slug"].(string)},
		"service": {w.plumbing}, "service_text": {"Plumbing"}, "location": {w.parklands["slug"].(string)},
		"contact_channel": {"sms"}, "contact_value": {"0711 999 888"}, "contact_consent": {"1"}}
	req, _ := http.NewRequest("POST", e.srv.URL+"/requests", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := anon.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 303 || !strings.Contains(res.Header.Get("Location"), "requested=1") {
		t.Fatalf("form post: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	// Without the token the form is rejected.
	form.Del("csrf_token")
	req, _ = http.NewRequest("POST", e.srv.URL+"/requests", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if res, _ = anon.http.Do(req); res.StatusCode != 403 {
		t.Fatalf("no-token post = %d", res.StatusCode)
	}
	// JSON API: contact requires consent.
	anon.expect(anon.do("POST", "/api/v1/demand-requests", anyMap{"service_text": "Borehole drilling", "contact_channel": "email", "contact_value": "x@example.test"}), 422)
	anon.expect(anon.do("POST", "/api/v1/demand-requests", anyMap{"service_text": "Borehole drilling", "location": "kiambu"}), 201)

	// Staff: support can view (audited, contacts are personal data) but not update; customers cannot view.
	// Roles persist across tests (another test edits the support role), so restore the
	// default grant this test relies on.
	e.exec(`INSERT IGNORE INTO role_permissions (role_id, permission_id) SELECT r.id, p.id FROM roles r JOIN permissions p
		ON p.permission_key = 'demand_requests.view' WHERE r.role_key = 'support'`)
	support := e.userWithRoles("support@example.test", "support")
	cust := e.userWithRoles("0700000888")
	cust.expect(cust.do("GET", "/api/v1/admin/demand-requests", nil), 403)
	list := support.expect(support.do("GET", "/api/v1/admin/demand-requests", nil), 200).body["data"].([]any)
	if len(list) != 2 || !strings.Contains(fmt.Sprint(list), "+254711999888") {
		t.Fatalf("demand list = %v", list)
	}
	if e.audits("demand_requests.contacts.viewed") != 1 {
		t.Fatal("contact view not audited")
	}
	id := list[1].(anyMap)["id"].(string)
	support.expect(support.do("PATCH", "/api/v1/admin/demand-requests/"+id, anyMap{"status": "in_progress"}), 403)
	w.admin.expect(w.admin.do("PATCH", "/api/v1/admin/demand-requests/"+id, anyMap{"status": "in_progress", "staff_note": "Recruiting"}), 204)

	// Contact details are purged after the retention period.
	e.exec(`UPDATE demand_requests SET created_at = DATE_SUB(created_at, INTERVAL 91 DAY)`)
	if err := jobsqueue.Enqueue(context.Background(), testDB, discovery.JobPurgeDemandContacts, anyMap{}, jobsqueue.Options{}); err != nil {
		t.Fatal(err)
	}
	for {
		ran, err := e.app.Queue.RunOnce(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !ran {
			break
		}
	}
	if e.count(`SELECT COUNT(*) FROM demand_requests WHERE contact_value IS NOT NULL`) != 0 {
		t.Fatal("contacts not purged")
	}

	// Search gaps report.
	gaps := w.admin.expect(w.admin.do("GET", "/api/v1/admin/search-gaps", nil), 200).body["data"].([]any)
	if len(gaps) < 2 {
		t.Fatalf("gaps = %v", gaps)
	}
	cust.expect(cust.do("GET", "/api/v1/admin/search-gaps", nil), 403)
}

func TestHomepageFeaturedAndNear(t *testing.T) {
	e := newEnv(t)
	w := newWorld(t, e)
	_, id := w.provider(t, e, proSpec{name: "Star Plumbing", service: "Plumbing", base: w.parklands, publish: true})
	w.provider(t, e, proSpec{name: "Ruaka Fixers", service: "Plumbing", base: w.ruakaTown, publish: true})
	anon := e.client()
	_, _, body := anon.page("/")
	if !strings.Contains(body, "Star Plumbing") || strings.Contains(body, "Featured professionals") {
		t.Fatal("homepage: recent providers missing or empty featured section shown")
	}
	mod := e.userWithRoles("mod@example.test", "moderator")
	mod.expect(mod.do("PUT", "/api/v1/admin/providers/"+id+"/featured", anyMap{"days": 7}), 403)
	w.admin.expect(w.admin.do("PUT", "/api/v1/admin/providers/"+id+"/featured", anyMap{"days": 500}), 422)
	v := w.admin.expect(w.admin.do("PUT", "/api/v1/admin/providers/"+id+"/featured", anyMap{"days": 7}), 200).data()
	if v["featured_until"] == nil {
		t.Fatal("featured_until missing")
	}
	_, _, body = anon.page("/")
	if !strings.Contains(body, "Featured professionals") {
		t.Fatal("featured section missing")
	}
	// Choosing a place filters "near" providers; the variant is noindex.
	_, _, body = anon.page("/?near=" + w.ruaka["slug"].(string))
	if !strings.Contains(body, "Ruaka Fixers") || metaRobots(body) != "noindex,follow" {
		t.Fatal("near filter")
	}
	w.admin.expect(w.admin.do("PUT", "/api/v1/admin/providers/"+id+"/featured", anyMap{"days": 0}), 200)
	if _, _, body = anon.page("/"); strings.Contains(body, "Featured professionals") {
		t.Fatal("unfeatured still shown")
	}
	if e.audits("providers.featured.updated") != 2 {
		t.Fatal("featuring not audited")
	}
}
