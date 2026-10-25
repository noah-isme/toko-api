package config

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
)

// testVAPIDKeys returns a valid key pair encoded like `make vapid-keys` does.
func testVAPIDKeys(t *testing.T) (publicKey, privateKey string) {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()), base64.RawURLEncoding.EncodeToString(key.Bytes())
}

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

func setDevEnv(t *testing.T) {
	t.Helper()
	t.Setenv("APP_ENV", "development")
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/toko")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("JWT_SECRET", strings.Repeat("s", 32))
	t.Setenv("PUSH_VAPID_PUBLIC_KEY", "")
	t.Setenv("PUSH_VAPID_PRIVATE_KEY", "")
	t.Setenv("PUSH_VAPID_SUBJECT", "")
	t.Setenv("PUSH_ENDPOINT_ALLOWED_HOSTS", "")
}

func TestLoadPushDisabledByDefault(t *testing.T) {
	setDevEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.PushEnabled() {
		t.Fatal("push must be disabled without VAPID keys")
	}
	if cfg.PushTTL.Seconds() != 86400 || cfg.QueueConcurrencyPush != 4 || cfg.PushMaxSubscriptionsPerUser != 10 {
		t.Fatalf("unexpected push defaults: ttl=%v conc=%d max=%d", cfg.PushTTL, cfg.QueueConcurrencyPush, cfg.PushMaxSubscriptionsPerUser)
	}
	if cfg.OutboxRelayInterval.Seconds() != 15 || cfg.OutboxRelayMinAge.Seconds() != 30 || cfg.OutboxRelayMaxAge.Hours() != 24 || cfg.OutboxRelayMaxAttempts != 10 {
		t.Fatalf("unexpected relay defaults: %v %v %v %d", cfg.OutboxRelayInterval, cfg.OutboxRelayMinAge, cfg.OutboxRelayMaxAge, cfg.OutboxRelayMaxAttempts)
	}
}

func TestLoadPushDisabledInProductionIsNotFatal(t *testing.T) {
	setProductionEnv(t)
	t.Setenv("PUSH_VAPID_PUBLIC_KEY", "")
	t.Setenv("PUSH_VAPID_PRIVATE_KEY", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.PushEnabled() {
		t.Fatal("push must be disabled")
	}
}

func TestLoadPushRequiresBothVAPIDKeys(t *testing.T) {
	setDevEnv(t)
	t.Setenv("PUSH_VAPID_PUBLIC_KEY", "pub")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "PUSH_VAPID_PRIVATE_KEY") {
		t.Fatalf("expected an error for a lone public key, got %v", err)
	}
	t.Setenv("PUSH_VAPID_PUBLIC_KEY", "")
	t.Setenv("PUSH_VAPID_PRIVATE_KEY", "priv")
	if _, err := Load(); err == nil {
		t.Fatal("expected an error for a lone private key")
	}
}

func TestLoadPushEnabledWithBothKeys(t *testing.T) {
	setDevEnv(t)
	pub, priv := testVAPIDKeys(t)
	t.Setenv("PUSH_VAPID_PUBLIC_KEY", pub)
	t.Setenv("PUSH_VAPID_PRIVATE_KEY", priv)
	t.Setenv("NOTIFY_FROM_EMAIL", "no-reply@shop.example")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.PushEnabled() {
		t.Fatal("push must be enabled")
	}
	if cfg.PushVAPIDSubject != "mailto:no-reply@shop.example" {
		t.Fatalf("subject = %q", cfg.PushVAPIDSubject)
	}

	t.Setenv("PUSH_VAPID_SUBJECT", "not an email")
	if _, err := Load(); err == nil {
		t.Fatal("an invalid subject must be rejected when push is enabled")
	}
}

func TestNormalizeVAPIDSubject(t *testing.T) {
	valid := map[string]string{
		"a@b.c":                       "mailto:a@b.c",
		"mailto:a@b.c":                "mailto:a@b.c",
		"MAILTO:a@b.c":                "mailto:a@b.c",
		"  a@b.c  ":                   "mailto:a@b.c",
		"https://example.com/contact": "https://example.com/contact",
	}
	for in, want := range valid {
		got, err := normalizeVAPIDSubject(in)
		if err != nil || got != want {
			t.Errorf("normalizeVAPIDSubject(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"mailto:mailto:a@b.c", "ftp://x", "not-an-email", "", "a@", "@b", "http://example.com", "a b@c.d"} {
		if got, err := normalizeVAPIDSubject(in); err == nil {
			t.Errorf("normalizeVAPIDSubject(%q) = %q, want error", in, got)
		}
	}
}

func TestLoadPushAllowedHostsDefaultAndOverride(t *testing.T) {
	setDevEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if strings.Join(cfg.PushEndpointAllowedHosts, ",") != strings.Join(DefaultPushEndpointAllowedHosts, ",") {
		t.Fatalf("default hosts = %v", cfg.PushEndpointAllowedHosts)
	}
	t.Setenv("PUSH_ENDPOINT_ALLOWED_HOSTS", " Push.Example.com , .relay.example ")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if strings.Join(cfg.PushEndpointAllowedHosts, ",") != "push.example.com,.relay.example" {
		t.Fatalf("override hosts = %v", cfg.PushEndpointAllowedHosts)
	}
}

func TestLoadPushRejectsUnusableVAPIDKeys(t *testing.T) {
	pub, priv := testVAPIDKeys(t)
	otherPub, _ := testVAPIDKeys(t)
	rawPriv, _ := base64.RawURLEncoding.DecodeString(priv)
	rawPub, _ := base64.RawURLEncoding.DecodeString(pub)
	stdPriv := base64.StdEncoding.EncodeToString(rawPriv)
	if !strings.ContainsAny(stdPriv, "+/") {
		stdPriv = "abc+/def+/ghi=" // force a standard-alphabet value
	}
	cases := map[string]struct {
		pub, priv, want string
	}{
		"standard base64 private key": {pub, stdPriv, "PUSH_VAPID_PRIVATE_KEY is not base64url"},
		"quoted private key":          {pub, `"` + priv + `"`, "PUSH_VAPID_PRIVATE_KEY is not base64url"},
		"short private key":           {pub, base64.RawURLEncoding.EncodeToString(rawPriv[:16]), "32 bytes"},
		"zero private key":            {pub, base64.RawURLEncoding.EncodeToString(make([]byte, 32)), "not a valid P-256 private key"},
		"quoted public key":           {`"` + pub + `"`, priv, "PUSH_VAPID_PUBLIC_KEY is not base64url"},
		"compressed public key":       {base64.RawURLEncoding.EncodeToString(rawPub[:33]), priv, "65-byte"},
		"off-curve public key":        {base64.RawURLEncoding.EncodeToString(append([]byte{4}, make([]byte, 64)...)), priv, "not a valid P-256 point"},
		"mismatched pair":             {otherPub, priv, "does not match"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			setDevEnv(t)
			t.Setenv("PUSH_VAPID_PUBLIC_KEY", tc.pub)
			t.Setenv("PUSH_VAPID_PRIVATE_KEY", tc.priv)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Load() error = %v, want it to mention %q", err, tc.want)
			}
			if strings.Contains(err.Error(), priv) {
				t.Fatal("the error must not echo the private key")
			}
		})
	}
}

func TestLoadPushAcceptsPaddedVAPIDKeys(t *testing.T) {
	setDevEnv(t)
	pub, priv := testVAPIDKeys(t)
	rawPub, _ := base64.RawURLEncoding.DecodeString(pub)
	t.Setenv("PUSH_VAPID_PUBLIC_KEY", base64.URLEncoding.EncodeToString(rawPub))
	t.Setenv("PUSH_VAPID_PRIVATE_KEY", priv)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.PushEnabled() {
		t.Fatal("push must be enabled")
	}
}
