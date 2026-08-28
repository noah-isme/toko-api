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

func TestLoadDevelopmentFallsBackToMockShippingWithoutCredentials(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/toko")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("JWT_SECRET", "development-secret")
	t.Setenv("SHIPPING_PROVIDER", "rajaongkir")
	t.Setenv("RAJAONGKIR_API_KEY", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ShippingProvider != "rajaongkir-mock" {
		t.Fatalf("ShippingProvider = %q, want rajaongkir-mock", cfg.ShippingProvider)
	}
}

func TestLoadDevelopmentKeepsRajaOngkirWithCredentials(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/toko")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("JWT_SECRET", "development-secret")
	t.Setenv("SHIPPING_PROVIDER", "rajaongkir")
	t.Setenv("RAJAONGKIR_API_KEY", "shipping-key")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ShippingProvider != "rajaongkir" {
		t.Fatalf("ShippingProvider = %q, want rajaongkir", cfg.ShippingProvider)
	}
}

func TestLoadProductionRejectsMockShippingProvider(t *testing.T) {
	setProductionEnv(t)
	t.Setenv("SHIPPING_PROVIDER", "rajaongkir-mock")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "unsupported production shipping provider") {
		t.Fatalf("Load() error = %v, want production mock-provider rejection", err)
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

func TestDatabasePoolDefaultsAndWorkerOverrides(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/toko")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("JWT_SECRET", "development-secret")

	// Default pool limits
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.DBMaxOpenConns != 5 {
		t.Fatalf("DBMaxOpenConns = %d, want 5", cfg.DBMaxOpenConns)
	}
	if cfg.DBMaxIdleConns != 1 {
		t.Fatalf("DBMaxIdleConns = %d, want 1", cfg.DBMaxIdleConns)
	}
	if cfg.WorkerDBMaxOpenConns != 2 {
		t.Fatalf("WorkerDBMaxOpenConns = %d, want 2", cfg.WorkerDBMaxOpenConns)
	}
	if cfg.WorkerDBMaxIdleConns != 1 {
		t.Fatalf("WorkerDBMaxIdleConns = %d, want 1", cfg.WorkerDBMaxIdleConns)
	}

	// Explicit overrides
	t.Setenv("DB_MAX_OPEN_CONNS", "10")
	t.Setenv("DB_MAX_IDLE_CONNS", "2")
	t.Setenv("WORKER_DB_MAX_OPEN_CONNS", "3")
	t.Setenv("WORKER_DB_MAX_IDLE_CONNS", "2")

	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load() with overrides error = %v", err)
	}
	if cfg.DBMaxOpenConns != 10 {
		t.Fatalf("DBMaxOpenConns = %d, want 10", cfg.DBMaxOpenConns)
	}
	if cfg.DBMaxIdleConns != 2 {
		t.Fatalf("DBMaxIdleConns = %d, want 2", cfg.DBMaxIdleConns)
	}
	if cfg.WorkerDBMaxOpenConns != 3 {
		t.Fatalf("WorkerDBMaxOpenConns = %d, want 3", cfg.WorkerDBMaxOpenConns)
	}
	if cfg.WorkerDBMaxIdleConns != 2 {
		t.Fatalf("WorkerDBMaxIdleConns = %d, want 2", cfg.WorkerDBMaxIdleConns)
	}
}

