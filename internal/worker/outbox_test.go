package worker

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/KotunOleg/Tixo/internal/db/dbtest"
	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/platform/rabbitmq"
)

func newRelayFixture(t *testing.T) (*Relay, *amqp.Channel, *dbgen.Queries, string) {
	t.Helper()
	conn := dialWorkerTest(t)
	ch, err := rabbitmq.OpenConfirmChannel(conn)
	if err != nil {
		t.Fatalf("open confirm channel: %v", err)
	}
	t.Cleanup(func() { _ = ch.Close() })

	topic := "test.outbox." + strings.ReplaceAll(t.Name(), "/", "_") + "." + uuid.NewString()
	if err := rabbitmq.DeclareTopology(ch, topic); err != nil {
		t.Fatalf("declare topology: %v", err)
	}
	t.Cleanup(func() {
		for _, q := range []string{topic, topic + ".retry", topic + ".parking"} {
			_, _ = ch.QueueDelete(q, false, false, false)
		}
	})

	q := dbgen.New(dbtest.Pool(t))
	return &Relay{Queries: q, Channel: ch, Log: testLogger()}, ch, q, topic
}

func TestTickPublishesAndMarksAnOutboxRow(t *testing.T) {
	relay, ch, q, topic := newRelayFixture(t)
	ctx := t.Context()

	payload := json.RawMessage(`{"order_id":1}`)
	if _, err := q.InsertOutbox(ctx, dbgen.InsertOutboxParams{Topic: topic, Payload: payload}); err != nil {
		t.Fatal(err)
	}

	if err := relay.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	rows, err := q.ClaimOutboxBatch(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("unpublished outbox rows after Tick = %d, want 0", len(rows))
	}

	msg, ok, err := ch.Get(topic, true)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !ok {
		t.Fatal("outbox row was not published to the queue")
	}
	var got struct {
		OrderID int64 `json:"order_id"`
	}
	if err := json.Unmarshal(msg.Body, &got); err != nil {
		t.Fatalf("unmarshal published body: %v", err)
	}
	if got.OrderID != 1 {
		t.Errorf("published order_id = %d, want 1", got.OrderID)
	}
}

func TestTickOnEmptyOutboxIsNoop(t *testing.T) {
	relay, ch, _, topic := newRelayFixture(t)

	if err := relay.Tick(t.Context()); err != nil {
		t.Fatalf("Tick on empty outbox: %v", err)
	}

	if _, ok, err := ch.Get(topic, true); err != nil || ok {
		t.Errorf("Tick on empty outbox published something: ok=%v err=%v", ok, err)
	}
}

func TestTickIsIdempotentAcrossCalls(t *testing.T) {
	relay, ch, q, topic := newRelayFixture(t)
	ctx := t.Context()

	if _, err := q.InsertOutbox(ctx, dbgen.InsertOutboxParams{Topic: topic, Payload: json.RawMessage(`{"order_id":2}`)}); err != nil {
		t.Fatal(err)
	}

	if err := relay.Tick(ctx); err != nil {
		t.Fatalf("first Tick: %v", err)
	}
	if _, ok, err := ch.Get(topic, true); err != nil || !ok {
		t.Fatalf("first Tick did not publish: ok=%v err=%v", ok, err)
	}

	if err := relay.Tick(ctx); err != nil {
		t.Fatalf("second Tick: %v", err)
	}
	if _, ok, err := ch.Get(topic, true); err != nil || ok {
		t.Error("second Tick republished an already-published row")
	}
}
