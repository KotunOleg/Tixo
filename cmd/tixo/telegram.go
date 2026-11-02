package main

import (
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/KotunOleg/Tixo/internal/config"
	"github.com/KotunOleg/Tixo/internal/notify"
	"github.com/KotunOleg/Tixo/internal/web/i18n"
)

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
