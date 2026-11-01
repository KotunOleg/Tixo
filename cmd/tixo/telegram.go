package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/KotunOleg/Tixo/internal/config"
	"github.com/KotunOleg/Tixo/internal/notify"
	"github.com/KotunOleg/Tixo/internal/platform/logger"
	"github.com/KotunOleg/Tixo/internal/platform/postgres"
	"github.com/KotunOleg/Tixo/internal/web/i18n"
)

func init() {
	commands["telegram-remind"] = command{summary: "queue telegram reminders for events starting within --within (default 24h)", run: runTelegramRemind}
}

func telegramClient(cfg *config.Config, log *slog.Logger) notify.Client {
	if cfg.TelegramDryRun {
		return &notify.DryRunClient{Log: log}
	}
	return notify.NewBotAPIClient(cfg.TelegramBotToken)
}

func telegramBot(cfg *config.Config, pool *pgxpool.Pool, tr *i18n.Translator, log *slog.Logger) *notify.Bot {
	if !cfg.TelegramEnabled() {
		log.Warn("telegram bot disabled: TELEGRAM_BOT_USERNAME is not set")
		return nil
	}
	return notify.NewBot(pool, telegramClient(cfg, log), tr, notify.BotConfig{BaseURL: cfg.BaseURL.String(), Username: cfg.TelegramBotUsername}, log)
}

func parseRemindArgs(args []string) (time.Duration, error) {
	fs := flag.NewFlagSet("telegram-remind", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	within := fs.Duration("within", notify.ReminderWindow, "queue reminders for events starting within this window")
	if err := fs.Parse(args); err != nil {
		return 0, fmt.Errorf("telegram-remind: %w", err)
	}
	if fs.NArg() > 0 {
		return 0, fmt.Errorf("telegram-remind: unexpected %q", fs.Arg(0))
	}
	if *within <= 0 {
		return 0, fmt.Errorf("telegram-remind: --within must be positive, got %s", *within)
	}
	return *within, nil
}

func runTelegramRemind(ctx context.Context, args []string, stdout io.Writer) error {
	within, err := parseRemindArgs(args)
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	slog.SetDefault(logger.New(os.Stdout, cfg.AppEnv))
	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
	if err != nil {
		return err
	}
	defer pool.Close()

	n, err := notify.NewReminders(pool).Enqueue(ctx, time.Now(), within)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "queued %d telegram reminders for events starting within %s\n", n, within)
	return err
}
