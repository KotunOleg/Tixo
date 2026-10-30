package worker

import (
	"context"
	"fmt"
	"log/slog"

	amqp "github.com/rabbitmq/amqp091-go"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/platform/rabbitmq"
)

const outboxBatchSize = 100

type Relay struct {
	Queries *dbgen.Queries
	Channel *amqp.Channel
	Log     *slog.Logger
}

func (r *Relay) Tick(ctx context.Context) error {
	rows, err := r.Queries.ClaimOutboxBatch(ctx, outboxBatchSize)
	if err != nil {
		return fmt.Errorf("relay: claim outbox batch: %w", err)
	}
	for _, row := range rows {
		if err := rabbitmq.Publish(ctx, r.Channel, rabbitmq.Exchange, row.Topic, row.Payload); err != nil {
			r.Log.Error("relay: publish outbox row failed, will retry next tick", "id", row.ID, "topic", row.Topic, "error", err)
			continue
		}
		if err := r.Queries.MarkOutboxPublished(ctx, row.ID); err != nil {
			return fmt.Errorf("relay: mark outbox %d published: %w", row.ID, err)
		}
	}
	return nil
}
