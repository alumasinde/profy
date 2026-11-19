package users

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxNameLength matches the VARCHAR(100) columns.
const MaxNameLength = 100

// Names is the mandatory three-part name (§11.2). Middle is nil when absent.
type Names struct {
	First  string
	Middle *string
	Last   string
}

// Display composes the display name from non-empty parts: first, middle, last.
func (n Names) Display() string {
	parts := []string{n.First}
	if n.Middle != nil && *n.Middle != "" {
		parts = append(parts, *n.Middle)
	}
	parts = append(parts, n.Last)
	return strings.Join(parts, " ")
}

// NormalizeNames trims and validates names, returning per-field error codes.
// Unicode letters and marks, spaces, apostrophes (' and ’), hyphens and periods are
// allowed. Spelling and case are never changed. Empty middle names become nil.
func NormalizeNames(first string, middle *string, last string) (Names, map[string]string) {
	errs := map[string]string{}
	out := Names{First: strings.TrimSpace(first), Last: strings.TrimSpace(last)}
	if code := checkName(out.First); code != "" {
		errs["first_name"] = code
	}
	if code := checkName(out.Last); code != "" {
		errs["last_name"] = code
	}
	if middle != nil {
		m := strings.TrimSpace(*middle)
		if m != "" {
			if code := checkName(m); code != "" {
				errs["middle_name"] = code
			}
			out.Middle = &m
		}
	}
	return out, errs
}

func checkName(s string) string {
	if s == "" {
		return "required"
	}
	if !utf8.ValidString(s) {
		return "invalid"
	}
	if utf8.RuneCountInString(s) > MaxNameLength {
		return "too_long"
	}
	letters := 0
	prevSpace := false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r):
			letters++
			prevSpace = false
		case r == ' ':
			if prevSpace {
				return "invalid"
			}
			prevSpace = true
		case r == '\'' || r == '’' || r == '-' || r == '.':
			prevSpace = false
		default:
			return "invalid" // digits, control characters, markup, emoji
		}
	}
	if letters == 0 {
		return "invalid"
	}
	return ""
}
