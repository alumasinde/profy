// Package ids generates time-ordered UUIDv7 public identifiers stored as BINARY(16).
package ids

import (
	"crypto/rand"
	"database/sql/driver"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// UUID is a 16-byte identifier.
type UUID [16]byte

// Nil is the zero UUID.
var Nil UUID

// New returns a new UUIDv7 (RFC 9562).
func New() UUID {
	var u UUID
	ms := uint64(time.Now().UnixMilli())
	binary.BigEndian.PutUint64(u[0:8], ms<<16)
	if _, err := rand.Read(u[6:]); err != nil {
		panic(fmt.Errorf("ids: crypto/rand failed: %w", err))
	}
	u[6] = (u[6] & 0x0f) | 0x70 // version 7
	u[8] = (u[8] & 0x3f) | 0x80 // variant RFC 4122
	return u
}

// String returns the canonical 8-4-4-4-12 form.
func (u UUID) String() string {
	var b [36]byte
	hex.Encode(b[0:8], u[0:4])
	b[8] = '-'
	hex.Encode(b[9:13], u[4:6])
	b[13] = '-'
	hex.Encode(b[14:18], u[6:8])
	b[18] = '-'
	hex.Encode(b[19:23], u[8:10])
	b[23] = '-'
	hex.Encode(b[24:], u[10:])
	return string(b[:])
}

// ErrInvalid is returned for malformed identifiers.
var ErrInvalid = errors.New("invalid identifier")

// Parse parses the canonical textual form.
func Parse(s string) (UUID, error) {
	var u UUID
	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return u, ErrInvalid
	}
	raw := strings.ReplaceAll(s, "-", "")
	if _, err := hex.Decode(u[:], []byte(raw)); err != nil {
		return u, ErrInvalid
	}
	return u, nil
}

// MarshalText implements encoding.TextMarshaler (JSON as a string).
func (u UUID) MarshalText() ([]byte, error) { return []byte(u.String()), nil }

// UnmarshalText implements encoding.TextUnmarshaler.
func (u *UUID) UnmarshalText(b []byte) error {
	p, err := Parse(string(b))
	if err != nil {
		return err
	}
	*u = p
	return nil
}

// Value implements driver.Valuer (BINARY(16)).
func (u UUID) Value() (driver.Value, error) { return u[:], nil }

// Scan implements sql.Scanner.
func (u *UUID) Scan(src any) error {
	b, ok := src.([]byte)
	if !ok || len(b) != 16 {
		return fmt.Errorf("ids: cannot scan %T into UUID", src)
	}
	copy(u[:], b)
	return nil
}
