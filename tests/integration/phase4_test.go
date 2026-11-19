package integration

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/alumasinde/profy/internal/modules/notifications"
)

// catalogue publishes one service and creates a selectable town + area in Nairobi.
type catalogue struct{ serviceID, licServiceID, countyID, townID, areaID, area2ID string }

func setupCatalogue(t *testing.T, e *env, admin *client) catalogue {
	t.Helper()
	cat := admin.expect(admin.do("POST", "/api/v1/admin/categories", map[string]any{"name": "Home Repairs"}), 201).data()
	admin.expect(admin.do("PATCH", "/api/v1/admin/categories/"+cat["id"].(string), map[string]any{"status": "published"}), 200)
	svc := admin.expect(admin.do("POST", "/api/v1/admin/services", map[string]any{"name": "Plumbing repairs", "category_id": cat["id"]}), 201).data()
	admin.expect(admin.do("PATCH", "/api/v1/admin/services/"+svc["id"].(string), map[string]any{"status": "published"}), 200)
	lic := admin.expect(admin.do("POST", "/api/v1/admin/services", map[string]any{"name": "Electrical wiring", "category_id": cat["id"], "requires_licence": true}), 201).data()
	admin.expect(admin.do("PATCH", "/api/v1/admin/services/"+lic["id"].(string), map[string]any{"status": "published"}), 200)
	var countyID string
	for _, c := range admin.expect(admin.do("GET", "/api/v1/admin/locations/counties?limit=100", nil), 200).body["data"].([]any) {
		if m := c.(map[string]any); m["slug"] == "nairobi" {
			countyID = m["id"].(string)
		}
	}
	town := admin.expect(admin.do("POST", "/api/v1/admin/locations/towns", map[string]any{"name": "Westlands", "parent_id": countyID, "is_selectable": true}), 201).data()
	area := admin.expect(admin.do("POST", "/api/v1/admin/locations/areas", map[string]any{"name": "Parklands", "parent_id": town["id"], "is_selectable": true}), 201).data()
	area2 := admin.expect(admin.do("POST", "/api/v1/admin/locations/areas", map[string]any{"name": "Highridge", "parent_id": town["id"], "is_selectable": true}), 201).data()
	return catalogue{serviceID: svc["id"].(string), licServiceID: lic["id"].(string), countyID: countyID, townID: town["id"].(string),
		areaID: area["id"].(string), area2ID: area2["id"].(string)}
}

// completeListing fills the required sections through the given API prefix.
func completeListing(t *testing.T, c *client, prefix string, cat catalogue) {
	t.Helper()
	c.expect(c.do("POST", prefix+"/services", map[string]any{"service_id": cat.serviceID, "pricing_type": "fixed", "price_from": 1500}), 201)
	c.expect(c.do("PUT", prefix+"/service-areas", map[string]any{"base_area_id": cat.areaID, "service_radius_km": 10,
		"coverage": []map[string]any{{"type": "town", "id": cat.townID}}}), 200)
}

func TestProviderListingLifecycleAndVisibility(t *testing.T) {
	e := newEnv(t)
	_, admin, cust := admins(t, e)
	cat := setupCatalogue(t, e, admin)
	mod := e.userWithRoles("mod@example.test", "moderator")

	prov := e.client()
	prov.register("Jane", "Wanjiru", "0711111111")
	prov.expect(prov.do("GET", "/api/v1/provider/profile", nil), 404)
	v := prov.expect(prov.do("POST", "/api/v1/provider/profile", map[string]any{"provider_type": "individual",
		"display_name": "Jane Wanjiru Plumbing", "accept_terms": true}), 201).data()
	prov.expect(prov.do("POST", "/api/v1/provider/profile", map[string]any{"provider_type": "individual", "display_name": "Again"}), 409)
	slug := v["slug"].(string)
	contacts := v["contacts"].([]any)
	if len(contacts) != 1 || contacts[0].(map[string]any)["value"] != "+254711111111" || contacts[0].(map[string]any)["verified_at"] == nil {
		t.Fatalf("contacts not prefilled from verified login: %v", contacts)
	}
	if !strings.Contains(prov.do("GET", "/api/v1/provider/profile", nil).raw, `"phone_verified"`) {
		t.Fatal("phone badge missing")
	}

	// Drafts are invisible; incomplete listings cannot be submitted.
	if s, _, _ := cust.page("/professionals/" + slug); s != 404 {
		t.Fatalf("draft visible: %d", s)
	}
	if r := prov.do("POST", "/api/v1/provider/profile/submit", nil); r.status != 409 || r.errCode() != "listing_incomplete" ||
		!strings.Contains(r.raw, "services") {
		t.Fatalf("incomplete submit: %s", r.raw)
	}
	// Pricing validation.
	prov.expect(prov.do("POST", "/api/v1/provider/services", map[string]any{"service_id": cat.serviceID, "pricing_type": "range", "price_from": 5000, "price_to": 100}), 422)
	completeListing(t, prov, "/api/v1/provider", cat)
	prov.expect(prov.do("PATCH", "/api/v1/provider/profile", map[string]any{"description": "Leaking taps, blocked drains and new installations across Westlands. Ten years of experience."}), 200)
	// Owners cannot set the slug.
	prov.expect(prov.do("PATCH", "/api/v1/provider/profile", map[string]any{"slug": "hijack"}), 422)

	sub := prov.expect(prov.do("POST", "/api/v1/provider/profile/submit", nil), 200).data()
	if sub["status"] != "pending_review" {
		t.Fatalf("status = %v", sub["status"])
	}
	if s, _, _ := cust.page("/professionals/" + slug); s != 404 {
		t.Fatalf("pending visible: %d", s)
	}

	// Review queue and permissions.
	cust.expect(cust.do("GET", "/api/v1/admin/providers", nil), 403)
	cust.expect(cust.do("POST", "/api/v1/admin/providers/"+v["id"].(string)+"/status", map[string]any{"action": "approve"}), 403)
	prov.expect(prov.do("POST", "/api/v1/admin/providers/"+v["id"].(string)+"/status", map[string]any{"action": "approve"}), 403)
	q := mod.expect(mod.do("GET", "/api/v1/admin/providers?status=pending_review", nil), 200).data()
	if items := q["items"].([]any); len(items) != 1 || items[0].(map[string]any)["id"] != v["id"] {
		t.Fatalf("queue = %v", q)
	}
	pid := v["id"].(string)
	mod.expect(mod.do("POST", "/api/v1/admin/providers/"+pid+"/status", map[string]any{"action": "reject"}), 422)
	rej := mod.expect(mod.do("POST", "/api/v1/admin/providers/"+pid+"/status", map[string]any{"action": "reject", "reason": "Add a profile photo of your work."}), 200).data()
	if rej["status"] != "draft" || rej["status_reason"] != "Add a profile photo of your work." {
		t.Fatalf("reject = %v", rej)
	}
	if n := e.count(`SELECT COUNT(*) FROM background_jobs WHERE kind = 'provider.notify'`); n != 1 {
		t.Fatalf("notify jobs = %d", n)
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
	if msg := e.lastMessage("sms", "+254711111111"); !strings.Contains(msg, "Add a profile photo of your work.") {
		t.Fatalf("owner notice = %q", msg)
	}
	prov.expect(prov.do("POST", "/api/v1/provider/profile/submit", nil), 200)
	// Moderators may not edit listing content.
	mod.expect(mod.do("PATCH", "/api/v1/admin/providers/"+pid, map[string]any{"display_name": "X"}), 403)
	pub := mod.expect(mod.do("POST", "/api/v1/admin/providers/"+pid+"/status", map[string]any{"action": "approve"}), 200).data()
	if pub["status"] != "published" || pub["published_at"] == nil {
		t.Fatalf("approve = %v", pub)
	}

	// Published: visible, contact hidden behind reveal by default.
	s, h, body := cust.page("/professionals/" + slug)
	if s != 200 || !strings.Contains(body, "Jane Wanjiru Plumbing") || !strings.Contains(body, "KES 1,500") {
		t.Fatalf("public page %d: %.300s", s, body)
	}
	if strings.Contains(body, "711") || !strings.Contains(body, "?reveal=phone") || !strings.Contains(body, `application/ld+json`) {
		t.Fatal("contact leaked or reveal link / JSON-LD missing")
	}
	if h.Get("X-Robots-Tag") != "" {
		t.Fatalf("published page noindexed: %s", h.Get("X-Robots-Tag"))
	}
	s, h, body = cust.page("/professionals/" + slug + "?reveal=phone")
	if s != 200 || !strings.Contains(body, "0711 111 111") || !strings.Contains(body, `href="tel:&#43;254711111111"`) || !strings.HasPrefix(h.Get("X-Robots-Tag"), "noindex") {
		t.Fatalf("reveal failed: %d %s", s, h.Get("X-Robots-Tag"))
	}
	if n := e.count(`SELECT COUNT(*) FROM analytics_events WHERE event_type = 'provider_phone_reveal'`); n != 1 {
		t.Fatalf("reveal events = %d", n)
	}
	api := cust.expect(cust.do("POST", "/api/v1/providers/"+slug+"/contact-reveal", map[string]any{"channel": "phone"}), 200).data()
	if api["href"] != "tel:+254711111111" {
		t.Fatalf("api reveal = %v", api)
	}
	// Owner dashboard counts views.
	d := prov.expect(prov.do("GET", "/api/v1/provider/dashboard", nil), 200).data()
	if d["profile_views_30d"].(float64) < 1 {
		t.Fatalf("dashboard = %v", d)
	}

	// Pause/resume (owner), suspend/restore (moderator).
	prov.expect(prov.do("POST", "/api/v1/provider/profile/pause", nil), 200)
	if s, h, body := cust.page("/professionals/" + slug); s != 200 || !strings.HasPrefix(h.Get("X-Robots-Tag"), "noindex") || strings.Contains(body, "KES 1,500") {
		t.Fatalf("paused page: %d %s", s, h.Get("X-Robots-Tag"))
	}
	cust.expect(cust.do("POST", "/api/v1/providers/"+slug+"/contact-reveal", map[string]any{"channel": "phone"}), 404)
	prov.expect(prov.do("POST", "/api/v1/provider/profile/resume", nil), 200)
	mod.expect(mod.do("POST", "/api/v1/admin/providers/"+pid+"/status", map[string]any{"action": "suspend"}), 422)
	mod.expect(mod.do("POST", "/api/v1/admin/providers/"+pid+"/status", map[string]any{"action": "suspend", "reason": "Reported for no-shows"}), 200)
	// A suspended owner cannot resume themselves.
	if r := prov.do("POST", "/api/v1/provider/profile/resume", nil); r.status != 409 || r.errCode() != "invalid_transition" {
		t.Fatalf("owner resumed suspended listing: %s", r.raw)
	}
	if _, _, body := cust.page("/professionals/" + slug); strings.Contains(body, "KES 1,500") {
		t.Fatal("suspended listing shows details")
	}
	mod.expect(mod.do("POST", "/api/v1/admin/providers/"+pid+"/status", map[string]any{"action": "restore"}), 200)
	if s, _, _ := cust.page("/professionals/" + slug); s != 200 {
		t.Fatal("restore failed")
	}

	// Staff slug change redirects the old URL.
	admin.expect(admin.do("PATCH", "/api/v1/admin/providers/"+pid, map[string]any{"slug": "jane-plumbing"}), 200)
	if s, h, _ := cust.page("/professionals/" + slug); s != 301 || h.Get("Location") != "/professionals/jane-plumbing" {
		t.Fatalf("slug redirect: %d %s", s, h.Get("Location"))
	}

	// History, moderation and audit trail.
	got := mod.expect(mod.do("GET", "/api/v1/admin/providers/"+pid, nil), 200).data()
	if len(got["history"].([]any)) < 8 || got["owner"] == nil {
		t.Fatalf("staff view = %v", got["history"])
	}
	if e.count(`SELECT COUNT(*) FROM moderation_actions WHERE target_type = 'provider'`) != 4 || e.audits("providers.listing.approve") != 1 {
		t.Fatal("moderation/audit records missing")
	}
	if e.count(`SELECT COUNT(*) FROM audit_logs WHERE new_values LIKE '%711111111%' OR old_values LIKE '%711111111%'`) != 0 {
		t.Fatal("contact value written to the audit log")
	}

	// Archive is final: 410.
	prov.expect(prov.do("POST", "/api/v1/provider/profile/archive", nil), 200)
	if s, _, _ := cust.page("/professionals/jane-plumbing"); s != 410 {
		t.Fatalf("archived = %d", s)
	}
}

func TestPreModerationOfMaterialEdits(t *testing.T) {
	e := newEnv(t)
	_, admin, _ := admins(t, e)
	cat := setupCatalogue(t, e, admin)
	prov := e.client()
	prov.register("Ali", "Hassan", "0712121212")
	v := prov.expect(prov.do("POST", "/api/v1/provider/profile", map[string]any{"provider_type": "business", "display_name": "Hassan Fixers", "accept_terms": true}), 201).data()
	completeListing(t, prov, "/api/v1/provider", cat)
	prov.expect(prov.do("POST", "/api/v1/provider/profile/submit", nil), 200)
	admin.expect(admin.do("POST", "/api/v1/admin/providers/"+v["id"].(string)+"/status", map[string]any{"action": "approve"}), 200)
	admin.expect(admin.do("PATCH", "/api/v1/admin/settings", map[string]any{"listing_edit_moderation": "pre"}), 200)
	got := prov.expect(prov.do("PATCH", "/api/v1/provider/profile", map[string]any{"display_name": "Hassan Fixers Ltd"}), 200).data()
	if got["status"] != "pending_review" {
		t.Fatalf("material edit did not re-enter review: %v", got["status"])
	}
	// Non-material edits do not.
	admin.expect(admin.do("POST", "/api/v1/admin/providers/"+v["id"].(string)+"/status", map[string]any{"action": "approve"}), 200)
	got = prov.expect(prov.do("PATCH", "/api/v1/provider/profile", map[string]any{"years_experience": 4}), 200).data()
	if got["status"] != "published" {
		t.Fatalf("minor edit changed status: %v", got["status"])
	}
}

var claimToken = regexp.MustCompile(`#token=([A-Za-z0-9_-]+)`)

func (e *env) lastMessage(channel, to string) string {
	e.t.Helper()
	e.app.Providers.WaitDeliveries()
	m, ok := e.app.Senders[channel].(*notifications.FakeSender).Last(to)
	if !ok {
		e.t.Fatalf("no %s message to %s", channel, to)
	}
	return m.Body
}

func TestAssistedOnboardingAndClaim(t *testing.T) {
	e := newEnv(t)
	_, admin, cust := admins(t, e)
	cat := setupCatalogue(t, e, admin)
	mod := e.userWithRoles("mod@example.test", "moderator")

	mod.expect(mod.do("POST", "/api/v1/admin/providers", map[string]any{"provider_type": "business", "display_name": "Kamau Electricals"}), 403)
	v := admin.expect(admin.do("POST", "/api/v1/admin/providers", map[string]any{"provider_type": "business", "display_name": "Kamau Electricals",
		"claim_channel": "sms", "claim_destination": "0722 222 222"}), 201).data()
	pid := v["id"].(string)
	if v["assisted"] != true || v["owner"] != nil || v["claim"].(map[string]any)["destination"] == "+254722222222" {
		t.Fatalf("assisted view = %v", v)
	}
	body := e.lastMessage("sms", "+254722222222")
	m := claimToken.FindStringSubmatch(body)
	if m == nil || !strings.Contains(body, "Kamau Electricals") {
		t.Fatalf("invite body: %s", body)
	}
	token := m[1]
	if e.count(`SELECT COUNT(*) FROM background_jobs WHERE payload LIKE ?`, "%"+token+"%") != 0 ||
		e.count(`SELECT COUNT(*) FROM audit_logs WHERE new_values LIKE ?`, "%"+token+"%") != 0 {
		t.Fatal("claim token persisted outside the hash")
	}

	// Staff fill the listing; it cannot go to review unclaimed.
	completeListing(t, admin, "/api/v1/admin/providers/"+pid, cat)
	admin.expect(admin.do("PUT", "/api/v1/admin/providers/"+pid+"/contacts", map[string]any{"contacts": []map[string]any{
		{"channel": "phone", "value": "0722222222", "visibility": "public"}}}), 200)
	if r := admin.do("POST", "/api/v1/admin/providers/"+pid+"/status", map[string]any{"action": "submit"}); r.status != 409 {
		t.Fatalf("unclaimed submit: %s", r.raw)
	}

	// Preview works without signing in; the wrong person cannot claim.
	anon := e.client()
	p := anon.expect(anon.do("POST", "/api/v1/provider/claim/preview", map[string]any{"token": token}), 200).data()
	if p["display_name"] != "Kamau Electricals" || strings.Contains(p["destination"].(string), "222222") {
		t.Fatalf("preview = %v", p)
	}
	anon.expect(anon.do("POST", "/api/v1/provider/claim", map[string]any{"token": token, "accept_terms": true}), 401)
	if r := cust.do("POST", "/api/v1/provider/claim", map[string]any{"token": token, "accept_terms": true}); r.status != 403 || r.errCode() != "claim_destination_mismatch" {
		t.Fatalf("mismatch claim: %s", r.raw)
	}

	owner := e.client()
	owner.register("Peter", "Kamau", "0722222222")
	owner.expect(owner.do("POST", "/api/v1/provider/claim", map[string]any{"token": token}), 422)
	got := owner.expect(owner.do("POST", "/api/v1/provider/claim", map[string]any{"token": token, "accept_terms": true}), 200).data()
	if got["id"] != pid || got["terms_accepted"] != true {
		t.Fatalf("claim = %v", got)
	}
	// Staff-entered phone matching the claimant's verified login is now verified.
	if !strings.Contains(owner.do("GET", "/api/v1/provider/profile", nil).raw, `"phone_verified"`) {
		t.Fatal("contact not verified on claim")
	}
	if r := owner.do("POST", "/api/v1/provider/claim", map[string]any{"token": token, "accept_terms": true}); r.status != 400 || r.errCode() != "claim_invalid" {
		t.Fatalf("token reuse: %s", r.raw)
	}
	admin.expect(admin.do("POST", "/api/v1/admin/providers/"+pid+"/invite", map[string]any{"channel": "sms", "destination": "0722222222"}), 409)
	owner.expect(owner.do("POST", "/api/v1/provider/profile/submit", nil), 200)
	if e.audits("providers.claimed") != 1 {
		t.Fatal("claim not audited")
	}
}

func pngBytes(t *testing.T, w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 120, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func (c *client) upload(path string, fields map[string]string, file string, data []byte) resp {
	c.e.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	fw, _ := mw.CreateFormFile(file, "upload.bin")
	_, _ = fw.Write(data)
	mw.Close()
	req, _ := http.NewRequest("POST", c.e.srv.URL+path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-CSRF-Token", c.csrf())
	res, err := c.http.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := resp{status: res.StatusCode, header: res.Header, raw: string(raw)}
	_ = jsonUnmarshal(string(raw), &out.body)
	return out
}

func TestMediaUploadsAndPortfolioModeration(t *testing.T) {
	e := newEnv(t)
	_, admin, cust := admins(t, e)
	cat := setupCatalogue(t, e, admin)
	mod := e.userWithRoles("mod@example.test", "moderator")
	prov := e.client()
	prov.register("Mary", "Achieng", "0713131313")
	v := prov.expect(prov.do("POST", "/api/v1/provider/profile", map[string]any{"provider_type": "individual", "display_name": "Mary Achieng", "accept_terms": true}), 201).data()

	r := prov.upload("/api/v1/provider/media", map[string]string{"purpose": "profile"}, "file", pngBytes(t, 300, 200))
	prov.expect(r, 201)
	photo := r.data()["profile_photo"].(map[string]any)
	if photo["status"] != "active" || photo["content_type"] != "image/jpeg" {
		t.Fatalf("photo = %v", photo)
	}
	s, h, _ := cust.page("/media/" + photo["id"].(string) + "/w160.jpg")
	if s != 200 || h.Get("Content-Type") != "image/jpeg" || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("media serve: %d %v", s, h)
	}
	if s, _, _ := cust.page("/media/" + photo["id"].(string) + "/w1600.jpg"); s != 404 {
		t.Fatal("upscaled variant served")
	}
	// Content sniffing, not the file name, decides the type.
	if r := prov.upload("/api/v1/provider/media", map[string]string{"purpose": "portfolio"}, "file", []byte("<html><script>alert(1)</script>")); r.status != 422 || !strings.Contains(r.raw, "unsupported_type") {
		t.Fatalf("html accepted: %s", r.raw)
	}
	// Replacing the profile photo retires the old one.
	r2 := prov.expect(prov.upload("/api/v1/provider/media", map[string]string{"purpose": "profile"}, "file", pngBytes(t, 200, 200)), 201)
	if e.count(`SELECT COUNT(*) FROM media_files WHERE status = 'deleted'`) != 1 || r2.data()["profile_photo"].(map[string]any)["id"] == photo["id"] {
		t.Fatal("old profile photo not retired")
	}

	completeListing(t, prov, "/api/v1/provider", cat)
	prov.expect(prov.do("POST", "/api/v1/provider/profile/submit", nil), 200)
	admin.expect(admin.do("POST", "/api/v1/admin/providers/"+v["id"].(string)+"/status", map[string]any{"action": "approve"}), 200)

	// With pre-moderation, new portfolio items on a live listing wait for review.
	admin.expect(admin.do("PATCH", "/api/v1/admin/settings", map[string]any{"portfolio_moderation": "pre"}), 200)
	pr := prov.expect(prov.upload("/api/v1/provider/media", map[string]string{"purpose": "portfolio", "caption": "Kitchen sink install"}, "file", pngBytes(t, 640, 480)), 201)
	item := pr.data()["portfolio"].([]any)[0].(map[string]any)
	mid := item["id"].(string)
	if item["media"].(map[string]any)["status"] != "pending_moderation" {
		t.Fatalf("portfolio = %v", item)
	}
	slug := v["slug"].(string)
	if _, _, body := cust.page("/professionals/" + slug); strings.Contains(body, "Kitchen sink install") {
		t.Fatal("pending image public")
	}
	if s, _, _ := cust.page("/media/" + mid + "/w480.jpg"); s != 404 {
		t.Fatal("pending image served to the public")
	}
	if s, _, _ := prov.page("/media/" + mid + "/w480.jpg"); s != 200 {
		t.Fatal("owner cannot see their pending image")
	}
	q := mod.expect(mod.do("GET", "/api/v1/admin/media", nil), 200)
	if !strings.Contains(q.raw, mid) {
		t.Fatalf("media queue: %s", q.raw)
	}
	mod.expect(mod.do("PATCH", "/api/v1/admin/media/"+mid, map[string]any{"decision": "approve"}), 200)
	if _, _, body := cust.page("/professionals/" + slug); !strings.Contains(body, "Kitchen sink install") {
		t.Fatal("approved image missing")
	}
	// Reorder/caption and delete.
	prov.expect(prov.do("PATCH", "/api/v1/provider/portfolio", map[string]any{"items": []map[string]any{{"id": mid, "caption": "New sink"}}}), 200)
	prov.expect(prov.do("PATCH", "/api/v1/provider/portfolio", map[string]any{"items": []map[string]any{{"id": photo["id"], "caption": "x"}}}), 422)
	other := e.client()
	other.register("Other", "Person", "0714141414")
	other.expect(other.do("POST", "/api/v1/provider/profile", map[string]any{"provider_type": "individual", "display_name": "Other Person"}), 201)
	other.expect(other.do("DELETE", "/api/v1/provider/media/"+mid, nil), 404) // not theirs
	prov.expect(prov.do("DELETE", "/api/v1/provider/media/"+mid, nil), 200)
	if s, _, _ := cust.page("/media/" + mid + "/w480.jpg"); s != 404 {
		t.Fatal("deleted image still served")
	}
}

func TestVerificationDocumentsAndContactBadges(t *testing.T) {
	e := newEnv(t)
	_, admin, cust := admins(t, e)
	cat := setupCatalogue(t, e, admin)
	mod := e.userWithRoles("mod@example.test", "moderator")
	prov := e.client()
	prov.register("Sam", "Otieno", "0715151515")
	v := prov.expect(prov.do("POST", "/api/v1/provider/profile", map[string]any{"provider_type": "individual", "display_name": "Sam Otieno", "accept_terms": true}), 201).data()
	pid := v["id"].(string)

	// Contacts: a new email is unverified until the owner proves it with a code.
	got := prov.expect(prov.do("PUT", "/api/v1/provider/contacts", map[string]any{"contacts": []map[string]any{
		{"channel": "phone", "value": "0715151515", "visibility": "public"},
		{"channel": "whatsapp", "value": "0715151515", "visibility": "reveal_on_click"},
		{"channel": "email", "value": "Sam@Example.test", "visibility": "logged_in_only"},
	}}), 200).data()
	if strings.Contains(fmtJSON(got["badges"]), "email_verified") {
		t.Fatal("email badge granted without proof")
	}
	ch := prov.expect(prov.do("POST", "/api/v1/provider/contacts/verify", map[string]any{"channel": "email"}), 200).data()
	got = prov.expect(prov.do("POST", "/api/v1/provider/contacts/verify/confirm", map[string]any{"challenge_id": ch["challenge_id"],
		"code": e.lastCode("sam@example.test")}), 200).data()
	if !strings.Contains(fmtJSON(got["badges"]), "email_verified") {
		t.Fatalf("email badge missing: %v", got["badges"])
	}
	// Changing a value drops its verification and badge.
	got = prov.expect(prov.do("PUT", "/api/v1/provider/contacts", map[string]any{"contacts": []map[string]any{
		{"channel": "phone", "value": "0799999999", "visibility": "public"},
		{"channel": "email", "value": "sam@example.test", "visibility": "logged_in_only"},
	}}), 200).data()
	b := fmtJSON(got["badges"])
	if strings.Contains(b, "phone_verified") || !strings.Contains(b, "email_verified") {
		t.Fatalf("badges after change: %s", b)
	}
	prov.expect(prov.do("PUT", "/api/v1/provider/contacts", map[string]any{"contacts": []map[string]any{
		{"channel": "phone", "value": "12345", "visibility": "public"}}}), 422)

	// Identity document review.
	pdf := []byte("%PDF-1.4\n1 0 obj << /Type /Catalog >> endobj\ntrailer << /Root 1 0 R >>\n%%EOF\n")
	sub := prov.upload("/api/v1/provider/verifications", map[string]string{"badge_type": "identity_checked", "document_type": "national_id"}, "files", pdf)
	prov.expect(sub, 201)
	evil := []byte("%PDF-1.4\n1 0 obj << /OpenAction << /S /JavaScript /JS (app.alert(1)) >> >> endobj\n%%EOF\n")
	if r := prov.upload("/api/v1/provider/verifications", map[string]string{"badge_type": "business_registered", "document_type": "kra_pin"}, "files", evil); r.status != 422 {
		t.Fatalf("active PDF accepted: %s", r.raw)
	}
	cust.expect(cust.do("GET", "/api/v1/admin/verifications", nil), 403)
	qv := mod.expect(mod.do("GET", "/api/v1/admin/verifications?status=pending", nil), 200).body["data"].([]any)
	if len(qv) != 1 {
		t.Fatalf("verification queue = %v", qv)
	}
	rec := qv[0].(map[string]any)
	rid := rec["id"].(string)
	// Moderators see the queue but only verifiers decide.
	mod.expect(mod.do("PATCH", "/api/v1/admin/verifications/"+rid, map[string]any{"decision": "approve"}), 403)
	detail := admin.expect(admin.do("GET", "/api/v1/admin/verifications/"+rid, nil), 200).data()
	doc := detail["documents"].([]any)[0].(map[string]any)
	if s, _, _ := cust.page("/api/v1/admin/media/" + doc["media_id"].(string) + "/download"); s != 403 {
		t.Fatal("private document reachable by a customer")
	}
	if s, _, _ := admin.page("/api/v1/admin/media/" + doc["media_id"].(string) + "/download"); s != 200 || e.audits("media.private.viewed") != 1 {
		t.Fatal("document download not served or not audited")
	}
	admin.expect(admin.do("PATCH", "/api/v1/admin/verifications/"+rid, map[string]any{"decision": "approve"}), 200)

	// Badge shows publicly once the listing is published.
	completeListing(t, prov, "/api/v1/provider", cat)
	prov.expect(prov.do("POST", "/api/v1/provider/profile/submit", nil), 200)
	admin.expect(admin.do("POST", "/api/v1/admin/providers/"+pid+"/status", map[string]any{"action": "approve"}), 200)
	_, _, body := cust.page("/professionals/" + v["slug"].(string))
	if !strings.Contains(body, "Identity") || !strings.Contains(body, "/badges") {
		t.Fatalf("badge not public: %.500s", body)
	}
	// logged_in_only email: anonymous visitors get a sign-in link.
	anon := e.client()
	if _, _, body := anon.page("/professionals/" + v["slug"].(string) + "?reveal=email"); strings.Contains(body, "sam@example.test") || !strings.Contains(body, "/auth/login?next=") {
		t.Fatal("logged_in_only email exposed to anonymous visitor")
	}
	if _, _, body := cust.page("/professionals/" + v["slug"].(string) + "?reveal=email"); !strings.Contains(body, "sam@example.test") {
		t.Fatal("signed-in visitor cannot reveal email")
	}
	if s, _, body := cust.page("/badges"); s != 200 || !strings.Contains(body, "Identity") {
		t.Fatal("badges page")
	}
}

func TestLocationMergeRepointsProviders(t *testing.T) {
	e := newEnv(t)
	_, admin, _ := admins(t, e)
	cat := setupCatalogue(t, e, admin)
	prov := e.client()
	prov.register("Lucy", "Njeri", "0716161616")
	prov.expect(prov.do("POST", "/api/v1/provider/profile", map[string]any{"provider_type": "individual", "display_name": "Lucy Njeri", "accept_terms": true}), 201)
	prov.expect(prov.do("PUT", "/api/v1/provider/service-areas", map[string]any{"base_area_id": cat.areaID,
		"coverage": []map[string]any{{"type": "area", "id": cat.areaID}, {"type": "area", "id": cat.area2ID}},
		"pin":      map[string]any{"latitude": -1.2635, "longitude": 36.8055}}), 200)
	prov.expect(prov.do("PUT", "/api/v1/provider/service-areas", map[string]any{"base_area_id": cat.areaID,
		"pin": map[string]any{"latitude": 51.5, "longitude": -0.12}}), 422)
	admin.expect(admin.do("POST", "/api/v1/admin/locations/merge", map[string]any{"type": "area", "source_id": cat.areaID, "target_id": cat.area2ID}), 200)
	v := prov.expect(prov.do("GET", "/api/v1/provider/profile", nil), 200).data()
	if v["base_area"].(map[string]any)["id"] != cat.area2ID {
		t.Fatalf("base area not re-pointed: %v", v["base_area"])
	}
	if cov := v["coverage"].([]any); len(cov) != 1 || cov[0].(map[string]any)["id"] != cat.area2ID {
		t.Fatalf("coverage = %v", cov)
	}
	pin := v["pin"].(map[string]any)
	if pin["latitude"].(float64) != -1.2635 || pin["longitude"].(float64) != 36.8055 {
		t.Fatalf("pin = %v", pin)
	}
}

func fmtJSON(v any) string {
	var b bytes.Buffer
	_ = jsonEncode(&b, v)
	return b.String()
}
