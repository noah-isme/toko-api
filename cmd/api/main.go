package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/hlog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/noah-isme/backend-toko/internal/auth"
	"github.com/noah-isme/backend-toko/internal/common"
	"github.com/noah-isme/backend-toko/internal/config"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/user"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}

	logger := newLogger(cfg.AppEnv)

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
