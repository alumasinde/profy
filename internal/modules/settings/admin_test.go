package settings

import (
	"encoding/json"
	"math"
	"testing"
)

func TestContrastMatchesWCAG(t *testing.T) {
	if r := Contrast("#000000", "#ffffff"); math.Abs(r-21) > 0.01 {
		t.Fatalf("black/white = %.2f", r)
	}
	if r := Contrast("#777777", "#ffffff"); math.Abs(r-4.48) > 0.02 {
		t.Fatalf("#777/white = %.2f", r)
	}
}

func TestDefaultThemePassesAndBadColoursFail(t *testing.T) {
	if errs := ContrastErrors("#0f766e", "#1e293b", "#b45309"); len(errs) != 0 {
		t.Fatalf("seeded theme must pass: %v", errs)
	}
	errs := ContrastErrors("#facc15", "#cbd5e1", "#f1f5f9")
	for _, k := range []string{KeyPrimaryColor, KeySecondaryColor, KeyAccentColor} {
		if errs[k] != "contrast_too_low" {
			t.Errorf("%s not rejected", k)
		}
	}
}

func TestValidateValueNormalizes(t *testing.T) {
	has := func(l string) bool { return l == "en" }
	d, _ := definition("support_phone")
	v, code := validateValue(d, json.RawMessage(`"0712 345 678"`), has)
	if code != "" || string(v) != `"+254712345678"` {
		t.Fatalf("phone: %s %s", v, code)
	}
	d, _ = definition(KeySiteURL)
	if _, code := validateValue(d, json.RawMessage(`"https://fundihub.co.ke/path"`), has); code != "invalid" {
		t.Fatal("site_url with a path must be rejected")
	}
	if v, code := validateValue(d, json.RawMessage(`"https://fundihub.co.ke/"`), has); code != "" || string(v) != `"https://fundihub.co.ke"` {
		t.Fatalf("site_url: %s %s", v, code)
	}
	d, _ = definition(KeyOTPChannels)
	if _, code := validateValue(d, json.RawMessage(`[]`), has); code != "required" {
		t.Fatal("empty channel list accepted")
	}
	d, _ = definition(KeyDefaultLocale)
	if _, code := validateValue(d, json.RawMessage(`"fr"`), has); code != "invalid" {
		t.Fatal("locale without catalogue accepted")
	}
}
