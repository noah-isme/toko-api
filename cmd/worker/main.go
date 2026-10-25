package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/extra/redisotel/v9"
	redis "github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"golang.org/x/sync/errgroup"

	"github.com/noah-isme/backend-toko/internal/common"
	"github.com/noah-isme/backend-toko/internal/config"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/events"
	"github.com/noah-isme/backend-toko/internal/lock"
	"github.com/noah-isme/backend-toko/internal/notifications"
	"github.com/noah-isme/backend-toko/internal/notify"
	"github.com/noah-isme/backend-toko/internal/obs"
	"github.com/noah-isme/backend-toko/internal/push"
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
	// Contexts without a request logger (relay, queue jobs) still log.
	zerolog.DefaultContextLogger = &logger

	// Registered first so it runs last, after the pool and Redis are closed.
	exitCode := 0
	defer func() {
		if exitCode != 0 {
			os.Exit(exitCode)
		}
	}()

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
	// Email/push tasks are keyed by event id and re-dispatched by the outbox
	// relay for up to OutboxRelayMaxAge, so their dedup key must outlive a
	// queued or retrying task for that long; with IdempotencyTTL (10 min) a
	// backlogged worker would let a re-dispatch queue a second copy.
	notifyQueue := taskQueue
	notifyQueue.DedupTTL = cfg.OutboxRelayMaxAge
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

	dlqStore := queue.NewStore(pool)
	newQueueWorker := func(kind string, concurrency int, doneMarkerTTL time.Duration, handle func(context.Context, []byte) error) queue.Worker {
		return queue.Worker{
			DoneMarkerTTL:     doneMarkerTTL,
			R:                 redisClient,
			Prefix:            cfg.QueueRedisPrefix,
			Kind:              kind,
			Concurrency:       concurrency,
			VisibilityTimeout: cfg.QueueVisibilityTimeout,
			RetryBase:         cfg.QueueBackoffBase,
			RetryJitter:       cfg.QueueBackoffJitter,
			Store:             dlqStore,
			HeartbeatInterval: cfg.WorkerHeartbeatInterval,
			SoftDeadline:      cfg.WorkerJobSoftDeadline,
			Logger:            &logger,
			Handler: func(jobCtx context.Context, task queue.Task) error {
				return handle(jobCtx, task.Payload)
			},
		}
	}

	// Email: rendered from domain_events and sent here, never on the request path.
	emailToggles := notify.MergeShipmentToggles(cfg.NotifyEmailTopics,
		cfg.NotifyOnShipped, cfg.NotifyOnOutForDelivery, cfg.NotifyOnDelivered)
	emailWorker := &notify.EmailWorker{
		Events:       queries,
		Mail:         notify.NewMailer(cfg, logger),
		Enabled:      cfg.NotifyEmailEnabled,
		TopicToggles: emailToggles,
		RelayHost:    cfg.SMTPHost,
		Logger:       &logger,
	}

	// Web push: same SSRF-safe client as the API.
	var pushSender push.Sender
	if cfg.PushEnabled() {
		pushSender = push.NewWebPushSender(cfg.PushVAPIDPublicKey, cfg.PushVAPIDPrivateKey, cfg.PushVAPIDSubject,
			int(cfg.PushTTL.Seconds()), common.NewOutboundHTTPClient(cfg.OutboundTimeout, false))
	} else {
		logger.Warn().Msg("PUSH_VAPID_PUBLIC_KEY/PUSH_VAPID_PRIVATE_KEY not set: push-delivery tasks will be dropped")
	}
	pushSvc := &push.Service{
		Q:                queries,
		Sender:           pushSender,
		PublicKey:        cfg.PushVAPIDPublicKey,
		AllowedHosts:     cfg.PushEndpointAllowedHosts,
		MaxSubscriptions: int32(cfg.PushMaxSubscriptionsPerUser),
	}
	pushWorker := &push.Worker{Svc: pushSvc, Events: queries, Orders: queries, Logger: &logger}

	// The outbox relay re-dispatches events through the same handlers the API
	// registers on its bus.
	bus := &events.Bus{
		Store:     queries,
		Scheduler: dispatcher,
		Notifiers: []events.Notifier{
			notify.EmailNotifier{
				Queue:        &notify.EmailQueue{Queue: notifyQueue, MaxAttempts: cfg.QueueMaxAttempts},
				Enabled:      cfg.NotifyEmailEnabled,
				From:         cfg.NotifyEmailFrom,
				TopicToggles: emailToggles,
			},
			&notifications.Notifier{
				Svc:    &notifications.Service{Q: queries},
				Orders: queries,
				OnError: func(err error) {
					if err != nil {
						logger.Warn().Err(err).Msg("in-app notification skipped")
					}
				},
			},
			&push.Notifier{
				Enqueue: &push.Enqueuer{Queue: notifyQueue, MaxAttempts: cfg.QueueMaxAttempts},
				Enabled: cfg.PushEnabled(),
			},
		},
	}
	relay := &events.OutboxRelay{
		Store:       queries,
		Bus:         bus,
		Locker:      lock.Locker{R: redisClient, RetryBackoff: cfg.LockRetryBackoff},
		Interval:    cfg.OutboxRelayInterval,
		MinAge:      cfg.OutboxRelayMinAge,
		MaxAge:      cfg.OutboxRelayMaxAge,
		MaxAttempts: int32(cfg.OutboxRelayMaxAttempts),
		BatchSize:   100,
		Logger:      &logger,
	}

	components := map[string]func(context.Context) error{
		// Webhook deliveries are idempotent via webhook_deliveries status, so
		// their dedup key is dropped on ack. Email and push are not: keep the
		// marker for the relay window so a re-dispatch cannot send twice.
		"webhook-delivery": newQueueWorker(notify.WebhookDeliveryTask(), cfg.QueueConcurrencyWebhook, 0, deliveryWorker.Handle).Run,
		"email-delivery":   newQueueWorker(notify.EmailDeliveryTask(), cfg.QueueConcurrencyEmail, cfg.OutboxRelayMaxAge, emailWorker.Handle).Run,
		"push-delivery":    newQueueWorker(push.PushDeliveryTask(), cfg.QueueConcurrencyPush, cfg.OutboxRelayMaxAge, pushWorker.Handle).Run,
		"outbox-relay":     relay.Run,
	}

	logger.Info().Msg("worker starting")
	group, groupCtx := errgroup.WithContext(ctx)
	for name, run := range components {
		group.Go(func() error {
			logger.Info().Str("worker_component", name).Msg("component started")
			if err := run(groupCtx); err != nil && !errors.Is(err, context.Canceled) {
				return fmt.Errorf("%s: %w", name, err)
			}
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		logger.Error().Err(err).Msg("worker stopped with error")
		exitCode = 1
		return
	}
	logger.Info().Msg("worker shutdown complete")
}

func mustInitDatabase(ctx context.Context, cfg *config.Config, logger zerolog.Logger) (*pgxpool.Pool, *dbgen.Queries) {
	poolConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		logger.Fatal().Err(err).Msg("parse database config")
	}
	poolConfig.ConnConfig.Tracer = obs.PGXTracer{}
	if poolConfig.ConnConfig.RuntimeParams == nil {
		poolConfig.ConnConfig.RuntimeParams = map[string]string{}
	}
	poolConfig.ConnConfig.RuntimeParams["application_name"] = "toko-worker"
	if cfg.DBStatementCacheCapacity >= 0 {
		poolConfig.ConnConfig.StatementCacheCapacity = cfg.DBStatementCacheCapacity
	}
	if cfg.WorkerDBMaxOpenConns > 0 {
		poolConfig.MaxConns = int32(cfg.WorkerDBMaxOpenConns)
	}
	if cfg.WorkerDBMaxIdleConns > 0 {
		idle := cfg.WorkerDBMaxIdleConns
		if cfg.WorkerDBMaxOpenConns > 0 && idle > cfg.WorkerDBMaxOpenConns {
			idle = cfg.WorkerDBMaxOpenConns
		}
		poolConfig.MinConns = int32(idle)
		poolConfig.MinIdleConns = int32(idle)
	}
	if cfg.DBConnMaxLifetime > 0 {
		poolConfig.MaxConnLifetime = cfg.DBConnMaxLifetime
		idle := cfg.DBConnMaxLifetime / 2
		if idle <= 0 {
			idle = cfg.DBConnMaxLifetime
		}
		poolConfig.MaxConnIdleTime = idle
	}
	if poolConfig.HealthCheckPeriod <= 0 {
		poolConfig.HealthCheckPeriod = time.Minute
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
