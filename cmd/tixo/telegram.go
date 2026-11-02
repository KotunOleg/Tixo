package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/KotunOleg/Tixo/internal/config"
	"github.com/KotunOleg/Tixo/internal/notify"
	"github.com/KotunOleg/Tixo/internal/platform/logger"
	"github.com/KotunOleg/Tixo/internal/platform/postgres"
	"github.com/KotunOleg/Tixo/internal/web/controllers"
	"github.com/KotunOleg/Tixo/internal/web/i18n"
	"github.com/KotunOleg/Tixo/web"
)

const telegramPollWait = 30 * time.Second

func init() {
	commands["telegram-remind"] = command{summary: "queue telegram reminders for events starting within --within (default 24h)", run: runTelegramRemind}
	commands["telegram-poll"] = command{summary: "answer the telegram bot by long polling (local dev instead of the webhook)", run: runTelegramPoll}
	commands["telegram-set-webhook"] = command{summary: "point the telegram bot at BASE_URL/tg/webhook, or remove the webhook with --delete", run: runTelegramSetWebhook}
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

	res, err := notify.NewReminders(pool).Enqueue(ctx, time.Now(), within)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "queued %d telegram reminders and requeued %d stuck ones for events starting within %s\n", res.Queued, res.Requeued, within)
	return err
}

func runTelegramPoll(ctx context.Context, args []string, _ io.Writer) error {
	if len(args) > 0 {
		return fmt.Errorf("telegram-poll: unexpected %q", args[0])
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := logger.New(os.Stdout, cfg.AppEnv)
	slog.SetDefault(log)
	if !cfg.TelegramEnabled() {
		return errors.New("telegram-poll: TELEGRAM_BOT_USERNAME is not set")
	}
	tr, err := i18n.New(web.FS)
	if err != nil {
		return err
	}
	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
	if err != nil {
		return err
	}
	defer pool.Close()

	client := telegramClient(cfg, log)
	bot := notify.NewBot(pool, client, tr, notify.BotConfig{BaseURL: cfg.BaseURL.String(), Username: cfg.TelegramBotUsername}, log)
	log.Info("telegram-poll: waiting for updates", "bot", cfg.TelegramBotUsername, "dry_run", cfg.TelegramDryRun)
	if err := notify.Poll(ctx, client, bot, telegramPollWait, log); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

func parseSetWebhookArgs(args []string) (bool, error) {
	fs := flag.NewFlagSet("telegram-set-webhook", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	remove := fs.Bool("delete", false, "remove the webhook so telegram-poll can run")
	if err := fs.Parse(args); err != nil {
		return false, fmt.Errorf("telegram-set-webhook: %w", err)
	}
	if fs.NArg() > 0 {
		return false, fmt.Errorf("telegram-set-webhook: unexpected %q", fs.Arg(0))
	}
	return *remove, nil
}

func telegramWebhookURL(cfg *config.Config) (string, error) {
	if cfg.BaseURL.Scheme != "https" {
		return "", fmt.Errorf("telegram-set-webhook: telegram only calls https webhooks, BASE_URL is %s", cfg.BaseURL.String())
	}
	if cfg.TelegramWebhookSecret == "" {
		return "", errors.New("telegram-set-webhook: TELEGRAM_WEBHOOK_SECRET is not set")
	}
	return strings.TrimSuffix(cfg.BaseURL.String(), "/") + controllers.TelegramWebhookPath, nil
}

func runTelegramSetWebhook(ctx context.Context, args []string, stdout io.Writer) error {
	remove, err := parseSetWebhookArgs(args)
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := logger.New(os.Stdout, cfg.AppEnv)
	client := telegramClient(cfg, log)
	if remove {
		if err := client.SetWebhook(ctx, "", ""); err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, "telegram webhook removed")
		return err
	}
	webhookURL, err := telegramWebhookURL(cfg)
	if err != nil {
		return err
	}
	if err := client.SetWebhook(ctx, webhookURL, cfg.TelegramWebhookSecret); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "telegram webhook set to %s (dry run: %v)\n", webhookURL, cfg.TelegramDryRun)
	return err
}
