package worker

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/platform/rabbitmq"
)

const outboxBatchSize = 100

type Relay struct {
	Pool    *pgxpool.Pool
	Queries *dbgen.Queries
	Channel *amqp.Channel
	Log     *slog.Logger
}

func (r *Relay) Tick(ctx context.Context) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("relay: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := r.Queries.WithTx(tx)

	rows, err := q.ClaimOutboxBatch(ctx, outboxBatchSize)
	if err != nil {
		return fmt.Errorf("relay: claim outbox batch: %w", err)
	}
	for _, row := range rows {
		if err := rabbitmq.Publish(ctx, r.Channel, rabbitmq.Exchange, row.Topic, row.Payload); err != nil {
			r.Log.Error("relay: publish outbox row failed, will retry next tick", "id", row.ID, "topic", row.Topic, "error", err)
			continue
		}
		if err := q.MarkOutboxPublished(ctx, row.ID); err != nil {
			return fmt.Errorf("relay: mark outbox %d published: %w", row.ID, err)
		}
	}
	return tx.Commit(ctx)
}
