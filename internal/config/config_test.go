package config

import (
	"strings"
	"testing"
	"time"
)

func valid() *Config {
	return &Config{
		Env: EnvLocal, SiteURL: "http://localhost:8080", DatabaseDSN: "u:p@tcp(localhost)/db",
		OTPHMACKey: []byte(strings.Repeat("k", 32)), SessionIdleTTL: time.Hour, SessionAbsoluteTTL: 2 * time.Hour,
		EmailAdapter: "fake", SMSAdapter: "fake", WorkerConcurrency: 1,
	}
}

func TestValidLocal(t *testing.T) {
	if err := valid().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestFakeAdaptersRefusedInProduction(t *testing.T) {
	c := valid()
	c.Env = EnvProduction
	c.SiteURL = "https://example.co.ke"
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "EMAIL_ADAPTER=fake") || !strings.Contains(err.Error(), "SMS_ADAPTER=fake") {
		t.Fatalf("expected fake adapters to be refused, got %v", err)
	}
}

func TestProductionRequiresHTTPSAndSecrets(t *testing.T) {
	c := valid()
	c.Env = EnvStaging
	c.OTPHMACKey = []byte("short")
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "https") || !strings.Contains(err.Error(), "OTP_HMAC_KEY") {
		t.Fatalf("got %v", err)
	}
}
