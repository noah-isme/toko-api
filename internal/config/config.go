package config

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/v2"
)

// Config holds application configuration loaded from the environment.
type Config struct {
	AppEnv             string
	Port               string
	DatabaseURL        string
	RedisURL           string
	JWTSecret          string
	CORSAllowedOrigins []string
	MidtransServerKey  string
	MidtransClientKey  string
	RajaOngkirAPIKey   string
	AccessTokenTTL     time.Duration
	RefreshTokenTTL    time.Duration
	CookieDomain       string
	CookieSecure       bool
	CookieSameSite     http.SameSite
}

// Load reads configuration from environment variables and optional .env files.
func Load() (*Config, error) {
	_ = godotenv.Load()

	k := koanf.New(".")
	if err := k.Load(env.Provider("", ".", func(s string) string { return s }), nil); err != nil {
		return nil, fmt.Errorf("load env: %w", err)
	}

	cfg := &Config{
		AppEnv:             valueOrDefault(k.String("APP_ENV"), "development"),
		Port:               valueOrDefault(k.String("PORT"), "8080"),
		DatabaseURL:        k.String("DATABASE_URL"),
		RedisURL:           k.String("REDIS_URL"),
		JWTSecret:          k.String("JWT_SECRET"),
		CORSAllowedOrigins: splitAndTrim(k.String("CORS_ALLOWED_ORIGINS")),
		MidtransServerKey:  k.String("MIDTRANS_SERVER_KEY"),
		MidtransClientKey:  k.String("MIDTRANS_CLIENT_KEY"),
		RajaOngkirAPIKey:   k.String("RAJAONGKIR_API_KEY"),
		AccessTokenTTL:     parseDuration(k.String("ACCESS_TOKEN_TTL"), "15m"),
		RefreshTokenTTL:    parseDuration(k.String("REFRESH_TOKEN_TTL"), "720h"),
		CookieDomain:       strings.TrimSpace(k.String("COOKIE_DOMAIN")),
		CookieSecure:       parseBool(k.String("COOKIE_SECURE")),
		CookieSameSite:     parseSameSite(k.String("COOKIE_SAMESITE")),
	}

	if cfg.CookieSameSite == http.SameSiteDefaultMode {
		cfg.CookieSameSite = http.SameSiteLaxMode
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

func parseBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
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
