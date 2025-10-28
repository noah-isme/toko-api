package app

import (
	"context"

	"github.com/alexedwards/argon2id"
	validator "github.com/go-playground/validator/v10"
	migrate "github.com/golang-migrate/migrate/v4"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/prometheus/client_golang/prometheus"
	redis "github.com/redis/go-redis/v9"
	limiter "github.com/ulule/limiter/v3"
	limiterredis "github.com/ulule/limiter/v3/drivers/store/redis"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Dependencies enumerates core services shared across modules to make future wiring explicit.
type Dependencies struct {
	Context         context.Context
	DB              *pgxpool.Pool
	Redis           *redis.Client
	Validator       *validator.Validate
	Limiter         *limiter.Limiter
	LimiterStore    limiter.Store
	TaskClient      *asynq.Client
	TaskServer      *asynq.Server
	MetricsRegistry *prometheus.Registry
	TracerProvider  trace.TracerProvider
	MeterProvider   metric.MeterProvider
	JWTBuilder      *jwt.Builder
	JWTAlgorithm    jwa.SignatureAlgorithm
}

// HashPassword demonstrates the availability of argon2id hashing for future handlers.
func HashPassword(password string) (string, error) {
	return argon2id.CreateHash(password, argon2id.DefaultParams)
}

// NewLimiterStore wires a rate limiter store backed by Redis.
func NewLimiterStore(rdb *redis.Client) (limiter.Store, error) {
	return limiterredis.NewStoreWithOptions(rdb, limiter.StoreOptions{})
}

// RunMigrations exposes migrate for startup routines.
func RunMigrations(m *migrate.Migrate) error {
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return err
	}
	return nil
}

// NewJWTBuilder provides a ready-to-use JWT builder for downstream services.
func NewJWTBuilder() *jwt.Builder {
	return jwt.NewBuilder()
}

// Tracer returns the default OpenTelemetry tracer for instrumentation hooks.
func Tracer(name string) trace.Tracer {
	return otel.Tracer(name)
}

// Meter returns the default OpenTelemetry meter for instrumentation hooks.
func Meter(name string) metric.Meter {
	return otel.Meter(name)
}
