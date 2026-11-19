// Package text holds slug and search-term normalization shared by catalogue, pages and locations.
package text

import (
	"regexp"
	"strings"
	"unicode"
)

// fold maps common Latin letters with diacritics to ASCII for slugs and search keys.
var fold = strings.NewReplacer(
	"à", "a", "á", "a", "â", "a", "ã", "a", "ä", "a", "å", "a", "æ", "ae", "ç", "c",
	"è", "e", "é", "e", "ê", "e", "ë", "e", "ì", "i", "í", "i", "î", "i", "ï", "i",
	"ñ", "n", "ò", "o", "ó", "o", "ô", "o", "õ", "o", "ö", "o", "ø", "o", "ù", "u",
	"ú", "u", "û", "u", "ü", "u", "ý", "y", "ÿ", "y", "ß", "ss", "œ", "oe",
	"’", "", "'", "", "&", " and ",
)

// SlugPattern is the canonical public slug form: lowercase ASCII words joined by single hyphens.
var SlugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// MaxSlug is the longest accepted slug.
const MaxSlug = 120

// Slugify derives a slug from a display name ("Murang'a Town" → "muranga-town").
func Slugify(s string) string {
	s = fold.Replace(strings.ToLower(strings.TrimSpace(s)))
	var b strings.Builder
	dash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.TrimSuffix(b.String(), "-")
	if len(out) > MaxSlug {
		out = strings.TrimRight(out[:MaxSlug], "-")
	}
	return out
}

// ValidSlug reports whether s is a canonical slug.
func ValidSlug(s string) bool { return len(s) <= MaxSlug && SlugPattern.MatchString(s) }

// NormalizeTerm prepares search terms (synonyms, aliases) for exact matching:
// lowercase, diacritics folded, punctuation removed, whitespace collapsed.
func NormalizeTerm(s string) string {
	s = fold.Replace(strings.ToLower(s))
	var b strings.Builder
	space := false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			space = false
		case !space && b.Len() > 0:
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(b.String())
}

// CleanLine trims and rejects control characters; it returns ok=false for empty or invalid input.
func CleanLine(s string, max int) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" || len([]rune(s)) > max {
		return s, false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return s, false
		}
	}
	return s, true
}

// CleanText trims multi-line text, allowing newlines and tabs but no other control characters.
func CleanText(s string, max int) (string, bool) {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	if len([]rune(s)) > max {
		return s, false
	}
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return s, false
		}
	}
	return s, true
}
