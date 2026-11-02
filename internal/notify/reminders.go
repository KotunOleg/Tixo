package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
)

const (
	TopicTelegram   = "notify.telegram"
	KindReminder24h = "reminder_24h"
	ReminderWindow  = 24 * time.Hour
)

type ReminderJob struct {
	UserID  int64  `json:"user_id"`
	EventID int64  `json:"event_id"`
	Kind    string `json:"kind"`
}

type Reminders struct {
	pool *pgxpool.Pool
	q    *dbgen.Queries
}

func NewReminders(pool *pgxpool.Pool) *Reminders {
	return &Reminders{pool: pool, q: dbgen.New(pool)}
}

func (r *Reminders) Enqueue(ctx context.Context, now time.Time, within time.Duration) (int, error) {
	if within <= 0 {
		return 0, fmt.Errorf("reminder window must be positive, got %s", within)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := r.q.WithTx(tx)

	rows, err := q.QueueEventReminders(ctx, dbgen.QueueEventRemindersParams{Kind: KindReminder24h, Now: now, Until: now.Add(within)})
	if err != nil {
		return 0, fmt.Errorf("queue event reminders: %w", err)
	}
	for _, row := range rows {
		payload, err := json.Marshal(ReminderJob{UserID: row.UserID, EventID: row.EventID, Kind: row.Kind})
		if err != nil {
			return 0, err
		}
		if _, err := q.InsertOutbox(ctx, dbgen.InsertOutboxParams{Topic: TopicTelegram, Payload: payload}); err != nil {
			return 0, fmt.Errorf("insert reminder outbox: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit reminders: %w", err)
	}
	return len(rows), nil
}
