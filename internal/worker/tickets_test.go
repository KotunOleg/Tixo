package worker

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/KotunOleg/Tixo/internal/db/dbtest"
	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/events"
	"github.com/KotunOleg/Tixo/internal/orders"
	"github.com/KotunOleg/Tixo/internal/organizers"
	"github.com/KotunOleg/Tixo/internal/platform/rabbitmq"
	"github.com/KotunOleg/Tixo/internal/platform/storage"
	"github.com/KotunOleg/Tixo/internal/tiers"
	"github.com/KotunOleg/Tixo/internal/venues"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func mustOrder(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	ctx := context.Background()
	q := dbgen.New(pool)

	user, err := q.CreateUser(ctx, dbgen.CreateUserParams{Email: "buyer@example.com", Name: "Buyer"})
	if err != nil {
		t.Fatal(err)
	}
	org, err := organizers.New(pool).Create(ctx, user.ID, organizers.CreateInput{Name: "Kyiv Live", Slug: "kyiv-live"})
	if err != nil {
		t.Fatal(err)
	}
	v, err := venues.New(pool).Create(ctx, org.ID, venues.Input{
		Name: "Arena", Address: "1 Test St", City: "Kyiv", Lat: 50.45, Lng: 30.52, Capacity: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(30 * 24 * time.Hour).Truncate(time.Second)
	ev, err := events.New(pool).Create(ctx, org.ID, events.Input{
		VenueID: v.ID, Slug: "big-show", Title: "Big Show", Genre: events.GenreRock, StartsAt: start, EndsAt: start.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	tier, err := tiers.New(pool).Create(ctx, org.ID, ev.ID, tiers.Input{Name: "Standard", Price: 50000, Quantity: 10})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := events.New(pool).SetStatus(ctx, org.ID, ev.ID, events.StatusPublished); err != nil {
		t.Fatal(err)
	}

	got, _, err := orders.New(pool).Purchase(ctx, user.ID, orders.Input{TierID: tier.ID, Quantity: 2, Key: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	return got.ID
}

func dialWorkerTest(t *testing.T) *amqp.Connection {
	t.Helper()
	url := os.Getenv("TEST_AMQP_URL")
	if url == "" {
		t.Skip("TEST_AMQP_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := rabbitmq.Dial(ctx, url)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func newWorkerFixture(t *testing.T) (*TicketWorker, *amqp.Channel, string, *pgxpool.Pool) {
	t.Helper()
	conn := dialWorkerTest(t)
	ch, err := rabbitmq.OpenConfirmChannel(conn)
	if err != nil {
		t.Fatalf("open confirm channel: %v", err)
	}
	t.Cleanup(func() { _ = ch.Close() })

	queue := "test.tickets.issue." + strings.ReplaceAll(t.Name(), "/", "_") + "." + uuid.NewString()
	if err := rabbitmq.DeclareTopology(ch, queue); err != nil {
		t.Fatalf("declare topology: %v", err)
	}
	t.Cleanup(func() {
		for _, q := range []string{queue, queue + ".retry", queue + ".parking"} {
			_, _ = ch.QueueDelete(q, false, false, false)
		}
	})

	store, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pool := dbtest.Pool(t)
	w := &TicketWorker{Queue: queue, Channel: ch, Queries: dbgen.New(pool), Storage: store, Log: testLogger()}
	return w, ch, queue, pool
}

func publishOrderID(t *testing.T, ch *amqp.Channel, queue string, orderID int64) {
	t.Helper()
	body, err := json.Marshal(struct {
		OrderID int64 `json:"order_id"`
	}{OrderID: orderID})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rabbitmq.Publish(ctx, ch, rabbitmq.Exchange, queue, body); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

func getDelivery(t *testing.T, ch *amqp.Channel, queue string) amqp.Delivery {
	t.Helper()
	msg, ok, err := ch.Get(queue, false)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !ok {
		t.Fatalf("no message on queue %s", queue)
	}
	return msg
}

func ticketPDFKeys(t *testing.T, q dbgen.Queries, orderID int64) []string {
	t.Helper()
	tickets, err := q.ListTicketsForOrder(context.Background(), orderID)
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]string, len(tickets))
	for i, tk := range tickets {
		if !tk.PdfKey.Valid {
			t.Fatalf("ticket %d has no pdf_key", tk.ID)
		}
		keys[i] = tk.PdfKey.String
	}
	return keys
}

func TestHandleFillsPDFKeysAndFulfillsOrder(t *testing.T) {
	w, ch, queue, pool := newWorkerFixture(t)
	orderID := mustOrder(t, pool)

	publishOrderID(t, ch, queue, orderID)
	msg := getDelivery(t, ch, queue)
	w.handle(t.Context(), msg)

	keys := ticketPDFKeys(t, *w.Queries, orderID)
	if len(keys) != 2 {
		t.Fatalf("tickets with pdf_key = %d, want 2", len(keys))
	}

	order, err := w.Queries.GetOrderByID(context.Background(), orderID)
	if err != nil {
		t.Fatal(err)
	}
	if order.Order.Status != "fulfilled" {
		t.Errorf("order status = %q, want fulfilled", order.Order.Status)
	}

	if _, ok, err := ch.Get(queue, true); err != nil || ok {
		t.Errorf("message not acked: ok=%v err=%v", ok, err)
	}
}

func TestHandleRedeliveryDoesNotRegeneratePDFOrDuplicateTicket(t *testing.T) {
	w, ch, queue, pool := newWorkerFixture(t)
	orderID := mustOrder(t, pool)

	publishOrderID(t, ch, queue, orderID)
	w.handle(t.Context(), getDelivery(t, ch, queue))
	firstKeys := ticketPDFKeys(t, *w.Queries, orderID)

	publishOrderID(t, ch, queue, orderID)
	w.handle(t.Context(), getDelivery(t, ch, queue))
	secondKeys := ticketPDFKeys(t, *w.Queries, orderID)

	if len(firstKeys) != len(secondKeys) {
		t.Fatalf("ticket count changed on redelivery: %d -> %d", len(firstKeys), len(secondKeys))
	}
	for i, k := range firstKeys {
		if secondKeys[i] != k {
			t.Errorf("ticket %d pdf_key regenerated on redelivery: %q -> %q", i, k, secondKeys[i])
		}
	}
}

func TestHandleParksMessageAfterMaxAttempts(t *testing.T) {
	w, ch, queue, _ := newWorkerFixture(t)

	publishOrderID(t, ch, queue, -1)
	msg := getDelivery(t, ch, queue)
	msg.Headers = amqp.Table{
		"x-death": []interface{}{
			amqp.Table{"queue": queue, "reason": "rejected", "count": int64(maxAttempts - 1)},
		},
	}

	w.handle(t.Context(), msg)

	if _, ok, err := ch.Get(queue, true); err != nil || ok {
		t.Errorf("message must leave the main queue once parked: ok=%v err=%v", ok, err)
	}
	parked, ok, err := ch.Get(queue+".parking", true)
	if err != nil {
		t.Fatalf("get parking: %v", err)
	}
	if !ok {
		t.Fatal("message was not parked after max attempts")
	}
	var payload struct {
		OrderID int64 `json:"order_id"`
	}
	if err := json.Unmarshal(parked.Body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.OrderID != -1 {
		t.Errorf("parked payload order_id = %d, want -1", payload.OrderID)
	}
}

func TestHandleRetriesBeforeMaxAttempts(t *testing.T) {
	w, ch, queue, _ := newWorkerFixture(t)

	publishOrderID(t, ch, queue, -1)
	msg := getDelivery(t, ch, queue)
	msg.Headers = amqp.Table{
		"x-death": []interface{}{
			amqp.Table{"queue": queue, "reason": "rejected", "count": int64(maxAttempts - 2)},
		},
	}

	w.handle(t.Context(), msg)

	if _, ok, err := ch.Get(queue+".parking", true); err != nil || ok {
		t.Error("message parked too early, before reaching max attempts")
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, ok, err := ch.Get(queue+".retry", true); err == nil && ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("rejected message never reached the retry queue")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
