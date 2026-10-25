package jobs

import (
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	redis "github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/noah-isme/backend-toko/internal/common"
	"github.com/noah-isme/backend-toko/internal/config"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/events"
	"github.com/noah-isme/backend-toko/internal/lock"
	"github.com/noah-isme/backend-toko/internal/notifications"
	"github.com/noah-isme/backend-toko/internal/notify"
	"github.com/noah-isme/backend-toko/internal/resilience"
	"github.com/noah-isme/backend-toko/internal/shipping"
)

// Deps are the worker's shared dependencies.
type Deps struct {
	Cfg     *config.Config
	Pool    *pgxpool.Pool
	Queries *dbgen.Queries
	Redis   *redis.Client
	Logger  *zerolog.Logger
	Events  *events.Bus
	Mailer  common.EmailSender
}

// trackingJitter is the error-backoff jitter for carrier calls.
const trackingJitter = 0.2

// New builds the periodic job runner with the checkout_expiry and
// shipment_tracking jobs.
func New(d Deps) (*Runner, error) {
	if d.Cfg == nil || d.Pool == nil || d.Queries == nil || d.Redis == nil {
		return nil, fmt.Errorf("jobs: config, pool, queries and redis are required")
	}
	cfg := d.Cfg
	logger := d.Logger
	if logger == nil {
		nop := zerolog.Nop()
		logger = &nop
	}
	provider, err := trackingProvider(cfg)
	if err != nil {
		return nil, err
	}
	breaker := resilience.NewBreaker(cfg.CircuitShippingMinReq, cfg.CircuitShippingFailureRate, cfg.CircuitShippingOpenFor).
		WithTarget("shipping-track").
		WithLogger(*logger)
	shipSvc := &shipping.Service{
		Q:                      d.Queries,
		Provider:               provider,
		Mail:                   d.Mailer,
		NotifyOnShipped:        cfg.NotifyOnShipped,
		NotifyOnOutForDelivery: cfg.NotifyOnOutForDelivery,
		NotifyOnDelivered:      cfg.NotifyOnDelivered,
		Events:                 d.Events,
	}
	expiry := &CheckoutExpiryJob{
		Pool:        d.Pool,
		Q:           d.Queries,
		Events:      d.Events,
		FallbackTTL: cfg.PaymentIntentTTL,
		Grace:       cfg.JobCheckoutExpiryGrace,
		SilentAfter: cfg.JobCheckoutExpirySilentAfter,
		ProofHold:   cfg.JobCheckoutProofHold,
		BatchSize:   cfg.JobBatchSize,
		Logger:      logger,
	}
	tracking := &ShipmentTrackingJob{
		Q:               d.Queries,
		Svc:             shipSvc,
		Provider:        BreakerProvider{Inner: provider, Breaker: breaker},
		BatchSize:       cfg.JobBatchSize,
		MaxCallsPerTick: cfg.ShipmentTrackMaxCallsPerTick,
		MaxAttempts:     cfg.ShipmentTrackMaxAttempts,
		MaxAgeDays:      cfg.ShipmentTrackMaxAgeDays,
		BackoffBase:     cfg.ShipmentTrackBackoffBase,
		BackoffMax:      cfg.ShipmentTrackBackoffMax,
		SilentAfter:     cfg.ShipmentTrackSilentAfter,
		Jitter:          trackingJitter,
		Logger:          logger,
	}
	return &Runner{
		Locker:    lock.Locker{R: d.Redis, RetryBackoff: cfg.LockRetryBackoff},
		LockTTL:   cfg.JobLockTTL,
		Heartbeat: cfg.WorkerHeartbeatInterval,
		Logger:    logger,
		Jobs: []Spec{
			{Job: expiry, Interval: cfg.JobCheckoutExpiryInterval},
			{Job: tracking, Interval: cfg.JobShipmentTrackingInterval},
		},
	}, nil
}

// trackingProvider mirrors the shipping provider selection in cmd/api.
func trackingProvider(cfg *config.Config) (shipping.Provider, error) {
	switch cfg.ShippingProvider {
	case "rajaongkir-mock", "mock":
		return shipping.RajaOngkirMock{}, nil
	case "rajaongkir", "":
		return shipping.RajaOngkirTracker{APIKey: cfg.RajaOngkirAPIKey, BaseURL: cfg.RajaOngkirBaseURL}, nil
	default:
		return nil, fmt.Errorf("jobs: unsupported shipping provider %q", cfg.ShippingProvider)
	}
}

// MailerFromConfig builds the transactional mail sender the way cmd/api does
// (without its password-reset warnings). With no SMTP host, mail is a no-op.
func MailerFromConfig(cfg *config.Config, logger zerolog.Logger) (common.EmailSender, error) {
	if strings.TrimSpace(cfg.SMTPHost) == "" {
		logger.Warn().Msg("SMTP_HOST not set: worker job emails are disabled")
		return common.NopEmailSender{}, nil
	}
	sender, err := common.NewSMTPSender(common.SMTPSender{
		Host:             cfg.SMTPHost,
		Port:             cfg.SMTPPort,
		Username:         cfg.SMTPUsername,
		Password:         cfg.SMTPPassword,
		From:             cfg.SMTPFrom,
		ImplicitTLS:      cfg.SMTPImplicitTLS,
		AllowInsecureTLS: cfg.SMTPAllowInsecureTLS,
		Timeout:          cfg.SMTPTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("configure smtp sender: %w", err)
	}
	return sender, nil
}

// NewBus builds the domain event bus with the same notifiers as the API
// (customer email honouring NOTIFY_EMAIL_* toggles, and in-app notifications).
func NewBus(cfg *config.Config, queries *dbgen.Queries, scheduler events.DeliveryScheduler, mailer common.EmailSender, logger *zerolog.Logger) *events.Bus {
	emailNotifier := notify.EmailNotifier{
		Mail:         mailer,
		Enabled:      cfg.NotifyEmailEnabled,
		From:         cfg.NotifyEmailFrom,
		TopicToggles: cfg.NotifyEmailTopics,
	}
	inApp := &notifications.Notifier{
		Svc:    &notifications.Service{Q: queries},
		Orders: queries,
		OnError: func(err error) {
			if err != nil && logger != nil {
				logger.Error().Err(err).Msg("record in-app notification")
			}
		},
	}
	return &events.Bus{
		Store:     queries,
		Scheduler: scheduler,
		Notifiers: []events.Notifier{emailNotifier, inApp},
	}
}
