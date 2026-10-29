package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/KotunOleg/Tixo/internal/config"
	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/orders"
	"github.com/KotunOleg/Tixo/internal/platform/logger"
	"github.com/KotunOleg/Tixo/internal/platform/postgres"
	"github.com/KotunOleg/Tixo/internal/platform/rabbitmq"
	"github.com/KotunOleg/Tixo/internal/worker"
)

const outboxTickInterval = time.Second

func init() {
	commands["scheduler"] = command{summary: "relay queued outbox events to rabbitmq", run: runScheduler}
}

func runScheduler(ctx context.Context, _ []string, _ io.Writer) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := logger.New(os.Stdout, cfg.AppEnv)
	slog.SetDefault(log)

	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
	if err != nil {
		return err
	}
	defer pool.Close()

	conn, err := rabbitmq.Dial(ctx, cfg.AMQPURL)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	ch, err := rabbitmq.OpenConfirmChannel(conn)
	if err != nil {
		return err
	}
	defer func() { _ = ch.Close() }()

	if err := rabbitmq.DeclareTopology(ch, orders.TopicTicketsIssue); err != nil {
		return err
	}

	relay := &worker.Relay{Pool: pool, Queries: dbgen.New(pool), Channel: ch, Log: log}
	log.Info("outbox scheduler starting", "interval", outboxTickInterval.String())

	ticker := time.NewTicker(outboxTickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := relay.Tick(ctx); err != nil {
				log.Error("outbox tick failed", "error", err)
			}
		}
	}
}
