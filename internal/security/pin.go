package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters (OWASP minimum recommendation: m=19 MiB, t=2, p=1).
const (
	argonTime    uint32 = 2
	argonMemory  uint32 = 19 * 1024
	argonThreads uint8  = 1
	argonKeyLen  uint32 = 32
	argonSaltLen        = 16
)

// HashSecret hashes a low-entropy secret (PIN) with Argon2id and a unique salt, returning a PHC string.
func HashSecret(secret string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(secret), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// ErrMalformedHash means a stored hash could not be parsed.
var ErrMalformedHash = errors.New("malformed secret hash")

// VerifySecret checks a secret against a PHC string in constant time.
func VerifySecret(secret, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, ErrMalformedHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, ErrMalformedHash
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false, ErrMalformedHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, ErrMalformedHash
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, ErrMalformedHash
	}
	got := argon2.IDKey([]byte(secret), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// commonPINs are frequently chosen six-digit PINs beyond the generic patterns below.
var commonPINs = map[string]bool{
	"123123": true, "112233": true, "121212": true, "131313": true, "159753": true, "147258": true,
	"258369": true, "102030": true, "520520": true, "789456": true, "456123": true, "696969": true,
	"123321": true, "654456": true, "111222": true, "000111": true, "101010": true, "142536": true,
	"159357": true, "753951": true, "147852": true, "741852": true, "963852": true, "198520": true,
}

// ValidatePIN reports why a PIN is unacceptable, or "" if it is allowed (§11.5).
// Rejected: non six-digit input, fewer than three distinct digits (covers repeats and
// two-digit alternations), ascending/descending runs, and a list of common PINs.
func ValidatePIN(pin string) string {
	if len(pin) != 6 {
		return "pin_length"
	}
	distinct := map[byte]bool{}
	for i := 0; i < len(pin); i++ {
		if pin[i] < '0' || pin[i] > '9' {
			return "pin_digits_only"
		}
		distinct[pin[i]] = true
	}
	if len(distinct) < 3 {
		return "pin_too_simple"
	}
	asc, desc := true, true
	for i := 1; i < len(pin); i++ {
		if (pin[i-1]-'0'+1)%10 != pin[i]-'0' {
			asc = false
		}
		if (pin[i]-'0'+1)%10 != pin[i-1]-'0' {
			desc = false
		}
	}
	if asc || desc {
		return "pin_sequence"
	}
	if commonPINs[pin] {
		return "pin_common"
	}
	// Two repeated triples (e.g. 123123, 840840) are trivially guessable.
	if pin[:3] == pin[3:] {
		return "pin_common"
	}
	return ""
}
