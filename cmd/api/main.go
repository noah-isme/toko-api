package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"net/http/pprof"
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

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/noah-isme/backend-toko/internal/analytics"
	"github.com/noah-isme/backend-toko/internal/audit"
	"github.com/noah-isme/backend-toko/internal/auth"
	"github.com/noah-isme/backend-toko/internal/cart"
	"github.com/noah-isme/backend-toko/internal/catalog"
	"github.com/noah-isme/backend-toko/internal/checkout"
	"github.com/noah-isme/backend-toko/internal/common"
	"github.com/noah-isme/backend-toko/internal/config"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/events"
	"github.com/noah-isme/backend-toko/internal/health"
	"github.com/noah-isme/backend-toko/internal/notify"
	"github.com/noah-isme/backend-toko/internal/obs"
	"github.com/noah-isme/backend-toko/internal/order"
	"github.com/noah-isme/backend-toko/internal/payment"
	"github.com/noah-isme/backend-toko/internal/queue"
	"github.com/noah-isme/backend-toko/internal/ratelimit"
	"github.com/noah-isme/backend-toko/internal/resilience"
	"github.com/noah-isme/backend-toko/internal/security"
	"github.com/noah-isme/backend-toko/internal/shipping"
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

	mailer := common.NopEmailSender{}

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
		Mailer:                common.NopEmailSender{},
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

	cartSvc := &cart.Service{Q: queries, TTL: cfg.CartTTL, VoucherPerUserLimitDefault: cfg.VoucherPerUserLimit}
	voucherSvc := &voucher.Service{Q: queries, DefaultPerUserLimit: cfg.VoucherPerUserLimit}
	voucherHandler := &voucher.Handler{Q: queries, Svc: voucherSvc, DefaultPriority: cfg.VoucherDefaultPriority, CatalogCache: catalogCache, Analytics: nil}
	cartHandler := &cart.Handler{
		Q:              queries,
		Svc:            cartSvc,
		ShippingClient: shipping.MockClient{},
		ShippingOrigin: cfg.ShippingOriginCode,
		TaxBps:         cfg.PricingTaxRateBPS,
		Currency:       cfg.CurrencyCode,
	}

	notifyStore := notify.NewStore(queries)
	taskQueue := queue.Enqueuer{R: redisClient, Prefix: cfg.QueueRedisPrefix, DedupTTL: cfg.IdempotencyTTL}
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
	bus := &events.Bus{
		Store:     queries,
		Scheduler: dispatcher,
		Notifiers: []events.Notifier{emailNotifier},
	}

	checkoutSvc := &checkout.Service{
		Q:        queries,
		Pool:     pool,
		CartSvc:  cartSvc,
		TaxBps:   cfg.PricingTaxRateBPS,
		Currency: cfg.CurrencyCode,
		Events:   bus,
	}
	checkoutHandler := &checkout.Handler{Svc: checkoutSvc}

	orderHandler := &order.Handler{Q: queries}
	orderAdmin := &order.AdminHandler{Q: queries}
	notifyAdmin := &notify.AdminHandler{Store: notifyStore, Disp: dispatcher}

	var shipProvider shipping.Provider
	switch cfg.ShippingProvider {
	case "rajaongkir-mock", "":
		shipProvider = shipping.RajaOngkirMock{}
	default:
		shipProvider = shipping.RajaOngkirMock{}
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

	providers := map[string]payment.Provider{
		"midtrans": payment.Midtrans{
			ServerKey: cfg.MidtransServerKey,
			BaseURL:   cfg.MidtransBaseURL,
			Sandbox:   cfg.PaymentSandbox,
		},
		"xendit": payment.Xendit{
			SecretKey: cfg.XenditSecretKey,
			BaseURL:   cfg.XenditBaseURL,
		},
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
	paymentHandler := &payment.Handler{Svc: paymentSvc, Q: queries}
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

	analyticsSvc := &analytics.Service{Q: queries, R: redisClient, TTL: cfg.AnalyticsCacheTTL, DefaultRange: cfg.AnalyticsDefaultRange, Prefix: cfg.RedisCachePrefix}
	voucherHandler.Analytics = analyticsSvc
	webhookHandler.Analytics = analyticsSvc
	analyticsHandler := &analytics.Handler{Svc: analyticsSvc}

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
	pprofEnabled := envBool("OBS_ENABLE_PPROF", true)
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
		v.Get("/categories", catalogHandler.Categories)
		v.Get("/brands", catalogHandler.Brands)
		v.Get("/products", catalogHandler.Products)
		v.Get("/products/{slug}", catalogHandler.ProductDetail)
		v.Get("/products/{slug}/related", catalogHandler.Related)

		v.Route("/auth", func(a chi.Router) {
			a.Use(auditRecorder.Middleware(audit.HTTPConfig{ResourceType: "auth"}))
			a.Post("/register", authHandler.Register)
			a.With(loginLimiter).Post("/login", authHandler.Login)
			a.Post("/refresh", authHandler.Refresh)
			a.Post("/logout", authHandler.Logout)
			a.With(loginLimiter).Post("/password/forgot", authHandler.Forgot)
			a.With(loginLimiter).Post("/password/reset", authHandler.Reset)

			a.Group(func(protected chi.Router) {
				protected.Use(authMiddleware.RequireAuth)
				protected.Get("/me", authHandler.Me)
			})
		})

		v.Route("/users/me/addresses", func(a chi.Router) {
			a.Use(authMiddleware.RequireAuth)
			a.Get("/", addressHandler.List)
			a.Post("/", addressHandler.Create)
			a.Route("/{addressID}", func(child chi.Router) {
				child.Patch("/", addressHandler.Update)
				child.Delete("/", addressHandler.Delete)
			})
		})

		v.Route("/carts", func(c chi.Router) {
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
				g.With(authMiddleware.RequireAuth).Post("/merge", cartHandler.Merge)
			})
		})

		v.With(idem.Middleware, authMiddleware.RequireAuth).Post("/checkout", checkoutHandler.Checkout)

		v.Group(func(authR chi.Router) {
			authR.Use(authMiddleware.RequireAuth)
			authR.Get("/orders", orderHandler.List)
			authR.Get("/orders/{orderId}", orderHandler.Get)
			authR.Get("/orders/{orderId}/shipment", shipHandler.GetByOrder)
			authR.Post("/orders/{orderId}/cancel", orderHandler.Cancel)
		})

		v.Route("/admin", func(admin chi.Router) {
			admin.Use(authMiddleware.RequireAuth)
			admin.Use(requireRole(queries, "admin"))
			admin.Use(auditRecorder.Middleware(audit.HTTPConfig{ResourceType: "admin"}))
			admin.Post("/vouchers", voucherHandler.Create)
			admin.Put("/vouchers/{code}", voucherHandler.Update)
			admin.Post("/vouchers/preview", voucherHandler.Preview)
			admin.Post("/orders/{id}/shipment", shipHandler.AdminCreate)
			admin.Patch("/orders/{id}/status", orderAdmin.PatchStatus)
			admin.Post("/webhooks", notifyAdmin.CreateEndpoint)
			admin.Put("/webhooks/{id}", notifyAdmin.UpdateEndpoint)
			admin.Get("/webhooks", notifyAdmin.ListEndpoints)
			admin.Delete("/webhooks/{id}", notifyAdmin.DeleteEndpoint)
			admin.Get("/webhook-deliveries", notifyAdmin.ListDeliveries)
			admin.Post("/webhook-deliveries/{id}/replay", notifyAdmin.ReplayDelivery)
			admin.Get("/audit-logs", auditHandler.List)
		})

		v.Route("/analytics", func(an chi.Router) {
			an.Use(authMiddleware.RequireAuth)
			an.Use(requireRole(queries, "admin"))
			an.Get("/sales", analyticsHandler.Sales)
			an.Get("/top-products", analyticsHandler.TopProducts)
			an.Get("/overview", analyticsHandler.Overview)
		})

		v.Route("/payments", func(p chi.Router) {
			p.Use(authMiddleware.RequireAuth)
			p.Group(func(g chi.Router) {
				g.Use(idem.Middleware)
				g.Post("/intent", paymentHandler.Intent)
			})
			p.Get("/{orderId}/status", paymentHandler.Status)
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
