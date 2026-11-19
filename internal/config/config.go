// Package config loads environment-based configuration. Secrets live only in
// the environment (plan §4.2); operational settings live in the database.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Environment names.
const (
	EnvLocal      = "local"
	EnvStaging    = "staging"
	EnvProduction = "production"
)

// Config is the process configuration.
type Config struct {
	Env      string
	HTTPAddr string

	// SiteURL is the bootstrap canonical origin, used until the site_url setting is configured.
	SiteURL string

	DatabaseDSN     string
	DBMaxOpenConns  int
	DBMaxIdleConns  int
	DBConnMaxLife   time.Duration
	DBQueryTimeout  time.Duration
	TrustProxy      bool // honour X-Forwarded-For from the reverse proxy
	ShutdownTimeout time.Duration

	// Secrets.
	OTPHMACKey []byte

	SessionIdleTTL     time.Duration
	SessionAbsoluteTTL time.Duration

	EmailAdapter string // "fake" | "smtp"
	SMSAdapter   string // "fake" | "africastalking"

	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string

	ATUsername string
	ATAPIKey   string
	ATSenderID string
	ATBaseURL  string

	WorkerConcurrency int
	WorkerPollEvery   time.Duration

	// MediaDir holds uploads: <dir>/public (served images) and <dir>/private (documents).
	MediaDir string
}

// IsProduction reports whether this is the production environment.
func (c *Config) IsProduction() bool { return c.Env == EnvProduction }

// IsLocal reports whether this is local development.
func (c *Config) IsLocal() bool { return c.Env == EnvLocal }

// SecureCookies reports whether cookies must carry the Secure attribute.
func (c *Config) SecureCookies() bool { return !c.IsLocal() }

// Load reads configuration from the environment and validates it.
func Load() (*Config, error) {
	c := &Config{
		Env:                get("APP_ENV", EnvLocal),
		HTTPAddr:           get("HTTP_ADDR", ":8080"),
		SiteURL:            strings.TrimRight(get("SITE_URL", "http://localhost:8080"), "/"),
		DatabaseDSN:        os.Getenv("DATABASE_DSN"),
		DBMaxOpenConns:     getInt("DB_MAX_OPEN_CONNS", 20),
		DBMaxIdleConns:     getInt("DB_MAX_IDLE_CONNS", 10),
		DBConnMaxLife:      getDuration("DB_CONN_MAX_LIFETIME", 30*time.Minute),
		DBQueryTimeout:     getDuration("DB_QUERY_TIMEOUT", 5*time.Second),
		TrustProxy:         getBool("TRUST_PROXY", false),
		ShutdownTimeout:    getDuration("SHUTDOWN_TIMEOUT", 15*time.Second),
		OTPHMACKey:         []byte(os.Getenv("OTP_HMAC_KEY")),
		SessionIdleTTL:     getDuration("SESSION_IDLE_TTL", 7*24*time.Hour),
		SessionAbsoluteTTL: getDuration("SESSION_ABSOLUTE_TTL", 30*24*time.Hour),
		EmailAdapter:       get("EMAIL_ADAPTER", "fake"),
		SMSAdapter:         get("SMS_ADAPTER", "fake"),
		SMTPHost:           os.Getenv("SMTP_HOST"),
		SMTPPort:           getInt("SMTP_PORT", 587),
		SMTPUsername:       os.Getenv("SMTP_USERNAME"),
		SMTPPassword:       os.Getenv("SMTP_PASSWORD"),
		SMTPFrom:           os.Getenv("SMTP_FROM"),
		ATUsername:         os.Getenv("AT_USERNAME"),
		ATAPIKey:           os.Getenv("AT_API_KEY"),
		ATSenderID:         os.Getenv("AT_SENDER_ID"),
		ATBaseURL:          get("AT_BASE_URL", "https://api.africastalking.com"),
		WorkerConcurrency:  getInt("WORKER_CONCURRENCY", 2),
		WorkerPollEvery:    getDuration("WORKER_POLL_INTERVAL", 2*time.Second),
		MediaDir:           get("MEDIA_DIR", "./data/media"),
	}
	return c, c.Validate()
}

// Validate enforces safe configuration. Fake delivery adapters can never run in production (§19.3).
func (c *Config) Validate() error {
	var errs []error
	switch c.Env {
	case EnvLocal, EnvStaging, EnvProduction:
	default:
		errs = append(errs, fmt.Errorf("APP_ENV must be local, staging or production, got %q", c.Env))
	}
	if c.DatabaseDSN == "" {
		errs = append(errs, errors.New("DATABASE_DSN is required"))
	}
	if u, err := url.Parse(c.SiteURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		errs = append(errs, fmt.Errorf("SITE_URL must be an absolute http(s) URL, got %q", c.SiteURL))
	} else if !c.IsLocal() && u.Scheme != "https" {
		errs = append(errs, errors.New("SITE_URL must use https outside local"))
	}
	if len(c.OTPHMACKey) < 32 {
		errs = append(errs, errors.New("OTP_HMAC_KEY must be at least 32 bytes"))
	}
	if c.SessionIdleTTL <= 0 || c.SessionAbsoluteTTL < c.SessionIdleTTL {
		errs = append(errs, errors.New("session TTLs invalid: need 0 < SESSION_IDLE_TTL <= SESSION_ABSOLUTE_TTL"))
	}
	switch c.EmailAdapter {
	case "fake":
		if c.IsProduction() {
			errs = append(errs, errors.New("EMAIL_ADAPTER=fake is not allowed in production"))
		}
	case "smtp":
		if c.SMTPHost == "" || c.SMTPFrom == "" {
			errs = append(errs, errors.New("SMTP_HOST and SMTP_FROM are required for EMAIL_ADAPTER=smtp"))
		}
	default:
		errs = append(errs, fmt.Errorf("unknown EMAIL_ADAPTER %q", c.EmailAdapter))
	}
	switch c.SMSAdapter {
	case "fake":
		if c.IsProduction() {
			errs = append(errs, errors.New("SMS_ADAPTER=fake is not allowed in production"))
		}
	case "africastalking":
		if c.ATUsername == "" || c.ATAPIKey == "" {
			errs = append(errs, errors.New("AT_USERNAME and AT_API_KEY are required for SMS_ADAPTER=africastalking"))
		}
	default:
		errs = append(errs, fmt.Errorf("unknown SMS_ADAPTER %q", c.SMSAdapter))
	}
	if c.WorkerConcurrency < 1 {
		errs = append(errs, errors.New("WORKER_CONCURRENCY must be >= 1"))
	}
	return errors.Join(errs...)
}

func get(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func getInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func getBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

func getDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
