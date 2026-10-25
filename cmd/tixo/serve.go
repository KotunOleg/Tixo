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

	"github.com/KotunOleg/Tixo/internal/config"
	"github.com/KotunOleg/Tixo/internal/health"
	"github.com/KotunOleg/Tixo/internal/platform/logger"
	"github.com/KotunOleg/Tixo/internal/server"
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
	pages, err := view.New(web.FS, tr, view.Config{BaseURL: cfg.BaseURL, Logger: log})
	if err != nil {
		return err
	}

	handler := server.New(server.Deps{
		Logger:      log,
		Health:      health.Handler(healthTimeout),
		Routes:      func(r chi.Router) { controllers.Mount(r, pages, web.FS) },
		NotFound:    pages.NotFound,
		ServerError: pages.ServerError,
	})

	ln, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.HTTPAddr, err)
	}
	return server.Serve(ctx, ln, handler, log, shutdownTimeout)
}
