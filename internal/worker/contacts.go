package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/KotunOleg/Tixo/internal/crm"
	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/platform/rabbitmq"
)

type CRMWorker struct {
	Queue   string
	Channel *amqp.Channel
	Queries *dbgen.Queries
	Client  crm.Client
	Log     *slog.Logger
}

func (w *CRMWorker) Run(ctx context.Context) error {
	if err := w.Channel.Qos(1, 0, false); err != nil {
		return fmt.Errorf("worker: set qos: %w", err)
	}
	msgs, err := w.Channel.ConsumeWithContext(ctx, w.Queue, "", false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("worker: consume %s: %w", w.Queue, err)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-msgs:
			if !ok {
				return nil
			}
			w.handle(ctx, msg)
		}
	}
}

func (w *CRMWorker) handle(ctx context.Context, msg amqp.Delivery) {
	var event crm.ContactEvent
	if err := json.Unmarshal(msg.Body, &event); err != nil {
		w.Log.Error("worker: decode crm.contact payload", "error", err)
		if nackErr := msg.Nack(false, false); nackErr != nil {
			w.Log.Error("worker: nack malformed message", "error", nackErr)
		}
		return
	}

	if err := w.upsert(ctx, event); err != nil {
		attempt := deathCount(msg, w.Queue) + 1
		if attempt >= maxAttempts {
			if parkErr := w.park(msg); parkErr != nil {
				w.Log.Error("worker: parking crm.contact failed, retrying instead of dropping it",
					"user_id", event.UserID, "attempt", attempt, "error", parkErr)
				if nackErr := msg.Nack(false, false); nackErr != nil {
					w.Log.Error("worker: nack message that failed to park", "error", nackErr)
				}
				return
			}
			w.Log.Error("worker: parked crm.contact after max attempts",
				"user_id", event.UserID, "attempt", attempt, "error", err)
			if ackErr := msg.Ack(false); ackErr != nil {
				w.Log.Error("worker: ack parked message", "error", ackErr)
			}
			return
		}
		w.Log.Error("worker: crm.contact failed, retrying",
			"user_id", event.UserID, "attempt", attempt, "error", err)
		if nackErr := msg.Nack(false, false); nackErr != nil {
			w.Log.Error("worker: nack failed message", "error", nackErr)
		}
		return
	}
	if err := msg.Ack(false); err != nil {
		w.Log.Error("worker: ack processed message", "error", err)
	}
}

func (w *CRMWorker) upsert(ctx context.Context, event crm.ContactEvent) error {
	user, err := w.Queries.GetUser(ctx, event.UserID)
	if err != nil {
		return fmt.Errorf("get user %d: %w", event.UserID, err)
	}
	return w.Client.UpsertContact(ctx, crm.Contact{Email: user.Email, Name: user.Name, Role: event.Kind})
}

func (w *CRMWorker) park(msg amqp.Delivery) error {
	return rabbitmq.Publish(context.Background(), w.Channel, rabbitmq.Exchange, w.Queue+".parking", msg.Body)
}
