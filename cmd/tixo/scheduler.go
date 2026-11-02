package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/KotunOleg/Tixo/internal/config"
	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/notify"
	"github.com/KotunOleg/Tixo/internal/platform/logger"
	"github.com/KotunOleg/Tixo/internal/platform/postgres"
	"github.com/KotunOleg/Tixo/internal/platform/rabbitmq"
	"github.com/KotunOleg/Tixo/internal/worker"
)

const (
	outboxTickInterval   = time.Second
	reminderTickInterval = time.Hour
)

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

	if err := declareWorkerQueues(ch); err != nil {
		return err
	}

	relay := &worker.Relay{Pool: pool, Queries: dbgen.New(pool), Channel: ch, Log: log}
	reminders := notify.NewReminders(pool)
	log.Info("outbox scheduler starting", "interval", outboxTickInterval.String(), "reminders_every", reminderTickInterval.String())

	enqueueReminders(ctx, reminders, log)
	ticker := time.NewTicker(outboxTickInterval)
	defer ticker.Stop()
	reminderTicker := time.NewTicker(reminderTickInterval)
	defer reminderTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := relay.Tick(ctx); err != nil {
				log.Error("outbox tick failed", "error", err)
			}
		case <-reminderTicker.C:
			enqueueReminders(ctx, reminders, log)
		}
	}
}

func enqueueReminders(ctx context.Context, r *notify.Reminders, log *slog.Logger) {
	n, err := r.Enqueue(ctx, time.Now(), notify.ReminderWindow)
	if err != nil {
		log.Error("telegram reminders tick failed", "error", err)
		return
	}
	log.Info("telegram reminders queued", "count", n)
}
