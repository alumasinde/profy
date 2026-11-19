package text

import "testing"

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Murang'a Town":          "muranga-town",
		"  Kahawa  West ":        "kahawa-west",
		"Électricité & Plumbing": "electricite-and-plumbing",
		"Ruaka/Two Rivers":       "ruaka-two-rivers",
		"---":                    "",
		"Nairobi CBD 2":          "nairobi-cbd-2",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidSlug(t *testing.T) {
	for _, ok := range []string{"a", "plumbing", "kahawa-nairobi", "a1-b2"} {
		if !ValidSlug(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "-a", "a-", "a--b", "A", "a b", "a_b", "é"} {
		if ValidSlug(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestNormalizeTerm(t *testing.T) {
	if got := NormalizeTerm("  Fundi wa  MAJI! "); got != "fundi wa maji" {
		t.Errorf("got %q", got)
	}
	if got := NormalizeTerm("Murang’a"); got != "muranga" {
		t.Errorf("got %q", got)
	}
}
