package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/KotunOleg/Tixo/internal/config"
	"github.com/KotunOleg/Tixo/internal/crm"
	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/orders"
	"github.com/KotunOleg/Tixo/internal/platform/logger"
	"github.com/KotunOleg/Tixo/internal/platform/postgres"
	"github.com/KotunOleg/Tixo/internal/platform/rabbitmq"
	"github.com/KotunOleg/Tixo/internal/platform/storage"
	"github.com/KotunOleg/Tixo/internal/worker"
)

func init() {
	commands["worker"] = command{summary: "consume an outbox queue (tickets.issue by default, or --queue=crm.contact)", run: runWorker}
}

func parseWorkerArgs(args []string) (string, error) {
	fs := flag.NewFlagSet("worker", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var queue string
	fs.StringVar(&queue, "queue", "", "queue to consume: tickets.issue (default) or crm.contact")
	if err := fs.Parse(args); err != nil {
		return "", fmt.Errorf("worker: %w", err)
	}
	if fs.NArg() > 0 {
		return "", fmt.Errorf("worker: unexpected %q", fs.Arg(0))
	}
	if queue == "" {
		queue = orders.TopicTicketsIssue
	}
	if queue != orders.TopicTicketsIssue && queue != crm.TopicCRMContact {
		return "", fmt.Errorf("worker: unknown --queue %q", queue)
	}
	return queue, nil
}

func runWorker(ctx context.Context, args []string, _ io.Writer) error {
	queue, err := parseWorkerArgs(args)
	if err != nil {
		return err
	}

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

	if err := rabbitmq.DeclareTopology(ch, queue); err != nil {
		return err
	}

	var run func(context.Context) error
	switch queue {
	case orders.TopicTicketsIssue:
		store, err := storage.New(cfg.StorageDir)
		if err != nil {
			return err
		}
		tw := &worker.TicketWorker{Queue: queue, Channel: ch, Queries: dbgen.New(pool), Storage: store, Log: log}
		run = tw.Run
	case crm.TopicCRMContact:
		client := crmClient(cfg, log)
		cw := &worker.CRMWorker{Queue: queue, Channel: ch, Queries: dbgen.New(pool), Client: client, Log: log}
		run = cw.Run
	}

	log.Info("worker starting", "queue", queue)
	if err := run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

func crmClient(cfg *config.Config, log *slog.Logger) crm.Client {
	if cfg.CRMDryRun {
		return &crm.DryRunClient{Log: log}
	}
	return crm.NewHubSpotClient(cfg.HubSpotAccessToken)
}
