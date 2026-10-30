package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"

	"github.com/KotunOleg/Tixo/internal/config"
	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/orders"
	"github.com/KotunOleg/Tixo/internal/platform/logger"
	"github.com/KotunOleg/Tixo/internal/platform/postgres"
	"github.com/KotunOleg/Tixo/internal/platform/rabbitmq"
	"github.com/KotunOleg/Tixo/internal/platform/storage"
	"github.com/KotunOleg/Tixo/internal/worker"
)

func init() {
	commands["worker"] = command{summary: "consume tickets.issue and render ticket PDFs", run: runWorker}
}

func runWorker(ctx context.Context, _ []string, _ io.Writer) error {
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

	store, err := storage.New(cfg.StorageDir)
	if err != nil {
		return err
	}

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

	tw := &worker.TicketWorker{Queue: orders.TopicTicketsIssue, Channel: ch, Queries: dbgen.New(pool), Storage: store, Log: log}
	log.Info("ticket worker starting", "queue", orders.TopicTicketsIssue)
	if err := tw.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
