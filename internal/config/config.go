package config

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/v2"

	"github.com/noah-isme/backend-toko/internal/events"
)

// Config holds application configuration loaded from the environment.
type Config struct {
	AppEnv                    string
	Port                      string
	DatabaseURL               string
	RedisURL                  string
	RedisCachePrefix          string
	DBMaxOpenConns            int
	DBMaxIdleConns            int
	DBConnMaxLifetime         time.Duration
	DBStatementCacheCapacity  int
	HTTPMaxInFlight           int
	JWTSecret                 string
	JWTIssuer                 string
	JWTAudience               string
	JWTClockSkew              time.Duration
	CORSAllowedOrigins        []string
	MidtransServerKey         string
	MidtransClientKey         string
	MidtransBaseURL           string
	XenditSecretKey           string
	XenditBaseURL             string
	PaymentProvider           string
	PaymentSandbox            bool
	PaymentIntentTTL          time.Duration
	PaymentCallbackBaseURL    string
	RajaOngkirAPIKey          string
	ShippingOriginCode        string
	ShippingTrackReplayTTL    time.Duration
	ShippingProvider          string
	ShippingCallbackBaseURL   string
	NotifyOnShipped           bool
	NotifyOnOutForDelivery    bool
	NotifyOnDelivered         bool
	AccessTokenTTL            time.Duration
	RefreshTokenTTL           time.Duration
	PasswordResetTTL          time.Duration
	RefreshCookieName         string
	RefreshCookieDomain       string
	RefreshCookieSecure       bool
	RefreshCookieSameSite     http.SameSite
	PublicBaseURL             string
	CatalogDefaultPage        int
	CatalogDefaultLimit       int
	CatalogMaxLimit           int
	CatalogCacheTTL           time.Duration
	CartTTL                   time.Duration
	PricingTaxRateBPS         int
	CurrencyCode              string
	CurrencyMinorUnit         int
	IdempotencyTTL            time.Duration
	VoucherMaxStack           int
	VoucherDefaultPriority    int
	VoucherPerUserLimit       int
	AnalyticsCacheTTL         time.Duration
	AnalyticsDefaultRange     int
	NotifyEmailEnabled        bool
	NotifyEmailFrom           string
	NotifyEmailTopics         map[string]bool
	WebhookDeliveryEnabled    bool
	WebhookDefaultMaxAttempts int
	WebhookBackoffBaseSec     int
	WebhookRequestTimeout     time.Duration
	WebhookAllowInsecureTLS   bool
	WebhookReplayTTL          time.Duration
	EventWorkerConcurrency    int
}

// Load reads configuration from environment variables and optional .env files.
func Load() (*Config, error) {
	_ = godotenv.Load()

	k := koanf.New(".")
	if err := k.Load(env.Provider("", ".", func(s string) string { return s }), nil); err != nil {
		return nil, fmt.Errorf("load env: %w", err)
	}

	catalogTTL := parsePositiveIntAllowZero(k.String("REDIS_CACHE_TTL_CATALOG_SEC"), 0)
	if catalogTTL <= 0 {
		catalogTTL = parsePositiveInt(k.String("CATALOG_CACHE_TTL_SEC"), 120)
	}
	analyticsTTL := parsePositiveIntAllowZero(k.String("REDIS_CACHE_TTL_ANALYTICS_SEC"), 0)
	if analyticsTTL <= 0 {
		analyticsTTL = parsePositiveIntAllowZero(k.String("ANALYTICS_CACHE_TTL_SEC"), 120)
	}

	cfg := &Config{
		AppEnv:                    valueOrDefault(k.String("APP_ENV"), "development"),
		Port:                      valueOrDefault(k.String("PORT"), "8080"),
		DatabaseURL:               k.String("DATABASE_URL"),
		RedisURL:                  k.String("REDIS_URL"),
		RedisCachePrefix:          strings.TrimSpace(valueOrDefault(k.String("REDIS_CACHE_PREFIX"), "cache")),
		DBMaxOpenConns:            parsePositiveIntAllowZero(k.String("DB_MAX_OPEN_CONNS"), 40),
		DBMaxIdleConns:            parsePositiveIntAllowZero(k.String("DB_MAX_IDLE_CONNS"), 10),
		DBConnMaxLifetime:         time.Duration(parsePositiveIntAllowZero(k.String("DB_CONN_MAX_LIFETIME_MIN"), 30)) * time.Minute,
		DBStatementCacheCapacity:  parsePositiveIntAllowZero(k.String("DB_STATEMENT_CACHE_CAPACITY"), 256),
		HTTPMaxInFlight:           parsePositiveIntAllowZero(k.String("HTTP_MAX_INFLIGHT"), 400),
		JWTSecret:                 k.String("JWT_SECRET"),
		JWTIssuer:                 strings.TrimSpace(valueOrDefault(k.String("JWT_ISSUER"), "backend-toko")),
		JWTAudience:               strings.TrimSpace(valueOrDefault(k.String("JWT_AUDIENCE"), "toko-frontend")),
		JWTClockSkew:              time.Duration(parsePositiveIntAllowZero(k.String("JWT_CLOCK_SKEW_SEC"), 60)) * time.Second,
		CORSAllowedOrigins:        splitAndTrim(k.String("CORS_ALLOWED_ORIGINS")),
		MidtransServerKey:         k.String("MIDTRANS_SERVER_KEY"),
		MidtransClientKey:         k.String("MIDTRANS_CLIENT_KEY"),
		MidtransBaseURL:           strings.TrimSpace(k.String("MIDTRANS_BASE_URL")),
		XenditSecretKey:           k.String("XENDIT_SECRET_KEY"),
		XenditBaseURL:             strings.TrimSpace(k.String("XENDIT_BASE_URL")),
		PaymentProvider:           strings.ToLower(valueOrDefault(k.String("PAYMENT_PROVIDER"), "midtrans")),
		PaymentSandbox:            parseBool(k.String("PAYMENT_SANDBOX")),
		PaymentIntentTTL:          time.Duration(parsePositiveInt(k.String("PAYMENT_INTENT_EXPIRES_MIN"), 15)) * time.Minute,
		PaymentCallbackBaseURL:    strings.TrimSpace(k.String("PAYMENT_CALLBACK_BASE_URL")),
		RajaOngkirAPIKey:          k.String("RAJAONGKIR_API_KEY"),
		ShippingOriginCode:        valueOrDefault(k.String("SHIPPING_ORIGIN_CODE"), ""),
		ShippingTrackReplayTTL:    time.Duration(parsePositiveInt(k.String("SHIPPING_TRACK_REPLAY_TTL_SEC"), 600)) * time.Second,
		ShippingProvider:          strings.ToLower(valueOrDefault(k.String("SHIPPING_PROVIDER"), "rajaongkir-mock")),
		ShippingCallbackBaseURL:   strings.TrimSpace(k.String("SHIPPING_CALLBACK_BASE_URL")),
		NotifyOnShipped:           parseBoolWithDefault(k.String("NOTIFY_ON_SHIPPED"), true),
		NotifyOnOutForDelivery:    parseBoolWithDefault(k.String("NOTIFY_ON_OUT_FOR_DELIVERY"), true),
		NotifyOnDelivered:         parseBoolWithDefault(k.String("NOTIFY_ON_DELIVERED"), true),
		AccessTokenTTL:            parseDuration(k.String("ACCESS_TOKEN_TTL"), "15m"),
		RefreshTokenTTL:           parseDuration(k.String("REFRESH_TOKEN_TTL"), "720h"),
		PasswordResetTTL:          parseDuration(k.String("PASSWORD_RESET_TTL"), "1h"),
		RefreshCookieName:         valueOrDefault(k.String("REFRESH_COOKIE_NAME"), "rt"),
		RefreshCookieDomain:       strings.TrimSpace(k.String("REFRESH_COOKIE_DOMAIN")),
		RefreshCookieSecure:       parseBool(k.String("REFRESH_COOKIE_SECURE")),
		RefreshCookieSameSite:     parseSameSite(k.String("REFRESH_COOKIE_SAMESITE")),
		PublicBaseURL:             strings.TrimSpace(k.String("PUBLIC_BASE_URL")),
		CatalogDefaultPage:        parsePositiveInt(k.String("CATALOG_DEFAULT_PAGE"), 1),
		CatalogDefaultLimit:       parsePositiveInt(k.String("CATALOG_DEFAULT_LIMIT"), 20),
		CatalogMaxLimit:           parsePositiveInt(k.String("CATALOG_MAX_LIMIT"), 100),
		CatalogCacheTTL:           time.Duration(catalogTTL) * time.Second,
		CartTTL:                   time.Duration(parsePositiveInt(k.String("CART_TTL_HOURS"), 168)) * time.Hour,
		PricingTaxRateBPS:         parsePositiveInt(k.String("PRICING_TAX_RATE_BPS"), 1100),
		CurrencyCode:              valueOrDefault(k.String("CURRENCY_CODE"), "IDR"),
		CurrencyMinorUnit:         parsePositiveIntAllowZero(k.String("CURRENCY_MINOR_UNIT"), 0),
		IdempotencyTTL:            time.Duration(parsePositiveInt(k.String("IDEMPOTENCY_TTL_SEC"), 600)) * time.Second,
		VoucherMaxStack:           parsePositiveIntAllowZero(k.String("VOUCHER_MAX_STACK"), 1),
		VoucherDefaultPriority:    parsePositiveIntAllowZero(k.String("VOUCHER_DEFAULT_PRIORITY"), 100),
		VoucherPerUserLimit:       parsePositiveIntAllowZero(k.String("VOUCHER_PER_USER_LIMIT_DEFAULT"), 1),
		AnalyticsCacheTTL:         time.Duration(analyticsTTL) * time.Second,
		AnalyticsDefaultRange:     parsePositiveIntAllowZero(k.String("ANALYTICS_DEFAULT_RANGE_DAYS"), 30),
		NotifyEmailEnabled:        parseBoolWithDefault(k.String("NOTIFY_EMAIL_ENABLED"), true),
		NotifyEmailFrom:           valueOrDefault(k.String("NOTIFY_FROM_EMAIL"), "no-reply@toko.local"),
		NotifyEmailTopics:         parseTopicToggles(k, "NOTIFY_EMAIL_TOPIC_", true),
		WebhookDeliveryEnabled:    parseBoolWithDefault(k.String("WEBHOOK_DELIVERY_ENABLED"), true),
		WebhookDefaultMaxAttempts: parsePositiveIntAllowZero(k.String("WEBHOOK_DEFAULT_MAX_ATTEMPTS"), 6),
		WebhookBackoffBaseSec:     parsePositiveIntAllowZero(k.String("WEBHOOK_BACKOFF_BASE_SEC"), 5),
		WebhookRequestTimeout:     time.Duration(parsePositiveIntAllowZero(k.String("WEBHOOK_REQUEST_TIMEOUT_MS"), 5000)) * time.Millisecond,
		WebhookAllowInsecureTLS:   parseBool(k.String("WEBHOOK_ALLOW_INSECURE_TLS")),
		WebhookReplayTTL:          time.Duration(parsePositiveIntAllowZero(k.String("WEBHOOK_REPLAY_TTL_SEC"), 600)) * time.Second,
		EventWorkerConcurrency:    parsePositiveIntAllowZero(k.String("EVENT_WORKER_CONCURRENCY"), 1),
	}

	if cfg.VoucherMaxStack < 1 {
		cfg.VoucherMaxStack = 1
	}
	if cfg.VoucherDefaultPriority <= 0 {
		cfg.VoucherDefaultPriority = 100
	}
	if cfg.VoucherPerUserLimit < 0 {
		cfg.VoucherPerUserLimit = 0
	}
	if cfg.AnalyticsDefaultRange <= 0 {
		cfg.AnalyticsDefaultRange = 30
	}

	if cfg.WebhookDefaultMaxAttempts <= 0 {
		cfg.WebhookDefaultMaxAttempts = 6
	}
	if cfg.WebhookBackoffBaseSec <= 0 {
		cfg.WebhookBackoffBaseSec = 5
	}
	if cfg.EventWorkerConcurrency <= 0 {
		cfg.EventWorkerConcurrency = 1
	}
	if cfg.NotifyEmailTopics == nil {
		cfg.NotifyEmailTopics = parseTopicToggles(k, "NOTIFY_EMAIL_TOPIC_", true)
	}

	if cfg.ShippingOriginCode == "" {
		cfg.ShippingOriginCode = "KOTA_KEDIRI"
	}

	if cfg.CatalogDefaultPage < 1 {
		cfg.CatalogDefaultPage = 1
	}
	if cfg.CatalogMaxLimit < 1 {
		cfg.CatalogMaxLimit = 100
	}
	if cfg.CatalogDefaultLimit < 1 {
		cfg.CatalogDefaultLimit = 20
	}
	if cfg.CatalogDefaultLimit > cfg.CatalogMaxLimit {
		cfg.CatalogDefaultLimit = cfg.CatalogMaxLimit
	}

	if cfg.RefreshCookieSameSite == http.SameSiteDefaultMode {
		cfg.RefreshCookieSameSite = http.SameSiteLaxMode
	}
	if strings.TrimSpace(cfg.RefreshCookieName) == "" {
		cfg.RefreshCookieName = "rt"
	}
	if cfg.PublicBaseURL != "" {
		cfg.PublicBaseURL = strings.TrimRight(cfg.PublicBaseURL, "/")
	}
	if cfg.PaymentCallbackBaseURL != "" {
		cfg.PaymentCallbackBaseURL = strings.TrimRight(cfg.PaymentCallbackBaseURL, "/")
	}
	if cfg.ShippingCallbackBaseURL != "" {
		cfg.ShippingCallbackBaseURL = strings.TrimRight(cfg.ShippingCallbackBaseURL, "/")
	}
	if cfg.MidtransBaseURL == "" {
		if cfg.PaymentSandbox {
			cfg.MidtransBaseURL = "https://app.sandbox.midtrans.com"
		} else {
			cfg.MidtransBaseURL = "https://app.midtrans.com"
		}
	}
	if cfg.XenditBaseURL == "" {
		cfg.XenditBaseURL = "https://api.xendit.co"
	}

	if cfg.CurrencyCode == "" {
		cfg.CurrencyCode = "IDR"
	}

	if cfg.DBMaxOpenConns <= 0 {
		cfg.DBMaxOpenConns = 40
	}
	if cfg.DBMaxIdleConns < 0 {
		cfg.DBMaxIdleConns = 0
	}
	if cfg.DBConnMaxLifetime <= 0 {
		cfg.DBConnMaxLifetime = 30 * time.Minute
	}
	if cfg.DBStatementCacheCapacity < 0 {
		cfg.DBStatementCacheCapacity = 0
	}
	if cfg.HTTPMaxInFlight <= 0 {
		cfg.HTTPMaxInFlight = 400
	}
	if cfg.RedisCachePrefix == "" {
		cfg.RedisCachePrefix = "cache"
	}
	if cfg.CatalogCacheTTL <= 0 {
		cfg.CatalogCacheTTL = 120 * time.Second
	}
	if cfg.AnalyticsCacheTTL <= 0 {
		cfg.AnalyticsCacheTTL = 120 * time.Second
	}

	if cfg.DatabaseURL == "" {
		return nil, errors.New("DATABASE_URL is required")
	}
	if cfg.RedisURL == "" {
		return nil, errors.New("REDIS_URL is required")
	}
	if cfg.JWTSecret == "" {
		return nil, errors.New("JWT_SECRET is required")
	}

	return cfg, nil
}

// HTTPAddr returns the address the HTTP server should bind to.
func (c *Config) HTTPAddr() string {
	port := strings.TrimSpace(c.Port)
	if port == "" {
		port = "8080"
	}
	if strings.HasPrefix(port, ":") {
		return port
	}
	return ":" + port
}

func splitAndTrim(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func parseTopicToggles(k *koanf.Koanf, prefix string, fallback bool) map[string]bool {
	topics := events.DefaultTopics()
	toggles := make(map[string]bool, len(topics))
	for _, topic := range topics {
		key := prefix + strings.ToUpper(strings.ReplaceAll(topic, ".", "_"))
		toggles[topic] = parseBoolWithDefault(k.String(key), fallback)
	}
	return toggles
}

func valueOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func parseDuration(value, fallback string) time.Duration {
	base := strings.TrimSpace(value)
	if base == "" {
		base = fallback
	}
	d, err := time.ParseDuration(base)
	if err != nil {
		d, _ = time.ParseDuration(fallback)
	}
	return d
}

func parsePositiveInt(value string, fallback int) int {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(trimmed)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func parsePositiveIntAllowZero(value string, fallback int) int {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(trimmed)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}

func parseBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func parseBoolWithDefault(value string, fallback bool) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	return parseBool(trimmed)
}

func parseSameSite(value string) http.SameSite {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "strict":
		return http.SameSiteStrictMode
	case "none":
		return http.SameSiteNoneMode
	case "lax":
		return http.SameSiteLaxMode
	default:
		return http.SameSiteDefaultMode
	}
}

// MustLoad behaves like Load but panics on error. Useful for tests and command entrypoints.
func MustLoad() *Config {
	cfg, err := Load()
	if err != nil {
		panic(err)
	}
	return cfg
}

// LoadForTests allows tests to override environment variables without touching the real environment.
func LoadForTests(env map[string]string) (*Config, error) {
	original := make(map[string]string, len(env))
	for key := range env {
		original[key] = os.Getenv(key)
		if err := setEnvVar(key, env[key]); err != nil {
			return nil, err
		}
	}
	cfg, err := Load()
	restoreErr := restoreEnv(original)
	if err != nil {
		return nil, err
	}
	return cfg, restoreErr
}

func setEnvVar(key, value string) error {
	if value == "" {
		return os.Unsetenv(key)
	}
	return os.Setenv(key, value)
}

func restoreEnv(values map[string]string) error {
	var errs []string
	for key, value := range values {
		if err := setEnvVar(key, value); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", key, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("restore env: %s", strings.Join(errs, "; "))
	}
	return nil
}
