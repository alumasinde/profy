// Package security holds hashing, token, OTP, PIN, CSRF and rate-limit primitives.
package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"math/big"
)

// RandomToken returns a URL-safe random token with n bytes of entropy.
func RandomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Errorf("security: crypto/rand failed: %w", err))
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// HashToken returns SHA-256 of a high-entropy token (sessions, devices). Not for low-entropy secrets.
func HashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// RandomDigits returns a cryptographically random numeric code of length n.
func RandomDigits(n int) string {
	out := make([]byte, n)
	ten := big.NewInt(10)
	for i := range out {
		d, err := rand.Int(rand.Reader, ten)
		if err != nil {
			panic(fmt.Errorf("security: crypto/rand failed: %w", err))
		}
		out[i] = byte('0' + d.Int64())
	}
	return string(out)
}

// OTPHasher computes keyed hashes of OTP codes so plaintext codes are never stored (§11.4).
type OTPHasher struct{ key []byte }

// NewOTPHasher builds a hasher with a server-side secret.
func NewOTPHasher(key []byte) *OTPHasher { return &OTPHasher{key: append([]byte(nil), key...)} }

// Hash binds the code to its challenge so a hash cannot be replayed across challenges.
func (h *OTPHasher) Hash(challengeID []byte, code string) []byte {
	m := hmac.New(sha256.New, h.key)
	m.Write(challengeID)
	m.Write([]byte{0})
	m.Write([]byte(code))
	return m.Sum(nil)
}

// Equal compares two hashes in constant time.
func Equal(a, b []byte) bool { return subtle.ConstantTimeCompare(a, b) == 1 }
