// Package paging implements bounded limits and opaque keyset cursors (plan §14.2).
package paging

import (
	"encoding/base64"
	"strconv"
)

// Encode makes an opaque cursor from the last row's internal key.
func Encode(id uint64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatUint(id, 10)))
}

// Decode parses a cursor; "" means the first page. ok is false for malformed input.
func Decode(c string) (id uint64, ok bool) {
	if c == "" {
		return 0, true
	}
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return 0, false
	}
	n, err := strconv.ParseUint(string(b), 10, 64)
	return n, err == nil && n > 0
}

// Limit clamps a requested page size to 1..max (default def).
func Limit(n, def, max int) int {
	switch {
	case n <= 0:
		return def
	case n > max:
		return max
	}
	return n
}
