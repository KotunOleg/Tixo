package notify

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/web/i18n"
)

const (
	LinkTokenTTL = 15 * time.Minute

	linkTokenBytes = 32
	privateChat    = "private"
)

type BotConfig struct {
	BaseURL  string
	Username string
}

type Bot struct {
	pool   *pgxpool.Pool
	q      *dbgen.Queries
	client Client
	tr     *i18n.Translator
	cfg    BotConfig
	log    *slog.Logger
	now    func() time.Time
}

func NewBot(pool *pgxpool.Pool, client Client, tr *i18n.Translator, cfg BotConfig, log *slog.Logger) *Bot {
	return &Bot{pool: pool, q: dbgen.New(pool), client: client, tr: tr, cfg: cfg, log: log, now: time.Now}
}

func (b *Bot) IssueLink(ctx context.Context, userID int64) (link string, expiresAt time.Time, err error) {
	raw := make([]byte, linkTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, fmt.Errorf("telegram link token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	expiresAt = b.now().Add(LinkTokenTTL)
	if err := b.q.IssueTelegramLinkToken(ctx, dbgen.IssueTelegramLinkTokenParams{
		UserID: userID, TokenHash: hashToken(token), ExpiresAt: expiresAt,
	}); err != nil {
		return "", time.Time{}, fmt.Errorf("issue telegram link token: %w", err)
	}
	return "https://t.me/" + b.cfg.Username + "?start=" + url.QueryEscape(token), expiresAt, nil
}

func (b *Bot) HandleUpdate(ctx context.Context, u Update) error {
	m := u.Message
	if m == nil || m.Chat.Type != privateChat {
		return nil
	}
	lang := chatLanguage(m.From)
	command, arg := parseCommand(m.Text)

	var reply string
	var err error
	switch {
	case command == "/start" && arg != "":
		reply, err = b.link(ctx, m.Chat.ID, lang, arg)
	case command == "/stop":
		reply, err = b.stop(ctx, m.Chat.ID, lang)
	default:
		reply, err = b.text(lang, "telegram.bot.help")
	}
	if err != nil {
		return err
	}
	if err := b.client.SendMessage(ctx, m.Chat.ID, reply); err != nil {
		b.log.WarnContext(ctx, "telegram: reply failed", "update_id", u.UpdateID, "error", err)
	}
	return nil
}

func (b *Bot) link(ctx context.Context, chatID int64, lang i18n.Lang, token string) (string, error) {
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := b.q.WithTx(tx)

	userID, err := q.ConsumeTelegramLinkToken(ctx, dbgen.ConsumeTelegramLinkTokenParams{TokenHash: hashToken(token), Now: b.now()})
	if errors.Is(err, pgx.ErrNoRows) {
		return b.text(lang, "telegram.bot.expired")
	}
	if err != nil {
		return "", fmt.Errorf("consume telegram link token: %w", err)
	}
	if err := q.ReleaseTelegramChat(ctx, chatID); err != nil {
		return "", fmt.Errorf("release telegram chat: %w", err)
	}
	if err := q.LinkTelegramChat(ctx, dbgen.LinkTelegramChatParams{
		UserID: userID, ChatID: chatID, Language: string(lang), LinkedAt: b.now(),
	}); err != nil {
		return "", fmt.Errorf("link telegram chat: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("link telegram chat: %w", err)
	}
	b.log.InfoContext(ctx, "telegram: chat linked", "user_id", userID)
	return b.text(lang, "telegram.bot.linked")
}

func (b *Bot) stop(ctx context.Context, chatID int64, lang i18n.Lang) (string, error) {
	if _, err := b.q.UnlinkTelegramChat(ctx, chatID); err != nil {
		return "", fmt.Errorf("unlink telegram chat: %w", err)
	}
	return b.text(lang, "telegram.bot.stopped")
}

func (b *Bot) text(lang i18n.Lang, id string) (string, error) {
	return b.tr.T(lang, id, "URL", strings.TrimSuffix(b.cfg.BaseURL, "/")+i18n.Path(lang, "/profile"))
}

func chatLanguage(from *User) i18n.Lang {
	if from != nil && strings.HasPrefix(strings.ToLower(from.LanguageCode), "uk") {
		return i18n.UK
	}
	return i18n.EN
}

func parseCommand(text string) (command, arg string) {
	fields := strings.Fields(text)
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "/") {
		return "", ""
	}
	command, _, _ = strings.Cut(strings.ToLower(fields[0]), "@")
	if len(fields) > 1 {
		arg = fields[1]
	}
	return command, arg
}

func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
