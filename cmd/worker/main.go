package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/extra/redisotel/v9"
	redis "github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/noah-isme/backend-toko/internal/config"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/lock"
	"github.com/noah-isme/backend-toko/internal/notify"
	"github.com/noah-isme/backend-toko/internal/obs"
	"github.com/noah-isme/backend-toko/internal/queue"
	"github.com/noah-isme/backend-toko/internal/resilience"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}

	logFormat := envOrDefault("OBS_LOG_FORMAT", "json")
	logLevel := envOrDefault("OBS_LOG_LEVEL", "info")
	logger := obs.NewLogger(logFormat, logLevel).With().Str("component", "worker").Logger()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, queries := mustInitDatabase(ctx, cfg, logger)
	defer pool.Close()

	redisClient := mustInitRedis(ctx, cfg, logger)
	defer func() {
		if err := redisClient.Close(); err != nil {
			logger.Error().Err(err).Msg("close redis")
		}
	}()

	notifyStore := notify.NewStore(queries)
	taskQueue := queue.Enqueuer{R: redisClient, Prefix: cfg.QueueRedisPrefix, DedupTTL: cfg.IdempotencyTTL, MaxAttempts: cfg.QueueMaxAttempts}
	webhookHTTPClient := notify.HttpClient(int(cfg.WebhookRequestTimeout/time.Millisecond), cfg.WebhookAllowInsecureTLS)
	dispatcher := &notify.Dispatcher{
		Store: notifyStore,
		HTTP: &resilience.HTTPClient{
			Client:      webhookHTTPClient,
			Breaker:     resilience.NewBreaker(cfg.CircuitWebhookMinReq, cfg.CircuitWebhookFailureRate, cfg.CircuitWebhookOpenFor),
			BaseBackoff: cfg.RetryBase,
			MaxAttempts: cfg.RetryMaxAttempts,
			Jitter:      cfg.RetryJitterPercent,
			Timeout:     cfg.OutboundTimeout,
			Target:      "webhook-delivery",
			Logger:      &logger,
		},
		Queue:              taskQueue,
		BackoffBaseSec:     cfg.WebhookBackoffBaseSec,
		DefaultMaxAttempts: cfg.WebhookDefaultMaxAttempts,
		Enabled:            cfg.WebhookDeliveryEnabled,
		Replay:             notify.RedisReplayProtector{Client: redisClient},
		ReplayTTL:          cfg.WebhookReplayTTL,
	}

	deliveryWorker := notify.DeliveryWorker{
		Dispatcher: dispatcher,
		Locker:     lock.Locker{R: redisClient, RetryBackoff: cfg.LockRetryBackoff},
		LockTTL:    cfg.LockTTL,
	}

	webhookQueueWorker := queue.Worker{
		R:                 redisClient,
		Prefix:            cfg.QueueRedisPrefix,
		Kind:              notify.WebhookDeliveryTask(),
		Concurrency:       cfg.QueueConcurrencyWebhook,
		VisibilityTimeout: cfg.QueueVisibilityTimeout,
		RetryBase:         cfg.QueueBackoffBase,
		RetryJitter:       cfg.QueueBackoffJitter,
		Store:             queue.NewStore(pool),
		HeartbeatInterval: cfg.WorkerHeartbeatInterval,
		SoftDeadline:      cfg.WorkerJobSoftDeadline,
		Logger:            &logger,
		Handler: func(jobCtx context.Context, task queue.Task) error {
			return deliveryWorker.Handle(jobCtx, task.Payload)
		},
	}

	logger.Info().Msg("worker starting")
	if err := webhookQueueWorker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error().Err(err).Msg("worker stopped with error")
	} else {
		logger.Info().Msg("worker shutdown complete")
	}
}

func mustInitDatabase(ctx context.Context, cfg *config.Config, logger zerolog.Logger) (*pgxpool.Pool, *dbgen.Queries) {
	poolConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		logger.Fatal().Err(err).Msg("parse database config")
	}
	poolConfig.ConnConfig.Tracer = obs.PGXTracer{}
	if cfg.DBStatementCacheCapacity >= 0 {
		poolConfig.ConnConfig.StatementCacheCapacity = cfg.DBStatementCacheCapacity
	}
	if cfg.DBMaxOpenConns > 0 {
		poolConfig.MaxConns = int32(cfg.DBMaxOpenConns)
	}
	if cfg.DBMaxIdleConns > 0 {
		poolConfig.MinConns = int32(cfg.DBMaxIdleConns)
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		logger.Fatal().Err(err).Msg("connect database")
	}
	if err := pool.Ping(ctx); err != nil {
		logger.Fatal().Err(err).Msg("ping database")
	}
	return pool, dbgen.New(pool)
}

func mustInitRedis(ctx context.Context, cfg *config.Config, logger zerolog.Logger) *redis.Client {
	redisOpts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		logger.Fatal().Err(err).Msg("parse redis url")
	}
	redisClient := redis.NewClient(redisOpts)
	if err := redisotel.InstrumentTracing(redisClient); err != nil {
		logger.Error().Err(err).Msg("instrument redis tracing")
	}
	if err := redisClient.Ping(ctx).Err(); err != nil {
		logger.Fatal().Err(err).Msg("ping redis")
	}
	return redisClient
}

func envOrDefault(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok {
		trimmed := strings.TrimSpace(val)
		if trimmed != "" {
			return trimmed
		}
	}
	return fallback
}
