package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	amqp "github.com/rabbitmq/amqp091-go"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/platform/rabbitmq"
	"github.com/KotunOleg/Tixo/internal/platform/storage"
	"github.com/KotunOleg/Tixo/internal/ticketpdf"
)

const (
	maxAttempts     = 5
	statusFulfilled = "fulfilled"
)

type TicketWorker struct {
	Queue   string
	Channel *amqp.Channel
	Queries *dbgen.Queries
	Storage storage.Store
	Log     *slog.Logger
}

func (w *TicketWorker) Run(ctx context.Context) error {
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

func (w *TicketWorker) handle(ctx context.Context, msg amqp.Delivery) {
	var payload struct {
		OrderID int64 `json:"order_id"`
	}
	if err := json.Unmarshal(msg.Body, &payload); err != nil {
		w.Log.Error("worker: decode tickets.issue payload", "error", err)
		if nackErr := msg.Nack(false, false); nackErr != nil {
			w.Log.Error("worker: nack malformed message", "error", nackErr)
		}
		return
	}

	if err := w.issue(ctx, payload.OrderID); err != nil {
		attempt := deathCount(msg, w.Queue) + 1
		if attempt >= maxAttempts {
			w.park(msg)
			w.Log.Error("worker: parked tickets.issue after max attempts",
				"order_id", payload.OrderID, "attempt", attempt, "error", err)
			if ackErr := msg.Ack(false); ackErr != nil {
				w.Log.Error("worker: ack parked message", "error", ackErr)
			}
			return
		}
		w.Log.Error("worker: tickets.issue failed, retrying",
			"order_id", payload.OrderID, "attempt", attempt, "error", err)
		if nackErr := msg.Nack(false, false); nackErr != nil {
			w.Log.Error("worker: nack failed message", "error", nackErr)
		}
		return
	}
	if err := msg.Ack(false); err != nil {
		w.Log.Error("worker: ack processed message", "error", err)
	}
}

func (w *TicketWorker) issue(ctx context.Context, orderID int64) error {
	order, err := w.Queries.GetOrderByID(ctx, orderID)
	if err != nil {
		return fmt.Errorf("get order %d: %w", orderID, err)
	}
	if order.Order.Status == statusFulfilled {
		return nil
	}

	tickets, err := w.Queries.ListTicketsForOrder(ctx, orderID)
	if err != nil {
		return fmt.Errorf("list tickets for order %d: %w", orderID, err)
	}
	for _, ticket := range tickets {
		if ticket.PdfKey.Valid {
			continue
		}
		pdf, err := ticketpdf.Render(ticketpdf.TicketInfo{
			EventTitle: order.EventTitle,
			VenueName:  order.VenueName,
			VenueCity:  order.VenueCity,
			TierName:   order.TierName,
			StartsAt:   order.EventStartsAt,
			Code:       uuid.UUID(ticket.Code.Bytes),
		})
		if err != nil {
			return fmt.Errorf("render pdf for ticket %d: %w", ticket.ID, err)
		}
		key, err := w.Storage.Save(bytes.NewReader(pdf), "pdf")
		if err != nil {
			return fmt.Errorf("save pdf for ticket %d: %w", ticket.ID, err)
		}
		if err := w.Queries.UpdateTicketPDFKey(ctx, dbgen.UpdateTicketPDFKeyParams{
			ID: ticket.ID, PdfKey: pgtype.Text{String: key, Valid: true},
		}); err != nil {
			return fmt.Errorf("update ticket %d pdf key: %w", ticket.ID, err)
		}
	}

	if err := w.Queries.MarkOrderFulfilled(ctx, orderID); err != nil {
		return fmt.Errorf("mark order %d fulfilled: %w", orderID, err)
	}
	return nil
}

func (w *TicketWorker) park(msg amqp.Delivery) {
	if err := rabbitmq.Publish(context.Background(), w.Channel, rabbitmq.Exchange, w.Queue+".parking", msg.Body); err != nil {
		w.Log.Error("worker: publish to parking queue", "error", err)
	}
}

func deathCount(msg amqp.Delivery, queue string) int {
	raw, ok := msg.Headers["x-death"]
	if !ok {
		return 0
	}
	entries, ok := raw.([]interface{})
	if !ok {
		return 0
	}
	for _, e := range entries {
		entry, ok := e.(amqp.Table)
		if !ok {
			continue
		}
		if q, _ := entry["queue"].(string); q != queue {
			continue
		}
		switch c := entry["count"].(type) {
		case int64:
			return int(c)
		case int32:
			return int(c)
		}
	}
	return 0
}
