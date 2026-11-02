package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/KotunOleg/Tixo/internal/notify"
	"github.com/KotunOleg/Tixo/internal/platform/rabbitmq"
)

type ReminderDeliverer interface {
	Deliver(ctx context.Context, job notify.ReminderJob) error
}

type TelegramWorker struct {
	Queue   string
	Channel *amqp.Channel
	Sender  ReminderDeliverer
	Log     *slog.Logger
}

func (w *TelegramWorker) Run(ctx context.Context) error {
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

func (w *TelegramWorker) handle(ctx context.Context, msg amqp.Delivery) {
	var job notify.ReminderJob
	if err := json.Unmarshal(msg.Body, &job); err != nil {
		w.Log.Error("worker: decode notify.telegram payload", "error", err)
		if nackErr := msg.Nack(false, false); nackErr != nil {
			w.Log.Error("worker: nack malformed message", "error", nackErr)
		}
		return
	}

	if err := w.Sender.Deliver(ctx, job); err != nil {
		attempt := deathCount(msg, w.Queue) + 1
		var rateLimited *notify.RetryAfterError
		if errors.As(err, &rateLimited) {
			w.Log.Warn("worker: telegram rate limit", "retry_after", rateLimited.After.String())
		}
		if attempt >= maxAttempts {
			if parkErr := w.park(msg); parkErr != nil {
				w.Log.Error("worker: parking notify.telegram failed, retrying instead of dropping it",
					"user_id", job.UserID, "event_id", job.EventID, "attempt", attempt, "error", parkErr)
				if nackErr := msg.Nack(false, false); nackErr != nil {
					w.Log.Error("worker: nack message that failed to park", "error", nackErr)
				}
				return
			}
			w.Log.Error("worker: parked notify.telegram after max attempts",
				"user_id", job.UserID, "event_id", job.EventID, "attempt", attempt, "error", err)
			if ackErr := msg.Ack(false); ackErr != nil {
				w.Log.Error("worker: ack parked message", "error", ackErr)
			}
			return
		}
		w.Log.Error("worker: notify.telegram failed, retrying",
			"user_id", job.UserID, "event_id", job.EventID, "attempt", attempt, "error", err)
		if nackErr := msg.Nack(false, false); nackErr != nil {
			w.Log.Error("worker: nack failed message", "error", nackErr)
		}
		return
	}
	if err := msg.Ack(false); err != nil {
		w.Log.Error("worker: ack processed message", "error", err)
	}
}

func (w *TelegramWorker) park(msg amqp.Delivery) error {
	return rabbitmq.Publish(context.Background(), w.Channel, rabbitmq.Exchange, w.Queue+".parking", msg.Body)
}
