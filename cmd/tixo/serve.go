package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"time"

	"github.com/KotunOleg/Tixo/internal/config"
	"github.com/KotunOleg/Tixo/internal/health"
	"github.com/KotunOleg/Tixo/internal/platform/logger"
	"github.com/KotunOleg/Tixo/internal/server"
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

	handler := server.New(server.Deps{
		Logger: log,
		Health: health.Handler(healthTimeout),
	})

	ln, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.HTTPAddr, err)
	}
	return server.Serve(ctx, ln, handler, log, shutdownTimeout)
}
