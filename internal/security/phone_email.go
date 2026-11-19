package security

import (
	"net/mail"
	"strings"
	"unicode"
)

// NormalizeKenyanPhone converts accepted Kenyan mobile inputs to E.164 (§11.1).
// Accepted: 07XXXXXXXX, 01XXXXXXXX, 2547…/2541…, +2547…/+2541… (spaces, dashes and
// parentheses ignored). Returns "" and false when the input is not a valid Kenyan mobile.
func NormalizeKenyanPhone(input string) (string, bool) {
	var b strings.Builder
	for i, r := range strings.TrimSpace(input) {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '+' && i == 0:
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '(' || r == ')':
		default:
			return "", false
		}
	}
	s := b.String()
	var national string // 9 digits starting with 7 or 1
	switch {
	case strings.HasPrefix(s, "+254"):
		national = s[4:]
	case strings.HasPrefix(s, "254"):
		national = s[3:]
	case strings.HasPrefix(s, "0"):
		national = s[1:]
	default:
		return "", false
	}
	if len(national) != 9 || (national[0] != '7' && national[0] != '1') {
		return "", false
	}
	for _, r := range national {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	return "+254" + national, true
}

// NormalizeEmail trims and lower-cases an email and validates its basic shape.
func NormalizeEmail(input string) (string, bool) {
	s := strings.ToLower(strings.TrimSpace(input))
	if len(s) < 3 || len(s) > 254 {
		return "", false
	}
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Address != s || addr.Name != "" {
		return "", false
	}
	at := strings.LastIndexByte(s, '@')
	if at < 1 || !strings.Contains(s[at+1:], ".") {
		return "", false
	}
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return "", false
		}
	}
	return s, true
}

// MaskDestination hides most of an email/phone for display ("+2547•••••678", "j•••@example.com").
func MaskDestination(d string) string {
	if at := strings.IndexByte(d, '@'); at > 0 {
		return d[:1] + "•••" + d[at:]
	}
	if len(d) > 8 {
		return d[:5] + strings.Repeat("•", len(d)-8) + d[len(d)-3:]
	}
	return "•••"
}
