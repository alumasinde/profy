package security

import (
	"testing"
	"time"
)

func TestNormalizeKenyanPhone(t *testing.T) {
	cases := map[string]string{
		"0712345678":       "+254712345678",
		"0712 345 678":     "+254712345678",
		"0110123456":       "+254110123456",
		"0101-234-567":     "+254101234567",
		"254712345678":     "+254712345678",
		"+254712345678":    "+254712345678",
		"+254 110 123 456": "+254110123456",
		"(0712) 345678":    "+254712345678",
	}
	for in, want := range cases {
		got, ok := NormalizeKenyanPhone(in)
		if !ok || got != want {
			t.Errorf("NormalizeKenyanPhone(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "0812345678", "071234567", "07123456789", "+255712345678", "0712a45678", "712345678", "++254712345678", "0712+345678"} {
		if got, ok := NormalizeKenyanPhone(bad); ok {
			t.Errorf("NormalizeKenyanPhone(%q) = %q; want invalid", bad, got)
		}
	}
}

func TestNormalizeEmail(t *testing.T) {
	if got, ok := NormalizeEmail("  Jane.Doe@Example.CO.KE "); !ok || got != "jane.doe@example.co.ke" {
		t.Fatalf("got %q %v", got, ok)
	}
	for _, bad := range []string{"", "jane", "jane@", "@x.com", "a b@x.com", "Jane <jane@x.com>", "jane@localhost"} {
		if _, ok := NormalizeEmail(bad); ok {
			t.Errorf("NormalizeEmail(%q) accepted", bad)
		}
	}
}

func TestValidatePIN(t *testing.T) {
	weak := map[string]string{
		"12345":   "pin_length",
		"1234567": "pin_length",
		"12a456":  "pin_digits_only",
		"000000":  "pin_too_simple",
		"121212":  "pin_too_simple",
		"112211":  "pin_too_simple",
		"123456":  "pin_sequence",
		"654321":  "pin_sequence",
		"890123":  "pin_sequence",
		"147258":  "pin_common",
		"840840":  "pin_common",
	}
	for pin, want := range weak {
		if got := ValidatePIN(pin); got != want {
			t.Errorf("ValidatePIN(%q) = %q, want %q", pin, got, want)
		}
	}
	for _, ok := range []string{"480317", "905162", "730284"} {
		if got := ValidatePIN(ok); got != "" {
			t.Errorf("ValidatePIN(%q) = %q, want accepted", ok, got)
		}
	}
}

func TestHashSecretRoundTrip(t *testing.T) {
	h, err := HashSecret("480317")
	if err != nil {
		t.Fatal(err)
	}
	h2, _ := HashSecret("480317")
	if h == h2 {
		t.Fatal("hashes must use unique salts")
	}
	if ok, err := VerifySecret("480317", h); err != nil || !ok {
		t.Fatalf("verify correct: %v %v", ok, err)
	}
	if ok, _ := VerifySecret("480318", h); ok {
		t.Fatal("verify wrong secret succeeded")
	}
	if _, err := VerifySecret("x", "$bcrypt$nope"); err == nil {
		t.Fatal("malformed hash accepted")
	}
}

func TestOTPHasherBindsChallenge(t *testing.T) {
	h := NewOTPHasher([]byte("0123456789abcdef0123456789abcdef"))
	a := h.Hash([]byte("challenge-a"), "123456")
	if !Equal(a, h.Hash([]byte("challenge-a"), "123456")) {
		t.Fatal("hash not deterministic")
	}
	if Equal(a, h.Hash([]byte("challenge-b"), "123456")) {
		t.Fatal("hash must differ per challenge")
	}
	other := NewOTPHasher([]byte("ffffffffffffffffffffffffffffffff"))
	if Equal(a, other.Hash([]byte("challenge-a"), "123456")) {
		t.Fatal("hash must depend on the server key")
	}
}

func TestRandomDigits(t *testing.T) {
	for i := 0; i < 200; i++ {
		d := RandomDigits(6)
		if len(d) != 6 {
			t.Fatalf("len %d", len(d))
		}
		for _, r := range d {
			if r < '0' || r > '9' {
				t.Fatalf("non-digit in %q", d)
			}
		}
	}
}

func TestLimiter(t *testing.T) {
	l := NewLimiter()
	now := time.Unix(1_700_000_000, 0)
	l.now = func() time.Time { return now }
	lim := Limit{Max: 3, Window: time.Minute}
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("k", lim); !ok {
			t.Fatalf("call %d denied", i)
		}
	}
	ok, retry := l.Allow("k", lim)
	if ok || retry <= 0 || retry > time.Minute {
		t.Fatalf("4th call: ok=%v retry=%v", ok, retry)
	}
	if ok, _ := l.Allow("other", lim); !ok {
		t.Fatal("keys must be independent")
	}
	now = now.Add(time.Minute)
	if ok, _ := l.Allow("k", lim); !ok {
		t.Fatal("window did not reset")
	}
	if ok, _ := l.Allow("k", Limit{}); ok {
		t.Fatal("invalid limit must fail closed")
	}
}

func TestMaskDestination(t *testing.T) {
	if got := MaskDestination("+254712345678"); got != "+2547•••••678" {
		t.Errorf("phone mask %q", got)
	}
	if got := MaskDestination("jane@example.com"); got != "j•••@example.com" {
		t.Errorf("email mask %q", got)
	}
}
