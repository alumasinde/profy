package integration

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/alumasinde/profy/internal/app"
)

func TestRegisterVerifySessionProfileLogout(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	c.register("Wanjiru", "Kamau", "0712 345 678")

	s := c.expect(c.do("GET", "/api/v1/auth/session", nil), 200).data()
	if s["authenticated"] != true {
		t.Fatalf("not authenticated: %v", s)
	}
	u := s["user"].(map[string]any)
	if u["first_name"] != "Wanjiru" || u["last_name"] != "Kamau" || u["middle_name"] != nil || u["phone"] != "+254712345678" {
		t.Fatalf("user = %v", u)
	}
	if roles := u["roles"].([]any); len(roles) != 1 || roles[0] != "customer" {
		t.Fatalf("roles = %v", roles)
	}
	if e.count(`SELECT COUNT(*) FROM user_consents WHERE consent_type IN ('terms','privacy')`) != 2 {
		t.Fatal("consents not recorded")
	}

	p := c.expect(c.do("PATCH", "/api/v1/customer/profile", map[string]any{
		"first_name": "Wanjiru", "middle_name": "Njeri", "last_name": "Kamau", "preferred_locale": "sw",
	}), 200).data()
	if p["display_name"] != "Wanjiru Njeri Kamau" || p["preferred_locale"] != "sw" {
		t.Fatalf("profile = %v", p)
	}
	bad := c.do("PATCH", "/api/v1/customer/profile", map[string]any{"first_name": "", "last_name": "K4mau"})
	if bad.status != 422 || !strings.Contains(bad.raw, `"first_name":"required"`) || !strings.Contains(bad.raw, `"last_name":"invalid"`) {
		t.Fatalf("validation: %s", bad.raw)
	}

	c.expect(c.do("POST", "/api/v1/auth/logout", nil), 204)
	if c.do("GET", "/api/v1/auth/session", nil).data()["authenticated"] != false {
		t.Fatal("still authenticated after logout")
	}
	c.expect(c.do("GET", "/api/v1/customer/profile", nil), 401)
}

func TestRegistrationValidation(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	r := c.expect(c.do("POST", "/api/v1/auth/register", map[string]any{
		"first_name": " ", "last_name": "Kamau", "destination": "0812345678", "accept_terms": false,
	}), 422)
	for _, f := range []string{`"first_name":"required"`, `"destination":"invalid"`, `"accept_terms":"required"`} {
		if !strings.Contains(r.raw, f) {
			t.Errorf("missing %s in %s", f, r.raw)
		}
	}
	// Unknown JSON fields are rejected.
	c.expect(c.do("POST", "/api/v1/auth/register", map[string]any{"full_name": "X"}), 400)
}

func TestOTPIsOneTimePurposeBoundAndAttemptLimited(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	r := c.expect(c.do("POST", "/api/v1/auth/register", map[string]any{
		"first_name": "A", "last_name": "B", "destination": "a@example.test", "accept_terms": true,
	}), 202)
	id := r.data()["challenge_id"].(string)
	code := e.lastCode("a@example.test")
	if e.count(`SELECT COUNT(*) FROM otp_challenges WHERE code_hash = ?`, code) != 0 {
		t.Fatal("plaintext code stored")
	}
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	for i := 0; i < 5; i++ {
		if got := c.do("POST", "/api/v1/auth/otp/verify", map[string]any{"challenge_id": id, "code": wrong}); got.errCode() != "invalid_or_expired_code" {
			t.Fatalf("attempt %d: %s", i, got.raw)
		}
	}
	// Max attempts reached: even the right code is refused now.
	c.expect(c.do("POST", "/api/v1/auth/otp/verify", map[string]any{"challenge_id": id, "code": code}), 401)

	// A fresh code works exactly once.
	e.exec(`UPDATE otp_challenges SET created_at = created_at - INTERVAL 2 HOUR`) // skip resend cooldown
	r = c.expect(c.do("POST", "/api/v1/auth/register", map[string]any{
		"first_name": "A", "last_name": "B", "destination": "a@example.test", "accept_terms": true,
	}), 202)
	id2 := r.data()["challenge_id"].(string)
	code2 := e.lastCode("a@example.test")
	c.expect(c.do("POST", "/api/v1/auth/otp/verify", map[string]any{"challenge_id": id2, "code": code2}), 200)
	c2 := e.client()
	c2.expect(c2.do("POST", "/api/v1/auth/otp/verify", map[string]any{"challenge_id": id2, "code": code2}), 401)

	// Purpose-bound: a login challenge cannot complete recovery.
	e.exec(`UPDATE otp_challenges SET created_at = created_at - INTERVAL 2 HOUR`)
	r = c2.expect(c2.do("POST", "/api/v1/auth/otp/send", map[string]any{"destination": "a@example.test", "purpose": "login"}), 202)
	c2.expect(c2.do("POST", "/api/v1/auth/recovery/complete", map[string]any{
		"challenge_id": r.data()["challenge_id"], "code": e.lastCode("a@example.test"),
	}), 401)

	// Expired codes are refused.
	e.exec(`UPDATE otp_challenges SET created_at = created_at - INTERVAL 2 HOUR`)
	r = c2.expect(c2.do("POST", "/api/v1/auth/otp/send", map[string]any{"destination": "a@example.test"}), 202)
	e.exec(`UPDATE otp_challenges SET expires_at = NOW(3) - INTERVAL 1 SECOND`)
	c2.expect(c2.do("POST", "/api/v1/auth/otp/verify", map[string]any{
		"challenge_id": r.data()["challenge_id"], "code": e.lastCode("a@example.test"),
	}), 401)
}

func TestOTPResendCooldownAndDailyCap(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	c.register("A", "B", "0711000001")
	c.expect(c.do("POST", "/api/v1/auth/logout", nil), 204)
	e.exec(`UPDATE otp_challenges SET created_at = created_at - INTERVAL 2 HOUR`)

	c.expect(c.do("POST", "/api/v1/auth/otp/send", map[string]any{"destination": "0711000001"}), 202)
	r := c.expect(c.do("POST", "/api/v1/auth/otp/send", map[string]any{"destination": "0711000001"}), 429)
	if r.errCode() != "rate_limited" {
		t.Fatal(r.raw)
	}
	if ra, _ := strconv.Atoi(r.header.Get("Retry-After")); ra < 1 || ra > 60 {
		t.Fatalf("Retry-After = %q", r.header.Get("Retry-After"))
	}
	// Daily cap per destination.
	for i := 0; i < 10; i++ {
		e.exec(`INSERT INTO otp_challenges (public_id, purpose, channel, destination, code_hash, expires_at, created_at)
			VALUES (UNHEX(REPLACE(UUID(),'-','')), 'login', 'sms', '+254711000001', REPEAT('x',32), NOW(3), NOW(3) - INTERVAL 3 HOUR)`)
	}
	c.expect(c.do("POST", "/api/v1/auth/otp/send", map[string]any{"destination": "0711000001"}), 429)
}

func TestLoginDoesNotRevealAccountExistence(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	c.register("Known", "User", "known@example.test")
	c.expect(c.do("POST", "/api/v1/auth/logout", nil), 204)
	e.exec(`UPDATE otp_challenges SET created_at = created_at - INTERVAL 2 HOUR`)

	known := c.expect(c.do("POST", "/api/v1/auth/otp/send", map[string]any{"destination": "known@example.test"}), 202).data()
	unknown := c.expect(c.do("POST", "/api/v1/auth/otp/send", map[string]any{"destination": "nobody@example.test"}), 202).data()
	for _, k := range []string{"challenge_id", "channel", "destination_masked", "expires_in", "resend_after"} {
		if (known[k] == nil) != (unknown[k] == nil) {
			t.Errorf("response shape differs at %q: %v vs %v", k, known, unknown)
		}
	}
	if known["expires_in"] != unknown["expires_in"] || known["resend_after"] != unknown["resend_after"] {
		t.Errorf("timing fields differ: %v vs %v", known, unknown)
	}
	if e.sentCount("nobody@example.test") != 0 {
		t.Fatal("a code was sent to an unknown destination")
	}
	// Registering an existing destination looks identical and signs the owner in,
	// without changing their names.
	e.exec(`UPDATE otp_challenges SET created_at = created_at - INTERVAL 2 HOUR`)
	r := c.expect(c.do("POST", "/api/v1/auth/register", map[string]any{
		"first_name": "Someone", "last_name": "Else", "destination": "known@example.test", "accept_terms": true,
	}), 202)
	v := c.expect(c.do("POST", "/api/v1/auth/otp/verify", map[string]any{
		"challenge_id": r.data()["challenge_id"], "code": e.lastCode("known@example.test"),
	}), 200).data()
	if v["new_user"] != false {
		t.Fatal("existing destination must sign in, not create a user")
	}
	if e.count(`SELECT COUNT(*) FROM users WHERE first_name = 'Known'`) != 1 || e.count(`SELECT COUNT(*) FROM users`) != 1 {
		t.Fatal("registration on an existing destination changed or duplicated the account")
	}
}

func TestCSRFAndOriginProtection(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	body := map[string]any{"destination": "x@example.test"}
	if r := c.do("POST", "/api/v1/auth/otp/send", body, "X-CSRF-Token", ""); r.status != 403 || r.errCode() != "csrf_failed" {
		t.Fatalf("missing token: %s", r.raw)
	}
	if r := c.do("POST", "/api/v1/auth/otp/send", body, "X-CSRF-Token", "forged-forged-forged-forged-forged-forged"); r.status != 403 {
		t.Fatalf("forged token: %s", r.raw)
	}
	if r := c.do("POST", "/api/v1/auth/otp/send", body, "Origin", "https://evil.example"); r.status != 403 {
		t.Fatalf("cross origin: %s", r.raw)
	}
	c.expect(c.do("POST", "/api/v1/auth/otp/send", body, "Origin", e.srv.URL), 202)
}

func TestPINSetupLoginLockoutAndRecovery(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	c.register("Pin", "User", "0722000002")

	// Re-verification is required for sensitive changes.
	e.exec(`UPDATE user_sessions SET reauthenticated_at = NULL`)
	if r := c.do("POST", "/api/v1/auth/pin/setup", map[string]any{"pin": "480317"}); r.errCode() != "reauthentication_required" {
		t.Fatalf("expected reauth: %s", r.raw)
	}
	e.exec(`UPDATE otp_challenges SET created_at = created_at - INTERVAL 2 HOUR`) // past the resend cooldown
	r := c.expect(c.do("POST", "/api/v1/auth/otp/send", map[string]any{"purpose": "sensitive_change"}), 202)
	c.expect(c.do("POST", "/api/v1/auth/otp/verify", map[string]any{
		"challenge_id": r.data()["challenge_id"], "code": e.lastCode("+254722000002"),
	}), 200)

	if r := c.do("POST", "/api/v1/auth/pin/setup", map[string]any{"pin": "123456"}); r.errCode() != "weak_pin" || !strings.Contains(r.raw, "pin_sequence") {
		t.Fatalf("weak pin accepted: %s", r.raw)
	}
	c.expect(c.do("POST", "/api/v1/auth/pin/setup", map[string]any{"pin": "480317"}), 204)
	var hash string
	if err := testDB.QueryRow(`SELECT secret_hash FROM user_credentials`).Scan(&hash); err != nil || !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("pin not argon2id-hashed: %q %v", hash, err)
	}
	if e.count(`SELECT COUNT(*) FROM audit_logs WHERE action = 'auth.pin.set'`) != 1 {
		t.Fatal("pin change not audited")
	}
	c.expect(c.do("POST", "/api/v1/auth/logout", nil), 204)

	// PIN only works on a device that completed OTP login.
	stranger := e.client()
	if r := stranger.do("POST", "/api/v1/auth/pin/login", map[string]any{"destination": "0722000002", "pin": "480317"}); r.errCode() != "invalid_credentials" {
		t.Fatalf("pin accepted on untrusted device: %s", r.raw)
	}
	c.expect(c.do("POST", "/api/v1/auth/pin/login", map[string]any{"destination": "0722000002", "pin": "480317"}), 200)
	c.expect(c.do("POST", "/api/v1/auth/logout", nil), 204)

	// Progressive delay, then lockout requiring OTP.
	for i := 1; i <= 3; i++ {
		c.expect(c.do("POST", "/api/v1/auth/pin/login", map[string]any{"destination": "0722000002", "pin": "905162"}), 401)
	}
	c.expect(c.do("POST", "/api/v1/auth/pin/login", map[string]any{"destination": "0722000002", "pin": "480317"}), 429)
	for i := 4; i <= 6; i++ {
		e.exec(`UPDATE user_credentials SET blocked_until = NULL`)
		c.expect(c.do("POST", "/api/v1/auth/pin/login", map[string]any{"destination": "0722000002", "pin": "905162"}), 401)
	}
	e.exec(`UPDATE user_credentials SET blocked_until = NULL`)
	if r := c.do("POST", "/api/v1/auth/pin/login", map[string]any{"destination": "0722000002", "pin": "480317"}); r.errCode() != "pin_locked" {
		t.Fatalf("expected lockout: %s", r.raw)
	}

	// Recovery: OTP unlocks, replaces the PIN and signs out every other session.
	other := e.client()
	e.exec(`UPDATE otp_challenges SET created_at = created_at - INTERVAL 2 HOUR`)
	other.login("0722000002")
	e.exec(`UPDATE otp_challenges SET created_at = created_at - INTERVAL 2 HOUR`)
	r = c.expect(c.do("POST", "/api/v1/auth/recovery/start", map[string]any{"destination": "0722000002"}), 202)
	c.expect(c.do("POST", "/api/v1/auth/recovery/complete", map[string]any{
		"challenge_id": r.data()["challenge_id"], "code": e.lastCode("+254722000002"), "new_pin": "730284",
	}), 200)
	if other.do("GET", "/api/v1/auth/session", nil).data()["authenticated"] != false {
		t.Fatal("other session survived recovery")
	}
	c.expect(c.do("POST", "/api/v1/auth/logout", nil), 204)
	c.expect(c.do("POST", "/api/v1/auth/pin/login", map[string]any{"destination": "0722000002", "pin": "730284"}), 200)
}

func TestSessionListAndRevocation(t *testing.T) {
	e := newEnv(t)
	a := e.client()
	a.register("Multi", "Device", "0733000003")
	b := e.client()
	e.exec(`UPDATE otp_challenges SET created_at = created_at - INTERVAL 2 HOUR`)
	b.login("0733000003")

	list := a.expect(a.do("GET", "/api/v1/auth/sessions", nil), 200).body["data"].([]any)
	if len(list) != 2 {
		t.Fatalf("sessions = %d", len(list))
	}
	var otherID string
	for _, s := range list {
		m := s.(map[string]any)
		if m["is_current"] == false {
			otherID = m["id"].(string)
		}
	}
	// Another user cannot revoke this user's session.
	mallory := e.client()
	mallory.register("Mal", "Lory", "0744000004")
	mallory.expect(mallory.do("DELETE", "/api/v1/auth/sessions/"+otherID, nil), 404)

	a.expect(a.do("DELETE", "/api/v1/auth/sessions/"+otherID, nil), 204)
	if b.do("GET", "/api/v1/auth/session", nil).data()["authenticated"] != false {
		t.Fatal("revoked session still valid")
	}
}

func TestSuperAdminBootstrapAndAuthorizationMatrix(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, _, err := app.CreateSuperAdmin(ctx, testDB, app.AdminInput{FirstName: "Root", LastName: "Admin", Email: "root@example.test"}); err != nil {
		t.Fatal(err)
	}
	root := e.client()
	root.login("root@example.test")

	cust := e.client()
	cust.register("Plain", "Customer", "0755000005")
	anon := e.client()

	endpoints := []struct{ method, path string }{
		{"GET", "/api/v1/admin/users"},
		{"GET", "/api/v1/admin/roles"},
		{"GET", "/api/v1/admin/permissions"},
		{"GET", "/api/v1/admin/audit-logs"},
		{"PUT", "/api/v1/admin/roles/support/permissions"},
	}
	for _, ep := range endpoints {
		body := any(nil)
		if ep.method == "PUT" {
			body = map[string]any{"permissions": []string{"dashboard.view"}}
		}
		if r := anon.do(ep.method, ep.path, body); r.status != 401 {
			t.Errorf("anon %s %s = %d", ep.method, ep.path, r.status)
		}
		if r := cust.do(ep.method, ep.path, body); r.status != 403 {
			t.Errorf("customer %s %s = %d", ep.method, ep.path, r.status)
		}
		if r := root.do(ep.method, ep.path, body); r.status != 200 {
			t.Errorf("super admin %s %s = %d %s", ep.method, ep.path, r.status, r.raw)
		}
	}

	// Find the customer and make them an administrator.
	users := root.expect(root.do("GET", "/api/v1/admin/users?q=Plain", nil), 200).body["data"].([]any)
	custID := users[0].(map[string]any)["public_id"].(string)
	root.expect(root.do("PUT", "/api/v1/admin/users/"+custID+"/roles", map[string]any{"roles": []string{"customer", "administrator"}}), 200)
	if e.count(`SELECT COUNT(*) FROM audit_logs WHERE action = 'users.roles.updated'`) != 1 {
		t.Fatal("role change not audited")
	}

	// Administrator: can list users, cannot edit role permissions or grant protected roles.
	cust.expect(cust.do("GET", "/api/v1/admin/users", nil), 200)
	cust.expect(cust.do("PUT", "/api/v1/admin/roles/support/permissions", map[string]any{"permissions": []string{}}), 403)
	cust.expect(cust.do("PUT", "/api/v1/admin/users/"+custID+"/roles", map[string]any{"roles": []string{"customer", "administrator", "super_administrator"}}), 403)

	// The super administrator role cannot be edited, and the last one cannot be removed.
	root.expect(root.do("PUT", "/api/v1/admin/roles/super_administrator/permissions", map[string]any{"permissions": []string{}}), 409)
	rootID := root.do("GET", "/api/v1/auth/session", nil).data()["user"].(map[string]any)["public_id"].(string)
	root.expect(root.do("PUT", "/api/v1/admin/users/"+rootID+"/roles", map[string]any{"roles": []string{"customer"}}), 409)

	// Unknown permission keys are rejected.
	root.expect(root.do("PUT", "/api/v1/admin/roles/support/permissions", map[string]any{"permissions": []string{"admin.access"}}), 422)

	// Suspension: needs a reason, revokes sessions, is audited; restoring works.
	root.expect(root.do("PATCH", "/api/v1/admin/users/"+custID+"/status", map[string]any{"status": "suspended"}), 422)
	root.expect(root.do("PATCH", "/api/v1/admin/users/"+custID+"/status", map[string]any{"status": "suspended", "reason": "Fraud report"}), 200)
	if cust.do("GET", "/api/v1/auth/session", nil).data()["authenticated"] != false {
		t.Fatal("suspended user still signed in")
	}
	root.expect(root.do("PATCH", "/api/v1/admin/users/"+custID+"/status", map[string]any{"status": "active"}), 200)
	if e.count(`SELECT COUNT(*) FROM audit_logs WHERE action = 'users.status.updated'`) != 2 {
		t.Fatal("status changes not audited")
	}

	// Pagination is bounded and cursor-based.
	page := root.expect(root.do("GET", "/api/v1/admin/users?limit=1", nil), 200)
	if len(page.body["data"].([]any)) != 1 || page.body["meta"].(map[string]any)["next_cursor"] == nil {
		t.Fatalf("pagination: %s", page.raw)
	}
	root.expect(root.do("GET", "/api/v1/admin/users?cursor=@@bad", nil), 422)
}
