package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/web/i18n"
)

const (
	SkipUnlinked = "unlinked"
	SkipStarted  = "started"
	SkipBlocked  = "blocked"
	SkipRejected = "rejected"

	reminderTimeLayout = "02.01.2006 15:04"
	publishedStatus    = "published"
)

var kyiv = mustLocation("Europe/Kyiv")

func mustLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

type Sender struct {
	q       *dbgen.Queries
	client  Client
	tr      *i18n.Translator
	baseURL string
	log     *slog.Logger
	now     func() time.Time
}

func NewSender(pool *pgxpool.Pool, client Client, tr *i18n.Translator, baseURL string, log *slog.Logger) *Sender {
	return &Sender{q: dbgen.New(pool), client: client, tr: tr, baseURL: strings.TrimSuffix(baseURL, "/"), log: log, now: time.Now}
}

func (s *Sender) Deliver(ctx context.Context, job ReminderJob) error {
	key := dbgen.GetEventReminderParams{UserID: job.UserID, EventID: job.EventID, Kind: job.Kind}
	r, err := s.q.GetEventReminder(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		s.log.WarnContext(ctx, "telegram: reminder row is gone, dropping the job", "user_id", job.UserID, "event_id", job.EventID)
		return nil
	}
	if err != nil {
		return fmt.Errorf("load reminder: %w", err)
	}
	if r.SentAt.Valid || r.SkippedReason.Valid {
		return nil
	}
	switch {
	case !r.ChatID.Valid:
		return s.skip(ctx, key, SkipUnlinked)
	case r.Status != publishedStatus:
		return s.skip(ctx, key, "event_"+r.Status)
	case !r.StartsAt.After(s.now()):
		return s.skip(ctx, key, SkipStarted)
	}

	text, err := s.text(r)
	if err != nil {
		return err
	}
	err = s.client.SendMessage(ctx, r.ChatID.Int64, text)
	var permanent *PermanentError
	switch {
	case errors.Is(err, ErrForbidden):
		if _, err := s.q.UnlinkTelegramChat(ctx, r.ChatID.Int64); err != nil {
			return fmt.Errorf("unlink blocked chat: %w", err)
		}
		return s.skip(ctx, key, SkipBlocked)
	case errors.As(err, &permanent):
		s.log.WarnContext(ctx, "telegram: reminder rejected", "user_id", job.UserID, "event_id", job.EventID, "error", err)
		return s.skip(ctx, key, SkipRejected)
	case err != nil:
		return err
	}
	if err := s.q.MarkNotificationSent(ctx, dbgen.MarkNotificationSentParams{
		SentAt: s.now(), UserID: job.UserID, EventID: job.EventID, Kind: job.Kind,
	}); err != nil {
		return fmt.Errorf("mark reminder sent: %w", err)
	}
	s.log.InfoContext(ctx, "telegram: reminder sent", "user_id", job.UserID, "event_id", job.EventID)
	return nil
}

func (s *Sender) skip(ctx context.Context, key dbgen.GetEventReminderParams, reason string) error {
	if err := s.q.MarkNotificationSkipped(ctx, dbgen.MarkNotificationSkippedParams{
		Reason: reason, UserID: key.UserID, EventID: key.EventID, Kind: key.Kind,
	}); err != nil {
		return fmt.Errorf("mark reminder skipped: %w", err)
	}
	s.log.InfoContext(ctx, "telegram: reminder skipped", "user_id", key.UserID, "event_id", key.EventID, "reason", reason)
	return nil
}

func (s *Sender) text(r dbgen.GetEventReminderRow) (string, error) {
	lang := i18n.Lang(r.Language)
	return s.tr.T(lang, "telegram.reminder",
		"Title", r.Title,
		"When", r.StartsAt.In(kyiv).Format(reminderTimeLayout),
		"Venue", r.VenueName,
		"City", r.VenueCity,
		"URL", s.baseURL+i18n.Path(lang, "/tickets"),
	)
}
