package i18n_test

import (
	"testing"

	"github.com/alumasinde/profy/internal/i18n"
	"github.com/alumasinde/profy/web"
)

func TestCataloguesLoadAndTranslationsAreSubsetOfEnglish(t *testing.T) {
	c, err := i18n.Load(web.FS, "i18n")
	if err != nil {
		t.Fatal(err)
	}
	en := map[string]bool{}
	for _, k := range c.Keys("en") {
		en[k] = true
	}
	for _, loc := range c.Locales() {
		for _, k := range c.Keys(loc) {
			if !en[k] {
				t.Errorf("%s defines %q which is missing from en (the source catalogue)", loc, k)
			}
		}
	}
}

func TestTranslateFallbackAndPlaceholders(t *testing.T) {
	c, err := i18n.Load(web.FS, "i18n")
	if err != nil {
		t.Fatal(err)
	}
	if got := c.T("sw", "public.nav.login"); got != "Ingia" {
		t.Errorf("sw login = %q", got)
	}
	if got := c.T("sw", "public.home.how_title"); got != c.T("en", "public.home.how_title") {
		t.Errorf("missing sw key must fall back to en, got %q", got)
	}
	if got := c.T("en", "otp.sms.body", "code", "123456", "site_name", "X", "minutes", 5); got != "123456 is your X code. Expires in 5 min. Never share it." {
		t.Errorf("placeholder substitution: %q", got)
	}
	if got := c.T("en", "no.such.key"); got != "no.such.key" {
		t.Errorf("missing key should render as key, got %q", got)
	}
}
