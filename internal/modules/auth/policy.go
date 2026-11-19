// Package auth implements passwordless-first authentication (plan §11): OTP login and
// registration, optional device-bound PIN login, recovery and opaque web sessions.
package auth

import (
	"time"

	"github.com/alumasinde/profy/internal/security"
)

// Purposes bind a challenge to one use (§11.4).
const (
	PurposeRegistration    = "registration"
	PurposeLogin           = "login"
	PurposeRecovery        = "recovery"
	PurposeSensitiveChange = "sensitive_change"
)

// Channels.
const (
	ChannelSMS   = "sms"
	ChannelEmail = "email"
)

// Session authentication methods.
const (
	MethodOTP      = "otp"
	MethodPIN      = "pin"
	MethodRecovery = "recovery"
)

// Security floors are code-defined (§8.2). Administrators may later tighten, never loosen.
const (
	OTPLength        = 6
	OTPTTL           = 5 * time.Minute
	OTPMaxAttempts   = 5
	OTPBaseCooldown  = 60 * time.Second
	OTPMaxCooldown   = 15 * time.Minute
	OTPDailyCap      = 10 // per destination per 24h
	ReauthWindow     = 10 * time.Minute
	PINDelayAfter    = 3 // failures before progressive delay
	PINLockAfter     = 6 // failures before OTP is required
	PINBaseDelay     = 30 * time.Second
	DeviceCookieLife = 365 * 24 * time.Hour
)

// Per-IP limits (in-process, single instance).
var (
	LimitOTPSendIP   = security.Limit{Max: 20, Window: time.Hour}
	LimitOTPVerifyIP = security.Limit{Max: 30, Window: 10 * time.Minute}
	LimitPINLoginIP  = security.Limit{Max: 20, Window: 15 * time.Minute}
)

// cooldownFor returns the resend gap after n sends in the last hour: 60s, 120s, 240s … capped.
func cooldownFor(n int) time.Duration {
	if n <= 0 {
		return 0
	}
	d := OTPBaseCooldown
	for i := 1; i < n && d < OTPMaxCooldown; i++ {
		d *= 2
	}
	if d > OTPMaxCooldown {
		d = OTPMaxCooldown
	}
	return d
}

// pinDelayFor returns the block duration after n consecutive failures (0 if none).
func pinDelayFor(n int) time.Duration {
	if n < PINDelayAfter {
		return 0
	}
	d := PINBaseDelay
	for i := PINDelayAfter; i < n; i++ {
		d *= 2
	}
	return d
}
