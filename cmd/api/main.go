package main

import (
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/hlog"

	"github.com/noah-isme/backend-toko/internal/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}

	logger := newLogger(cfg.AppEnv)

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
