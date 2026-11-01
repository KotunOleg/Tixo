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

	ReminderStaleAfter = time.Hour
)

type ReminderJob struct {
	UserID  int64  `json:"user_id"`
	EventID int64  `json:"event_id"`
	Kind    string `json:"kind"`
}

type EnqueueResult struct {
	Queued   int
	Requeued int
}

type Reminders struct {
	pool *pgxpool.Pool
	q    *dbgen.Queries
}

func NewReminders(pool *pgxpool.Pool) *Reminders {
	return &Reminders{pool: pool, q: dbgen.New(pool)}
}

func (r *Reminders) Enqueue(ctx context.Context, now time.Time, within time.Duration) (EnqueueResult, error) {
	if within <= 0 {
		return EnqueueResult{}, fmt.Errorf("reminder window must be positive, got %s", within)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return EnqueueResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := r.q.WithTx(tx)

	fresh, err := q.QueueEventReminders(ctx, dbgen.QueueEventRemindersParams{Kind: KindReminder24h, Now: now, Until: now.Add(within)})
	if err != nil {
		return EnqueueResult{}, fmt.Errorf("queue event reminders: %w", err)
	}
	stale, err := q.RequeueStaleReminders(ctx, dbgen.RequeueStaleRemindersParams{
		Kind: KindReminder24h, Now: now, StaleBefore: now.Add(-ReminderStaleAfter), Until: now.Add(within),
	})
	if err != nil {
		return EnqueueResult{}, fmt.Errorf("requeue stale reminders: %w", err)
	}

	jobs := make([]ReminderJob, 0, len(fresh)+len(stale))
	for _, row := range fresh {
		jobs = append(jobs, ReminderJob{UserID: row.UserID, EventID: row.EventID, Kind: row.Kind})
	}
	for _, row := range stale {
		jobs = append(jobs, ReminderJob{UserID: row.UserID, EventID: row.EventID, Kind: row.Kind})
	}
	for _, job := range jobs {
		payload, err := json.Marshal(job)
		if err != nil {
			return EnqueueResult{}, err
		}
		if _, err := q.InsertOutbox(ctx, dbgen.InsertOutboxParams{Topic: TopicTelegram, Payload: payload}); err != nil {
			return EnqueueResult{}, fmt.Errorf("insert reminder outbox: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return EnqueueResult{}, fmt.Errorf("commit reminders: %w", err)
	}
	return EnqueueResult{Queued: len(fresh), Requeued: len(stale)}, nil
}
