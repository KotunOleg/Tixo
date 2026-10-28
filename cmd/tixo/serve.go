package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/KotunOleg/Tixo/internal/analytics"
	"github.com/KotunOleg/Tixo/internal/api"
	"github.com/KotunOleg/Tixo/internal/apitokens"
	"github.com/KotunOleg/Tixo/internal/artists"
	"github.com/KotunOleg/Tixo/internal/auth"
	"github.com/KotunOleg/Tixo/internal/config"
	"github.com/KotunOleg/Tixo/internal/events"
	"github.com/KotunOleg/Tixo/internal/features"
	"github.com/KotunOleg/Tixo/internal/health"
	"github.com/KotunOleg/Tixo/internal/lineup"
	"github.com/KotunOleg/Tixo/internal/orders"
	"github.com/KotunOleg/Tixo/internal/organizers"
	"github.com/KotunOleg/Tixo/internal/platform/logger"
	"github.com/KotunOleg/Tixo/internal/platform/postgres"
	"github.com/KotunOleg/Tixo/internal/platform/rabbitmq"
	"github.com/KotunOleg/Tixo/internal/platform/redisclient"
	"github.com/KotunOleg/Tixo/internal/platform/storage"
	"github.com/KotunOleg/Tixo/internal/server"
	"github.com/KotunOleg/Tixo/internal/tiers"
	"github.com/KotunOleg/Tixo/internal/users"
	"github.com/KotunOleg/Tixo/internal/venues"
	"github.com/KotunOleg/Tixo/internal/web/controllers"
	"github.com/KotunOleg/Tixo/internal/web/i18n"
	"github.com/KotunOleg/Tixo/internal/web/view"
	"github.com/KotunOleg/Tixo/web"
)

const (
	healthTimeout   = 2 * time.Second
	shutdownTimeout = 10 * time.Second
)

func init() {
	commands["serve"] = command{summary: "run the web server", run: runServe}
}

func runServe(ctx context.Context, _ []string, _ io.Writer) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := logger.New(os.Stdout, cfg.AppEnv)
	slog.SetDefault(log)

	tr, err := i18n.New(web.FS)
	if err != nil {
		return err
	}
	pages, err := view.New(web.FS, tr, view.Config{BaseURL: cfg.BaseURL, Logger: log, SignIn: cfg.GoogleEnabled(), GA4ID: cfg.GA4MeasurementID})
	if err != nil {
		return err
	}

	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
	if err != nil {
		return err
	}
	defer pool.Close()

	posters, err := storage.New(cfg.StorageDir)
	if err != nil {
		return err
	}

	rdb, err := redisclient.New(cfg.RedisURL)
	if err != nil {
		return err
	}
	defer func() { _ = rdb.Close() }()

	sessions := auth.NewSessions(rdb, cfg.BaseURL, log, pages.ServerError)
	var google *auth.Google
	if cfg.GoogleEnabled() {
		google = auth.NewGoogle(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.BaseURL)
	} else {
		log.Warn("google sign-in disabled: GOOGLE_CLIENT_ID is not set")
	}
	if !cfg.GA4Enabled() {
		log.Warn("ga4 analytics disabled: GA4_MEASUREMENT_ID is not set")
	}

	orgs, ven, evs, trs, lup, arts, tokens := organizers.New(pool), venues.New(pool), events.New(pool), tiers.New(pool), lineup.New(pool), artists.New(pool), apitokens.New(pool)
	ords := orders.New(pool)
	flags := features.New(pool)
	stats := analytics.New(pool, flags)
	site := controllers.Deps{
		View: pages, FS: web.FS, Sessions: sessions, Google: google, Users: users.New(pool), Organizers: orgs, Venues: ven, Events: evs, Tiers: trs, Lineup: lup, Artists: arts, Orders: ords, Features: flags, Analytics: stats, APITokens: tokens, Storage: posters, Logger: log,
	}
	apiDeps := api.Deps{Organizers: orgs, Venues: ven, Events: evs, Tiers: trs, Lineup: lup, Artists: arts, Orders: ords, Analytics: stats, APITokens: tokens, Logger: log}
	handler := server.New(server.Deps{
		Logger: log,
		Health: health.Handler(version, healthTimeout, postgres.Checker(pool), redisclient.Checker(rdb), rabbitmq.Checker(cfg.AMQPURL)),
		Routes: func(r chi.Router) {
			controllers.Mount(r, site)
			api.Mount(r, apiDeps)
		},
		NotFound:    site.NotFound(),
		ServerError: pages.ServerError,
	})

	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.HTTPAddr, err)
	}
	return server.Serve(ctx, ln, handler, log, shutdownTimeout)
}
