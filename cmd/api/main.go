package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"net/http/pprof"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/extra/redisotel/v9"
	redis "github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/noah-isme/backend-toko/internal/admin"
	"github.com/noah-isme/backend-toko/internal/analytics"
	"github.com/noah-isme/backend-toko/internal/audit"
	"github.com/noah-isme/backend-toko/internal/auth"
	"github.com/noah-isme/backend-toko/internal/campaign"
	"github.com/noah-isme/backend-toko/internal/cart"
	"github.com/noah-isme/backend-toko/internal/catalog"
	"github.com/noah-isme/backend-toko/internal/checkout"
	"github.com/noah-isme/backend-toko/internal/common"
	"github.com/noah-isme/backend-toko/internal/config"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/events"
	"github.com/noah-isme/backend-toko/internal/favorites"
	"github.com/noah-isme/backend-toko/internal/health"
	"github.com/noah-isme/backend-toko/internal/loyalty"
	"github.com/noah-isme/backend-toko/internal/notifications"
	"github.com/noah-isme/backend-toko/internal/notify"
	"github.com/noah-isme/backend-toko/internal/obs"
	"github.com/noah-isme/backend-toko/internal/order"
	"github.com/noah-isme/backend-toko/internal/payment"
	"github.com/noah-isme/backend-toko/internal/platform"
	"github.com/noah-isme/backend-toko/internal/push"
	"github.com/noah-isme/backend-toko/internal/qa"
	"github.com/noah-isme/backend-toko/internal/queue"
	"github.com/noah-isme/backend-toko/internal/ratelimit"
	"github.com/noah-isme/backend-toko/internal/recommendations"
	"github.com/noah-isme/backend-toko/internal/resilience"
	"github.com/noah-isme/backend-toko/internal/reviews"
	"github.com/noah-isme/backend-toko/internal/security"
	"github.com/noah-isme/backend-toko/internal/shipping"
	"github.com/noah-isme/backend-toko/internal/tenant"
	"github.com/noah-isme/backend-toko/internal/user"
	"github.com/noah-isme/backend-toko/internal/voucher"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}

	logFormat := envOrDefault("OBS_LOG_FORMAT", "json")
	logLevel := envOrDefault("OBS_LOG_LEVEL", "info")
	logger := obs.NewLogger(logFormat, logLevel).With().Str("env", cfg.AppEnv).Logger()

	metricsNamespace := envOrDefault("OBS_METRICS_NAMESPACE", "toko")
	metricsEnabled := envBool("OBS_ENABLE_PROMETHEUS", true)
	obs.MustRegisterDomainMetrics(metricsNamespace, nil)

	tracingEnabled := envBool("OBS_ENABLE_TRACING", true)
	if tracingEnabled {
		sampling := envFloat("OBS_TRACING_SAMPLING_RATIO", 1.0)
		shutdown, err := obs.InitTracer(context.Background(), obs.TracingConfig{
			ServiceName:   "toko-api",
			Endpoint:      envOrDefault("OBS_OTLP_ENDPOINT", ""),
			Exporter:      envOrDefault("OBS_TRACING_EXPORTER", "otlp"),
			SamplingRatio: sampling,
			Environment:   cfg.AppEnv,
		})
		if err != nil {
			logger.Error().Err(err).Msg("initialise tracing")
			tracingEnabled = false
		} else {
			defer func() {
				ctx := context.Background()
				if err := shutdown(ctx); err != nil {
					logger.Error().Err(err).Msg("shutdown tracer")
				}
			}()
		}
	}

	mailer := buildMailer(cfg, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	poolConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		logger.Fatal().Err(err).Msg("parse database config")
	}
	poolConfig.ConnConfig.Tracer = obs.PGXTracer{}
	if poolConfig.ConnConfig.RuntimeParams == nil {
		poolConfig.ConnConfig.RuntimeParams = map[string]string{}
	}
	poolConfig.ConnConfig.RuntimeParams["application_name"] = "toko-api"
	if cfg.DBStatementCacheCapacity >= 0 {
		poolConfig.ConnConfig.StatementCacheCapacity = cfg.DBStatementCacheCapacity
	}
	if cfg.DBMaxOpenConns > 0 {
		poolConfig.MaxConns = int32(cfg.DBMaxOpenConns)
	}
	if cfg.DBMaxIdleConns > 0 {
		idle := cfg.DBMaxIdleConns
		if cfg.DBMaxOpenConns > 0 && idle > cfg.DBMaxOpenConns {
			idle = cfg.DBMaxOpenConns
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
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		logger.Fatal().Err(err).Msg("ping database")
	}

	queries := dbgen.New(pool)

	if metricsEnabled {
		prometheus.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Namespace: metricsNamespace,
			Name:      "db_pool_acquired_conns",
			Help:      "Current number of acquired PostgreSQL connections.",
		}, func() float64 {
			if pool == nil {
				return 0
			}
			return float64(pool.Stat().AcquiredConns())
		}))
		prometheus.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Namespace: metricsNamespace,
			Name:      "db_pool_idle_conns",
			Help:      "Current number of idle PostgreSQL connections.",
		}, func() float64 {
			if pool == nil {
				return 0
			}
			return float64(pool.Stat().IdleConns())
		}))
		prometheus.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Namespace: metricsNamespace,
			Name:      "db_pool_in_use_ratio",
			Help:      "Fraction of PostgreSQL pool connections currently acquired.",
		}, func() float64 {
			if pool == nil {
				return 0
			}
			stat := pool.Stat()
			max := stat.MaxConns()
			if max <= 0 {
				return 0
			}
			return float64(stat.AcquiredConns()) / float64(max)
		}))
	}

	redisOpts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		logger.Fatal().Err(err).Msg("parse redis url")
	}
	redisClient := redis.NewClient(redisOpts)
	if err := redisotel.InstrumentTracing(redisClient); err != nil {
		logger.Error().Err(err).Msg("instrument redis tracing")
	}
	if metricsEnabled {
		if err := redisotel.InstrumentMetrics(redisClient); err != nil {
			logger.Error().Err(err).Msg("instrument redis metrics")
		}
	}
	defer func() {
		if err := redisClient.Close(); err != nil {
			logger.Error().Err(err).Msg("close redis")
		}
	}()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		logger.Fatal().Err(err).Msg("ping redis")
	}
	catalogCache := catalog.NewCache(redisClient, cfg.CatalogCacheTTL, cfg.RedisCachePrefix)
	catalogService, err := catalog.NewService(catalog.ServiceConfig{
		Queries:      queries,
		Cache:        catalogCache,
		DefaultPage:  cfg.CatalogDefaultPage,
		DefaultLimit: cfg.CatalogDefaultLimit,
		MaxLimit:     cfg.CatalogMaxLimit,
	})
	if err != nil {
		logger.Fatal().Err(err).Msg("initialise catalog service")
	}
	catalogHandler := catalog.NewHandler(catalog.HandlerConfig{Service: catalogService})

	recommendationsService, err := recommendations.NewService(recommendations.ServiceConfig{
		Queries: queries,
	})
	if err != nil {
		logger.Fatal().Err(err).Msg("initialise recommendations service")
	}
	recommendationsHandler := recommendations.NewHandler(recommendations.HandlerConfig{Service: recommendationsService})

	authService, err := auth.NewService(auth.Config{
		Queries:         queries,
		Secret:          cfg.JWTSecret,
		AccessTokenTTL:  cfg.AccessTokenTTL,
		RefreshTokenTTL: cfg.RefreshTokenTTL,
		ResetTokenTTL:   cfg.PasswordResetTTL,
		Issuer:          cfg.JWTIssuer,
		Audience:        cfg.JWTAudience,
		ClockSkew:       cfg.JWTClockSkew,
	})
	if err != nil {
		logger.Fatal().Err(err).Msg("initialise auth service")
	}
	authHandler := &auth.Handler{
		Service:               authService,
		Mailer:                mailer,
		MembershipPool:        pool,
		RefreshCookieName:     cfg.RefreshCookieName,
		RefreshCookieDomain:   cfg.RefreshCookieDomain,
		RefreshCookieSecure:   cfg.RefreshCookieSecure,
		RefreshCookieSameSite: cfg.RefreshCookieSameSite,
		PublicBaseURL:         cfg.PublicBaseURL,
	}
	authMiddleware := auth.Middleware{Service: authService}

	addressService := user.NewService(pool)
	addressHandler := &user.Handler{Service: addressService}

	idem := common.Idem{R: redisClient, TTL: cfg.IdempotencyTTL}

	// The schema's source of truth for the default tenant is the row with
	// slug='default' (seeded by migration 000018 with a generated UUID), so
	// resolve it from the database instead of assuming a fixed UUID. An
	// explicit TENANT_DEFAULT_ID still wins for multi-tenant deployments.
	defaultTenantIDStr, err := resolveDefaultTenantID(ctx, pool)
	if err != nil {
		logger.Fatal().Err(err).Msg("resolve default tenant id")
	}
	defaultTenantID, err := cart.ToUUID(defaultTenantIDStr)
	if err != nil {
		logger.Fatal().Err(err).Msg("parse default tenant id")
	}

	baseURL, _ := url.Parse(cfg.PublicBaseURL)
	baseDomain := "localhost"
	if baseURL != nil && baseURL.Hostname() != "" {
		baseDomain = baseURL.Hostname()
	}
	tenantResolver := tenant.NewResolver("X-Tenant-ID", baseDomain, defaultTenantIDStr)
	tenantMembership := requireTenantMembership(pool)

	cartSvc := &cart.Service{
		Q:                          queries,
		Pool:                       pool,
		TTL:                        cfg.CartTTL,
		VoucherPerUserLimitDefault: cfg.VoucherPerUserLimit,
		DefaultTenantID:            defaultTenantID,
	}
	voucherSvc := &voucher.Service{Q: queries, DefaultPerUserLimit: cfg.VoucherPerUserLimit}
	voucherHandler := &voucher.Handler{Q: queries, Svc: voucherSvc, Pool: pool, DefaultPriority: cfg.VoucherDefaultPriority, CatalogCache: catalogCache, Analytics: nil}
	campaignHandler := &campaign.Handler{Pool: pool}
	cartHandler := &cart.Handler{
		Q:              queries,
		Svc:            cartSvc,
		ShippingClient: shipping.MockClient{},
		ShippingOrigin: cfg.ShippingOriginCode,
		TaxBps:         cfg.PricingTaxRateBPS,
		Currency:       cfg.CurrencyCode,
	}

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
	emailNotifier := notify.EmailNotifier{
		Mail:         mailer,
		Enabled:      cfg.NotifyEmailEnabled,
		From:         cfg.NotifyEmailFrom,
		TopicToggles: cfg.NotifyEmailTopics,
	}
	notificationsSvc := &notifications.Service{Q: queries}
	notificationsHandler := &notifications.Handler{Svc: notificationsSvc}
	notificationsNotifier := &notifications.Notifier{
		Svc:    notificationsSvc,
		Orders: queries,
		OnError: func(err error) {
			if err != nil {
				logger.Error().Err(err).Msg("record in-app notification")
			}
		},
	}
	bus := &events.Bus{
		Store:     queries,
		Scheduler: dispatcher,
		Notifiers: []events.Notifier{emailNotifier, notificationsNotifier},
	}

	orderHandler := &order.Handler{Q: queries}
	orderAdmin := &order.AdminHandler{Q: queries}
	notifyAdmin := &notify.AdminHandler{Store: notifyStore, Disp: dispatcher}
	queueAdmin := &queue.AdminHandler{
		Store:             queue.NewStore(pool),
		Queue:             taskQueue,
		PageSize:          cfg.AdminDLQPageSize,
		Logger:            logger,
		VisibilityTimeout: cfg.QueueVisibilityTimeout,
	}

	var shipProvider shipping.Provider
	switch cfg.ShippingProvider {
	case "rajaongkir-mock", "mock":
		shipProvider = shipping.RajaOngkirMock{}
	case "rajaongkir", "":
		shipProvider = shipping.RajaOngkirTracker{APIKey: cfg.RajaOngkirAPIKey, BaseURL: cfg.RajaOngkirBaseURL}
	default:
		logger.Fatal().Str("provider", cfg.ShippingProvider).Msg("unsupported shipping provider")
	}
	shipSvc := &shipping.Service{
		Q:                      queries,
		Provider:               shipProvider,
		Mail:                   mailer,
		NotifyOnShipped:        cfg.NotifyOnShipped,
		NotifyOnOutForDelivery: cfg.NotifyOnOutForDelivery,
		NotifyOnDelivered:      cfg.NotifyOnDelivered,
		Events:                 bus,
	}
	shipHandler := &shipping.Handler{Svc: shipSvc, Q: queries}
	shipWebhook := shipping.Webhook{Svc: shipSvc, Replay: redisClient, ReplayTTL: cfg.ShippingTrackReplayTTL}
	switch cfg.ShippingProvider {
	case "rajaongkir", "":
		cartHandler.ShippingClient = shipping.RajaOngkirClient{APIKey: cfg.RajaOngkirAPIKey, BaseURL: cfg.RajaOngkirBaseURL}
	case "rajaongkir-mock", "mock":
		cartHandler.ShippingClient = shipping.MockClient{}
	}

	providers := map[string]payment.Provider{
		"midtrans": payment.Midtrans{
			ServerKey: cfg.MidtransServerKey,
			BaseURL:   cfg.MidtransBaseURL,
			Sandbox:   cfg.PaymentSandbox,
		},
		"xendit": payment.Xendit{
			SecretKey:     cfg.XenditSecretKey,
			BaseURL:       cfg.XenditBaseURL,
			CallbackToken: cfg.XenditCallbackToken,
		},
		"stub": payment.Midtrans{Stub: true, Sandbox: true},
	}
	activeProvider := providers[cfg.PaymentProvider]
	if activeProvider == nil {
		activeProvider = providers["midtrans"]
	}
	paymentSvc := &payment.Service{
		Q:               queries,
		Provider:        activeProvider,
		IntentTTL:       cfg.PaymentIntentTTL,
		CallbackBaseURL: cfg.PaymentCallbackBaseURL,
	}
	paymentHandler := &payment.Handler{Svc: paymentSvc, Q: queries, Pool: pool, BankName: cfg.PaymentBankName, BankAccountName: cfg.PaymentBankAccountName, BankAccountNumber: cfg.PaymentBankAccountNumber, QRURL: cfg.PaymentQRURL}

	checkoutSvc := &checkout.Service{
		Q:                       queries,
		Pool:                    pool,
		CartSvc:                 cartSvc,
		PaymentSvc:              paymentSvc,
		TaxBps:                  cfg.PricingTaxRateBPS,
		Currency:                cfg.CurrencyCode,
		Events:                  bus,
		InventoryReservationTTL: cfg.PaymentIntentTTL,
	}
	checkoutHandler := &checkout.Handler{Svc: checkoutSvc}

	webhookHandler := payment.Webhook{
		Q:            queries,
		Pool:         pool,
		Providers:    providers,
		Replay:       redisClient,
		ReplayTTL:    cfg.WebhookReplayTTL,
		Voucher:      voucherSvc,
		Events:       bus,
		CatalogCache: catalogCache,
		Analytics:    nil,
	}
	platformHandler := &platform.Handler{Pool: pool, Providers: providers}

	analyticsSvc := &analytics.Service{Q: queries, R: redisClient, TTL: cfg.AnalyticsCacheTTL, DefaultRange: cfg.AnalyticsDefaultRange, Prefix: cfg.RedisCachePrefix}
	voucherHandler.Analytics = analyticsSvc
	webhookHandler.Analytics = analyticsSvc
	analyticsHandler := &analytics.Handler{Svc: analyticsSvc}

	adminCatalog := &admin.CatalogHandler{Q: queries, Cache: catalogCache}
	adminOrders := &admin.OrdersHandler{Q: queries}
	adminAnalytics := &admin.AnalyticsHandler{Q: queries}

	reviewsSvc := &reviews.Service{Q: queries}
	reviewsHandler := &reviews.Handler{Svc: reviewsSvc}

	qaSvc := &qa.Service{Q: queries}
	qaHandler := &qa.Handler{Svc: qaSvc}

	loyaltySvc := &loyalty.Service{Q: queries}
	loyaltyHandler := &loyalty.Handler{Svc: loyaltySvc}

	pushSvc := &push.Service{Q: queries}
	pushHandler := &push.Handler{Svc: pushSvc}

	favoritesSvc := &favorites.Service{Q: queries}
	favoritesHandler := &favorites.Handler{Svc: favoritesSvc}

	auditSample := envFloat("AUDIT_SAMPLING_RATE", 1.0)
	if auditSample < 0 {
		auditSample = 0
	}
	if auditSample > 1 {
		auditSample = 1
	}
	auditEnabled := envBool("AUDIT_ENABLED", true) && auditSample > 0
	auditSvc := &audit.Service{Store: queries, Enabled: auditEnabled, SamplingRate: auditSample}
	auditHandler := audit.Handler{Store: auditSvc.Store}
	auditRecorder := audit.HTTPRecorder{
		Service: auditSvc,
		OnError: func(err error) {
			if err != nil {
				logger.Error().Err(err).Msg("record audit log")
			}
		},
	}

	securityHeaders := security.Headers{
		Enable:                envBool("SECURITY_ENABLE_HEADERS", true),
		EnableHSTS:            envBool("SECURITY_ENABLE_HSTS", true),
		HSTSMaxAge:            envInt("SECURITY_HSTS_MAX_AGE", 31536000),
		HSTSIncludeSubdomains: envBool("SECURITY_HSTS_INCLUDE_SUBDOMAINS", true),
	}
	corsOrigins := envOrDefault("SECURITY_ALLOWED_ORIGINS", strings.Join(cfg.CORSAllowedOrigins, ","))
	if strings.TrimSpace(corsOrigins) == "" && len(cfg.CORSAllowedOrigins) > 0 {
		corsOrigins = strings.Join(cfg.CORSAllowedOrigins, ",")
	}
	if strings.TrimSpace(corsOrigins) == "" {
		corsOrigins = "http://localhost:3000"
	}
	bodyLimitBytes := envInt("SECURITY_BODY_LIMIT_BYTES", 1_048_576)
	if bodyLimitBytes <= 0 {
		bodyLimitBytes = 1_048_576
	}
	csrfEnabled := envBool("SECURITY_CSRF_ENABLED", true)
	csrfHeader := envOrDefault("SECURITY_CSRF_HEADER", "X-CSRF-Token")

	rateLimitPrefix := envOrDefault("RATE_LIMIT_REDIS_PREFIX", "rl:")
	limiter := ratelimit.Limiter{Client: redisClient, Prefix: rateLimitPrefix}
	rateLimitErr := func(err error) {
		if err != nil {
			logger.Error().Err(err).Msg("rate limiter failure")
		}
	}
	globalLimiter := ratelimit.Handler{
		Limiter: limiter,
		Config: ratelimit.Config{
			Key:    func(*http.Request) string { return "global" },
			Window: time.Duration(envInt("RATE_LIMIT_GLOBAL_WINDOW_SEC", 60)) * time.Second,
			Max:    envInt("RATE_LIMIT_GLOBAL_MAX", 1200),
		},
		OnError: rateLimitErr,
	}.Middleware
	ipLimiter := ratelimit.Handler{
		Limiter: limiter,
		Config: ratelimit.Config{
			Key: func(r *http.Request) string {
				ip := common.ClientIP(r)
				if ip == "" {
					ip = "unknown"
				}
				return "ip:" + ip
			},
			Window: time.Duration(envInt("RATE_LIMIT_IP_WINDOW_SEC", 60)) * time.Second,
			Max:    envInt("RATE_LIMIT_IP_MAX", 240),
		},
		OnError: rateLimitErr,
	}.Middleware
	userLimiter := ratelimit.Handler{
		Limiter: limiter,
		Config: ratelimit.Config{
			Key: func(r *http.Request) string {
				if userID, ok := common.UserID(r.Context()); ok && strings.TrimSpace(userID) != "" {
					return "user:" + userID
				}
				ip := common.ClientIP(r)
				if ip == "" {
					ip = "unknown"
				}
				return "anon:" + ip
			},
			Window: time.Duration(envInt("RATE_LIMIT_USER_WINDOW_SEC", 60)) * time.Second,
			Max:    envInt("RATE_LIMIT_USER_MAX", 120),
		},
		OnError: rateLimitErr,
	}.Middleware
	loginLimiter := ratelimit.Handler{
		Limiter: limiter,
		Config: ratelimit.Config{
			Key: func(r *http.Request) string {
				ip := common.ClientIP(r)
				if ip == "" {
					ip = "unknown"
				}
				return "login:" + ip
			},
			Window: time.Duration(envInt("RATE_LIMIT_LOGIN_WINDOW_SEC", 300)) * time.Second,
			Max:    envInt("RATE_LIMIT_LOGIN_MAX", 10),
		},
		OnError: rateLimitErr,
	}.Middleware

	var httpMetrics *obs.HTTPMetrics
	if metricsEnabled {
		buckets := obs.ParseBucketsCSV(envOrDefault("OBS_METRICS_BUCKETS_MS", ""))
		httpMetrics = obs.NewHTTPMetrics(metricsNamespace, buckets, nil)
	}

	maxInFlight := cfg.HTTPMaxInFlight
	if maxInFlight <= 0 {
		maxInFlight = 400
	}
	inflightSem := make(chan struct{}, maxInFlight)

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(obs.RoutePatternMiddleware)
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			select {
			case inflightSem <- struct{}{}:
				defer func() { <-inflightSem }()
				next.ServeHTTP(w, req)
			case <-req.Context().Done():
				http.Error(w, "request cancelled", http.StatusRequestTimeout)
			}
		})
	})
	if tracingEnabled {
		r.Use(obs.TracingMiddleware)
	}
	if metricsEnabled && httpMetrics != nil {
		r.Use(obs.HTTPObs{Metrics: httpMetrics}.Middleware)
	}
	r.Use(obs.RequestLogger{Logger: logger}.Middleware)
	r.Use(securityHeaders.Middleware)
	if strings.TrimSpace(corsOrigins) != "" {
		r.Use(security.AllowCORS(corsOrigins))
	}
	r.Use(security.BodyLimit{Max: int64(bodyLimitBytes)}.Middleware)
	if csrfEnabled {
		r.Use(security.CSRF{Header: csrfHeader}.Middleware)
	}

	if metricsEnabled {
		r.Handle("/metrics", promhttp.Handler())
	}
	pprofEnabled := envBool("OBS_ENABLE_PPROF", false)
	if pprofEnabled {
		user := envOrDefault("SECURE_PPROF_BASIC_AUTH_USER", "")
		pass := envOrDefault("SECURE_PPROF_BASIC_AUTH_PASS", "")
		r.Mount("/debug/pprof", protectPprof(newPprofMux(), user, pass))
	}

	healthHandler := health.Handler{
		Checker:      readinessChecker{db: pool, redis: redisClient},
		DBTimeout:    envDurationMillis("HEALTH_READY_DB_TIMEOUT_MS", 500),
		RedisTimeout: envDurationMillis("HEALTH_READY_REDIS_TIMEOUT_MS", 300),
	}
	r.Get("/health/live", healthHandler.Live)
	r.Get("/health/ready", healthHandler.Ready)

	r.Route("/api/v1", func(v chi.Router) {
		v.Use(globalLimiter)
		v.Use(ipLimiter)
		v.Use(userLimiter)
		v.Use(tenantResolver.Middleware)

		v.Get("/categories", catalogHandler.Categories)
		v.Get("/brands", catalogHandler.Brands)
		v.Get("/products", catalogHandler.Products)
		v.Get("/products/{slug}", catalogHandler.ProductDetail)
		v.Get("/products/{slug}/related", catalogHandler.Related)
		v.Get("/vouchers", voucherHandler.PublicList)
		v.Get("/flash-sales", campaignHandler.Public)

		// Recommendations
		v.Get("/recommendations/personalized", recommendationsHandler.Personalized)
		v.Get("/recommendations/trending", recommendationsHandler.Trending)
		v.Get("/products/{id}/frequently-bought-together", recommendationsHandler.FrequentlyBoughtTogether)
		v.Get("/products/{id}/also-viewed", recommendationsHandler.CustomersAlsoViewed)

		// Reviews
		v.Get("/products/{id}/reviews", reviewsHandler.List)
		v.Get("/products/{id}/reviews/stats", reviewsHandler.Stats)
		v.With(authMiddleware.RequireAuth, tenantMembership).Post("/products/{id}/reviews", reviewsHandler.Create)
		v.With(authMiddleware.RequireAuth, tenantMembership).Delete("/products/{id}/reviews", reviewsHandler.Delete) // Optional

		// Product Q&A
		v.Get("/products/{id}/questions", qaHandler.List)
		v.With(authMiddleware.RequireAuth, tenantMembership).Post("/products/{id}/questions", qaHandler.Create)
		v.With(authMiddleware.RequireAuth, tenantMembership).Post("/products/{id}/questions/{questionId}/answer", qaHandler.Answer)
		v.With(authMiddleware.RequireAuth, tenantMembership).Post("/products/{id}/questions/{questionId}/vote", qaHandler.Vote)

		// Loyalty
		v.Route("/loyalty", func(l chi.Router) {
			l.Use(authMiddleware.RequireAuth)
			l.Use(tenantMembership)
			l.Get("/profile", loyaltyHandler.GetProfile)
			l.Get("/transactions", loyaltyHandler.GetTransactions)
			l.Post("/redeem", loyaltyHandler.RedeemReward)
		})

		// Web Push
		v.Route("/push", func(p chi.Router) {
			p.Use(authMiddleware.RequireAuth)
			p.Use(tenantMembership)
			p.Get("/vapid-key", pushHandler.GetVapidPublicKey)
			p.Post("/subscription", pushHandler.Subscribe)
			p.Delete("/subscription", pushHandler.Unsubscribe)
			p.Get("/preferences", pushHandler.GetPreferences)
			p.Patch("/preferences", pushHandler.UpdatePreferences)
			p.Post("/send-test", pushHandler.SendTest)
		})

		// Favorites
		v.Route("/favorites", func(f chi.Router) {
			f.Use(authMiddleware.RequireAuth)
			f.Use(tenantMembership)
			f.Get("/", favoritesHandler.List)
			f.Post("/", favoritesHandler.Toggle)
			f.Get("/{id}", favoritesHandler.Check)
		})

		v.Route("/auth", func(a chi.Router) {
			a.Use(auditRecorder.Middleware(audit.HTTPConfig{ResourceType: "auth"}))
			a.Post("/register", authHandler.Register)
			a.With(loginLimiter).Post("/login", authHandler.Login)
			a.Post("/refresh", authHandler.Refresh)
			a.Post("/logout", authHandler.Logout)
			a.With(loginLimiter).Post("/password/forgot", authHandler.Forgot)
			a.With(loginLimiter).Post("/password/reset", authHandler.Reset)
			a.Post("/email/verify", authHandler.VerifyEmail)
			a.With(loginLimiter).Post("/email/resend", authHandler.ResendVerification)

			a.Group(func(protected chi.Router) {
				protected.Use(authMiddleware.RequireAuth)
				protected.Use(tenantMembership)
				protected.Get("/me", authHandler.Me)
				protected.Get("/sessions", authHandler.ListSessions)
				protected.Post("/logout/all", authHandler.LogoutAll)
			})
		})

		// Registered directly rather than via Route("/users/me") so it does not
		// mount a subrouter that would collide with /users/me/addresses below.
		v.With(authMiddleware.RequireAuth, tenantMembership).Patch("/users/me", authHandler.UpdateProfile)

		v.Route("/users/me/addresses", func(a chi.Router) {
			a.Use(authMiddleware.RequireAuth)
			a.Use(tenantMembership)
			a.Get("/", addressHandler.List)
			a.Post("/", addressHandler.Create)
			a.Route("/{addressID}", func(child chi.Router) {
				child.Patch("/", addressHandler.Update)
				child.Delete("/", addressHandler.Delete)
			})
		})

		v.Route("/carts", func(c chi.Router) {
			c.Get("/", cartHandler.GetActive)
			c.Get("/{id}", cartHandler.Get)
			c.Group(func(g chi.Router) {
				g.Use(idem.Middleware)
				g.Post("/", cartHandler.Create)
				g.Post("/{id}/items", cartHandler.AddItem)
				g.Patch("/{id}/items/{itemId}", cartHandler.UpdateItem)
				g.Delete("/{id}/items/{itemId}", cartHandler.RemoveItem)
				g.Post("/{id}/apply-voucher", cartHandler.ApplyVoucher)
				g.Delete("/{id}/voucher", cartHandler.RemoveVoucher)
				g.Post("/{id}/quote/shipping", cartHandler.QuoteShipping)
				g.Post("/{id}/quote/tax", cartHandler.QuoteTax)
				g.With(authMiddleware.RequireAuth, tenantMembership).Post("/merge", cartHandler.Merge)
			})
		})

		v.With(idem.Middleware, authMiddleware.RequireAuth, tenantMembership).Post("/checkout", checkoutHandler.Checkout)

		v.With(idem.Middleware, authMiddleware.RequireAuth, tenantMembership).Post("/checkout/draft", checkoutHandler.CreateDraft)
		v.Group(func(authR chi.Router) {
			authR.Use(authMiddleware.RequireAuth)
			authR.Use(tenantMembership)
			authR.Get("/tenant", platformHandler.Tenant)
			authR.Get("/users/me/privacy", platformHandler.PrivacyGet)
			authR.Put("/users/me/privacy", platformHandler.PrivacyUpdate)
			authR.Get("/users/me/data-export", platformHandler.DataExport)
			authR.Delete("/users/me", platformHandler.DeleteAccount)
			authR.Post("/onboarding", platformHandler.CreateTenant)
			authR.Get("/orders", orderHandler.List)
			authR.Get("/orders/{orderId}", orderHandler.Get)
			authR.Get("/orders/{orderId}/shipment", shipHandler.GetByOrder)
			authR.Post("/orders/{orderId}/cancel", orderHandler.Cancel)
			authR.Post("/orders/{orderId}/returns", platformHandler.ReturnCreate)
			authR.Get("/returns", platformHandler.ReturnList)
			authR.Get("/returns/{returnId}", platformHandler.ReturnGet)
			authR.Post("/support/tickets", platformHandler.TicketCreate)
			authR.Get("/support/tickets", platformHandler.TicketList)
			authR.Get("/support/tickets/{ticketId}/messages", platformHandler.TicketMessages)
			authR.Post("/support/tickets/{ticketId}/messages", platformHandler.TicketMessage)

			authR.Get("/notifications", notificationsHandler.List)
			authR.Get("/notifications/unread-count", notificationsHandler.UnreadCount)
			authR.Post("/notifications/read-all", notificationsHandler.MarkAllRead)
			authR.Post("/notifications/{id}/read", notificationsHandler.MarkRead)
		})

		v.Route("/admin", func(admin chi.Router) {
			admin.Use(authMiddleware.RequireAuth)
			admin.Use(tenantMembership)
			admin.Use(requireRole(queries, "admin"))
			admin.Use(auditRecorder.Middleware(audit.HTTPConfig{ResourceType: "admin"}))
			admin.Post("/vouchers", voucherHandler.Create)
			admin.Put("/vouchers/{code}", voucherHandler.Update)
			admin.Post("/vouchers/preview", voucherHandler.Preview)
			admin.Post("/flash-sales", campaignHandler.AdminCreate)
			admin.Get("/flash-sales", campaignHandler.AdminList)
			admin.Get("/flash-sales/{id}", campaignHandler.AdminGet)
			admin.Patch("/flash-sales/{id}", campaignHandler.AdminUpdate)
			admin.Post("/orders/{id}/shipment", shipHandler.AdminCreate)
			admin.Patch("/orders/{id}/status", orderAdmin.PatchStatus)
			admin.Post("/webhooks", notifyAdmin.CreateEndpoint)
			admin.Put("/webhooks/{id}", notifyAdmin.UpdateEndpoint)
			admin.Get("/webhooks", notifyAdmin.ListEndpoints)
			admin.Delete("/webhooks/{id}", notifyAdmin.DeleteEndpoint)
			admin.Get("/webhook-deliveries", notifyAdmin.ListDeliveries)
			admin.Post("/webhook-deliveries/{id}/replay", notifyAdmin.ReplayDelivery)
			admin.Get("/queue/dlq", queueAdmin.ListDLQ)
			admin.Post("/queue/dlq/replay", queueAdmin.ReplayDLQ)
			admin.Get("/queue/stats", queueAdmin.Stats)
			admin.Get("/audit-logs", auditHandler.List)
			admin.Get("/customers", platformHandler.Customers)
			admin.Get("/inventory", platformHandler.InventoryList)
			admin.Patch("/inventory/{variantId}", platformHandler.InventoryUpdate)
			admin.Get("/returns", platformHandler.AdminReturnList)
			admin.Patch("/returns/{returnId}", platformHandler.AdminReturnStatus)
			admin.Post("/returns/{returnId}/refund", platformHandler.AdminRefund)
			admin.Get("/support/tickets", platformHandler.AdminTicketList)
			admin.Get("/support/tickets/{ticketId}/messages", platformHandler.AdminTicketMessages)
			admin.Patch("/support/tickets/{ticketId}", platformHandler.AdminTicketStatus)
			admin.Post("/support/tickets/{ticketId}/messages", platformHandler.AdminTicketMessage)
			admin.Get("/settings", platformHandler.SettingsGet)
			admin.Patch("/settings", platformHandler.SettingsUpdate)
			admin.Post("/onboarding", platformHandler.Onboarding)

			// Catalog management.
			admin.Get("/products", adminCatalog.ListProducts)
			admin.Post("/products", adminCatalog.CreateProduct)
			admin.Get("/products/{id}", adminCatalog.GetProduct)
			admin.Patch("/products/{id}", adminCatalog.UpdateProduct)
			admin.Delete("/products/{id}", adminCatalog.DeleteProduct)
			admin.Patch("/products/{id}/stock", adminCatalog.UpdateProductStock)

			admin.Get("/categories", adminCatalog.ListCategories)
			admin.Post("/categories", adminCatalog.CreateCategory)
			admin.Patch("/categories/{id}", adminCatalog.UpdateCategory)
			admin.Delete("/categories/{id}", adminCatalog.DeleteCategory)

			admin.Get("/brands", adminCatalog.ListBrands)
			admin.Post("/brands", adminCatalog.CreateBrand)
			admin.Patch("/brands/{id}", adminCatalog.UpdateBrand)
			admin.Delete("/brands/{id}", adminCatalog.DeleteBrand)

			// Order and voucher management reads. "/orders/stats" is registered
			// before "/orders/{id}" so chi does not treat "stats" as an id.
			admin.Get("/orders", adminOrders.ListOrders)
			admin.Get("/orders/stats", adminOrders.OrderStats)
			admin.Get("/orders/{id}", adminOrders.GetOrder)

			admin.Get("/vouchers", adminOrders.ListVouchers)
			admin.Get("/vouchers/stats", adminOrders.VoucherStats)
			admin.Delete("/vouchers/{code}", adminOrders.DeleteVoucher)

			admin.Get("/analytics/overview", adminAnalytics.Overview)
		})

		v.Route("/analytics", func(an chi.Router) {
			an.Use(authMiddleware.RequireAuth)
			an.Use(tenantMembership)
			an.Use(requireRole(queries, "admin"))
			an.Get("/sales", analyticsHandler.Sales)
			an.Get("/top-products", analyticsHandler.TopProducts)
			an.Get("/overview", analyticsHandler.Overview)
		})

		v.Route("/payments", func(p chi.Router) {
			p.Use(authMiddleware.RequireAuth)
			p.Use(tenantMembership)
			p.Group(func(g chi.Router) {
				g.Use(idem.Middleware)
				g.Post("/intent", paymentHandler.Intent)
			})
			p.Get("/{orderId}/status", paymentHandler.Status)
			p.Get("/{orderId}/instructions", paymentHandler.Instructions)
			p.Post("/{orderId}/proof", paymentHandler.UploadProof)
		})

		v.Post("/webhooks/shipping/{courier}", shipWebhook.Handle)
		v.Post("/webhooks/payment/{provider}", webhookHandler.Handle)
	})

	srv := &http.Server{
		Addr:    cfg.HTTPAddr(),
		Handler: r,
	}

	health.SetReady(true)
	logger.Info().Str("addr", srv.Addr).Msg("server starting")
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatal().Err(err).Msg("server exited unexpectedly")
		}
	}()

	shutdownCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	<-shutdownCtx.Done()
	health.SetReady(false)
	ctxTimeout := cfg.APIMaxShutdownGrace
	if ctxTimeout <= 0 {
		ctxTimeout = 15 * time.Second
	}
	shutdownTimeout, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownTimeout); err != nil {
		logger.Error().Err(err).Msg("server shutdown")
	}
}

func requireRole(q dbgen.Querier, role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if q == nil {
				common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "role validator not configured", nil)
				return
			}
			userID, ok := common.UserID(r.Context())
			if !ok {
				common.JSONError(w, http.StatusForbidden, "FORBIDDEN", "forbidden", nil)
				return
			}
			uid, err := cart.ToUUID(userID)
			if err != nil {
				common.JSONError(w, http.StatusForbidden, "FORBIDDEN", "forbidden", nil)
				return
			}
			user, err := q.GetUserByID(r.Context(), uid)
			if err != nil {
				common.JSONError(w, http.StatusForbidden, "FORBIDDEN", "forbidden", nil)
				return
			}
			if !slices.Contains(user.Roles, role) {
				common.JSONError(w, http.StatusForbidden, "FORBIDDEN", "insufficient permissions", nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// requireTenantMembership prevents an authenticated user from selecting an
// arbitrary tenant with X-Tenant-ID. Public catalogue requests remain open,
// while every authenticated workflow requires an ACTIVE membership row.
func requireTenantMembership(pool *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if pool == nil {
				common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "tenant membership store not configured", nil)
				return
			}
			userID, hasUser := common.UserID(r.Context())
			tenantID, hasTenant := tenant.FromContext(r.Context())
			if !hasUser || !hasTenant {
				common.JSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication and tenant are required", nil)
				return
			}
			uid, userErr := cart.ToUUID(userID)
			tid, tenantErr := cart.ToUUID(tenantID)
			if userErr != nil || tenantErr != nil {
				common.JSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid authentication or tenant context", nil)
				return
			}
			var member bool
			if err := pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM tenant_memberships WHERE tenant_id=$1 AND user_id=$2 AND status='ACTIVE')`, tid, uid).Scan(&member); err != nil {
				common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to validate tenant membership", nil)
				return
			}
			if !member {
				common.JSONError(w, http.StatusForbidden, "TENANT_FORBIDDEN", "user is not a member of this tenant", nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

type readinessChecker struct {
	db    *pgxpool.Pool
	redis *redis.Client
}

func (c readinessChecker) PingDB(ctx context.Context, timeout time.Duration) error {
	if c.db == nil {
		return errors.New("db not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return c.db.Ping(ctx)
}

func (c readinessChecker) PingRedis(ctx context.Context, timeout time.Duration) error {
	if c.redis == nil {
		return errors.New("redis not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return c.redis.Ping(ctx).Err()
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

// buildMailer returns the transactional email sender. Without SMTP_HOST it
// falls back to a no-op and says so loudly: password reset and email
// verification both mint tokens that are useless if nothing delivers them, and
// that failure is otherwise completely silent.
func buildMailer(cfg *config.Config, logger zerolog.Logger) common.EmailSender {
	if strings.TrimSpace(cfg.SMTPHost) == "" {
		logger.Warn().
			Msg("SMTP_HOST not set: transactional email is disabled, password reset and email verification links will not be delivered")
		return common.NopEmailSender{}
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
		// Misconfigured SMTP is an operator error worth failing on: starting
		// with silently broken email is worse than not starting.
		logger.Fatal().Err(err).Msg("configure smtp sender")
	}

	if cfg.SMTPAllowInsecureTLS {
		logger.Warn().Msg("SMTP_ALLOW_INSECURE_TLS is enabled: TLS certificates are not verified")
	}
	if strings.TrimSpace(cfg.PublicBaseURL) == "" {
		// Reset and verification mails embed a storefront link built from this
		// value. Without it the link is relative, which no mail client can follow.
		logger.Warn().
			Msg("PUBLIC_BASE_URL not set: password reset and verification emails will contain relative, unusable links")
	}
	logger.Info().
		Str("host", cfg.SMTPHost).
		Int("port", cfg.SMTPPort).
		Bool("implicitTLS", cfg.SMTPImplicitTLS).
		Str("from", cfg.SMTPFrom).
		Msg("transactional email enabled")

	return sender
}

// resolveDefaultTenantID returns the tenant every unscoped request falls back to.
// An explicit TENANT_DEFAULT_ID wins; otherwise it reads the seeded 'default'
// tenant so the value always references a row that actually exists.
func resolveDefaultTenantID(ctx context.Context, pool *pgxpool.Pool) (string, error) {
	if configured := envOrDefault("TENANT_DEFAULT_ID", ""); configured != "" {
		return configured, nil
	}
	var id string
	err := pool.QueryRow(ctx, `SELECT id::text FROM tenants WHERE slug = 'default'`).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("look up default tenant (slug='default'): %w", err)
	}
	return id, nil
}

func envBool(key string, fallback bool) bool {
	if val, ok := os.LookupEnv(key); ok {
		switch strings.ToLower(strings.TrimSpace(val)) {
		case "1", "t", "true", "yes", "on":
			return true
		case "0", "f", "false", "no", "off":
			return false
		}
	}
	return fallback
}

func envFloat(key string, fallback float64) float64 {
	if val, ok := os.LookupEnv(key); ok {
		if parsed, err := strconv.ParseFloat(strings.TrimSpace(val), 64); err == nil {
			return parsed
		}
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if val, ok := os.LookupEnv(key); ok {
		if parsed, err := strconv.Atoi(strings.TrimSpace(val)); err == nil {
			return parsed
		}
	}
	return fallback
}

func envDurationMillis(key string, fallback int) time.Duration {
	return time.Duration(envInt(key, fallback)) * time.Millisecond
}

func newPprofMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", pprof.Index)
	mux.HandleFunc("/cmdline", pprof.Cmdline)
	mux.HandleFunc("/profile", pprof.Profile)
	mux.HandleFunc("/symbol", pprof.Symbol)
	mux.HandleFunc("/trace", pprof.Trace)
	mux.Handle("/allocs", pprof.Handler("allocs"))
	mux.Handle("/block", pprof.Handler("block"))
	mux.Handle("/goroutine", pprof.Handler("goroutine"))
	mux.Handle("/heap", pprof.Handler("heap"))
	mux.Handle("/mutex", pprof.Handler("mutex"))
	mux.Handle("/threadcreate", pprof.Handler("threadcreate"))
	return mux
}

func protectPprof(handler http.Handler, user, pass string) http.Handler {
	user = strings.TrimSpace(user)
	pass = strings.TrimSpace(pass)
	if user == "" {
		return handler
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(u), []byte(user)) != 1 || subtle.ConstantTimeCompare([]byte(p), []byte(pass)) != 1 {
			w.Header().Set("WWW-Authenticate", "Basic realm=restricted")
			http.Error(w, "unauthorised", http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	})
}
