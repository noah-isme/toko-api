package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"slices"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/hlog"

	"github.com/jackc/pgx/v5/pgxpool"
	redis "github.com/redis/go-redis/v9"

	"github.com/noah-isme/backend-toko/internal/analytics"
	"github.com/noah-isme/backend-toko/internal/auth"
	"github.com/noah-isme/backend-toko/internal/cart"
	"github.com/noah-isme/backend-toko/internal/catalog"
	"github.com/noah-isme/backend-toko/internal/checkout"
	"github.com/noah-isme/backend-toko/internal/common"
	"github.com/noah-isme/backend-toko/internal/config"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/order"
	"github.com/noah-isme/backend-toko/internal/payment"
	"github.com/noah-isme/backend-toko/internal/shipping"
	"github.com/noah-isme/backend-toko/internal/user"
	"github.com/noah-isme/backend-toko/internal/voucher"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}

	logger := newLogger(cfg.AppEnv)

	mailer := common.NopEmailSender{}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Fatal().Err(err).Msg("connect database")
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		logger.Fatal().Err(err).Msg("ping database")
	}

	queries := dbgen.New(pool)

	redisOpts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		logger.Fatal().Err(err).Msg("parse redis url")
	}
	redisClient := redis.NewClient(redisOpts)
	defer func() {
		if err := redisClient.Close(); err != nil {
			logger.Error().Err(err).Msg("close redis")
		}
	}()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		logger.Fatal().Err(err).Msg("ping redis")
	}
	catalogService, err := catalog.NewService(catalog.ServiceConfig{
		Queries:      queries,
		Cache:        catalog.NewCache(redisClient, cfg.CatalogCacheTTL),
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
	voucherHandler := &voucher.Handler{Q: queries, Svc: voucherSvc, DefaultPriority: cfg.VoucherDefaultPriority}
	cartHandler := &cart.Handler{
		Q:              queries,
		Svc:            cartSvc,
		ShippingClient: shipping.MockClient{},
		ShippingOrigin: cfg.ShippingOriginCode,
		TaxBps:         cfg.PricingTaxRateBPS,
		Currency:       cfg.CurrencyCode,
	}

	checkoutSvc := &checkout.Service{
		Q:        queries,
		Pool:     pool,
		CartSvc:  cartSvc,
		TaxBps:   cfg.PricingTaxRateBPS,
		Currency: cfg.CurrencyCode,
	}
	checkoutHandler := &checkout.Handler{Svc: checkoutSvc}

	orderHandler := &order.Handler{Q: queries}
	orderAdmin := &order.AdminHandler{Q: queries}

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
		Q:         queries,
		Pool:      pool,
		Providers: providers,
		Replay:    redisClient,
		ReplayTTL: cfg.WebhookReplayTTL,
		Voucher:   voucherSvc,
	}

	analyticsSvc := &analytics.Service{Q: queries, R: redisClient, TTL: cfg.AnalyticsCacheTTL, DefaultRange: cfg.AnalyticsDefaultRange}
	analyticsHandler := &analytics.Handler{Svc: analyticsSvc}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(hlog.NewHandler(logger))
	r.Use(hlog.AccessHandler(func(r *http.Request, status, size int, duration time.Duration) {
		hlog.FromRequest(r).Info().
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Int("status", status).
			Int("size", size).
			Dur("duration", duration).
			Msg("request")
	}))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   allowedOrigins(cfg),
		AllowedMethods:   []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	r.Get("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	r.Route("/api/v1", func(v chi.Router) {
		v.Get("/categories", catalogHandler.Categories)
		v.Get("/brands", catalogHandler.Brands)
		v.Get("/products", catalogHandler.Products)
		v.Get("/products/{slug}", catalogHandler.ProductDetail)
		v.Get("/products/{slug}/related", catalogHandler.Related)

		v.Route("/auth", func(a chi.Router) {
			a.Post("/register", authHandler.Register)
			a.Post("/login", authHandler.Login)
			a.Post("/refresh", authHandler.Refresh)
			a.Post("/logout", authHandler.Logout)
			a.Post("/password/forgot", authHandler.Forgot)
			a.Post("/password/reset", authHandler.Reset)

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
			admin.Post("/vouchers", voucherHandler.Create)
			admin.Put("/vouchers/{code}", voucherHandler.Update)
			admin.Post("/vouchers/preview", voucherHandler.Preview)
			admin.Post("/orders/{id}/shipment", shipHandler.AdminCreate)
			admin.Patch("/orders/{id}/status", orderAdmin.PatchStatus)
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

	logger.Info().Str("addr", srv.Addr).Msg("server starting")
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Fatal().Err(err).Msg("server exited unexpectedly")
	}
}

func newLogger(env string) zerolog.Logger {
	zerolog.TimeFieldFormat = time.RFC3339
	base := zerolog.New(os.Stdout).With().Timestamp().Logger()
	if env != "production" {
		output := zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}
		base = zerolog.New(output).With().Timestamp().Logger()
	}
	return base
}

func allowedOrigins(cfg *config.Config) []string {
	if len(cfg.CORSAllowedOrigins) == 0 {
		return []string{"*"}
	}
	return cfg.CORSAllowedOrigins
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
