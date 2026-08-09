package config

import (
	"strings"
	"testing"
)

func setProductionEnv(t *testing.T) {
	t.Helper()
	t.Setenv("APP_ENV", "production")
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/toko")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("JWT_SECRET", strings.Repeat("s", 32))
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://store.example.com")
	t.Setenv("PUBLIC_BASE_URL", "https://store.example.com")
	t.Setenv("REFRESH_COOKIE_SECURE", "true")
	t.Setenv("PAYMENT_SANDBOX", "false")
	t.Setenv("MIDTRANS_SERVER_KEY", "server-key")
	t.Setenv("RAJAONGKIR_API_KEY", "shipping-key")
	t.Setenv("SMTP_HOST", "smtp.example.com")
	t.Setenv("NOTIFY_EMAIL_ENABLED", "true")
	t.Setenv("SMTP_ALLOW_INSECURE_TLS", "false")
	t.Setenv("OBS_ENABLE_PPROF", "false")
	t.Setenv("PAYMENT_PROVIDER", "midtrans")
	t.Setenv("SHIPPING_PROVIDER", "rajaongkir")
}

func TestLoadProductionAcceptsExplicitSafeConfiguration(t *testing.T) {
	setProductionEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.AppEnv != "production" {
		t.Fatalf("AppEnv = %q, want production", cfg.AppEnv)
	}
}

func TestLoadProductionRejectsInsecureCookie(t *testing.T) {
	setProductionEnv(t)
	t.Setenv("REFRESH_COOKIE_SECURE", "false")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "REFRESH_COOKIE_SECURE") {
		t.Fatalf("Load() error = %v, want REFRESH_COOKIE_SECURE validation", err)
	}
}

func TestLoadProductionRejectsSandboxPayment(t *testing.T) {
	setProductionEnv(t)
	t.Setenv("PAYMENT_SANDBOX", "true")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "PAYMENT_SANDBOX") {
		t.Fatalf("Load() error = %v, want PAYMENT_SANDBOX validation", err)
	}
}
