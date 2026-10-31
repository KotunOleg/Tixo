package worker

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/KotunOleg/Tixo/internal/crm"
	"github.com/KotunOleg/Tixo/internal/db/dbtest"
	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/platform/rabbitmq"
)

type fakeCRMClient struct {
	calls []crm.Contact
	err   error
}

func (f *fakeCRMClient) UpsertContact(_ context.Context, c crm.Contact) error {
	f.calls = append(f.calls, c)
	return f.err
}

func mustCRMUser(t *testing.T, pool *pgxpool.Pool, email, name string) int64 {
	t.Helper()
	u, err := dbgen.New(pool).CreateUser(context.Background(), dbgen.CreateUserParams{Email: email, Name: name})
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

func newCRMWorkerFixture(t *testing.T) (*CRMWorker, *fakeCRMClient, *amqp.Channel, string, *pgxpool.Pool) {
	t.Helper()
	conn := dialWorkerTest(t)
	ch, err := rabbitmq.OpenConfirmChannel(conn)
	if err != nil {
		t.Fatalf("open confirm channel: %v", err)
	}
	t.Cleanup(func() { _ = ch.Close() })

	queue := "test.crm.contact." + strings.ReplaceAll(t.Name(), "/", "_") + "." + uuid.NewString()
	if err := rabbitmq.DeclareTopology(ch, queue); err != nil {
		t.Fatalf("declare topology: %v", err)
	}
	t.Cleanup(func() {
		for _, q := range []string{queue, queue + ".retry", queue + ".parking"} {
			_, _ = ch.QueueDelete(q, false, false, false)
		}
	})

	pool := dbtest.Pool(t)
	client := &fakeCRMClient{}
	w := &CRMWorker{Queue: queue, Channel: ch, Queries: dbgen.New(pool), Client: client, Log: testLogger()}
	return w, client, ch, queue, pool
}

func publishContactEvent(t *testing.T, ch *amqp.Channel, queue string, event crm.ContactEvent) {
	t.Helper()
	body, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rabbitmq.Publish(ctx, ch, rabbitmq.Exchange, queue, body); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

func TestCRMHandleUpsertsContactOnceAndAcks(t *testing.T) {
	w, client, ch, queue, pool := newCRMWorkerFixture(t)
	userID := mustCRMUser(t, pool, "buyer@example.com", "Buyer")

	publishContactEvent(t, ch, queue, crm.ContactEvent{UserID: userID, Kind: crm.RoleBuyer})
	w.handle(t.Context(), getDelivery(t, ch, queue))

	if len(client.calls) != 1 {
		t.Fatalf("UpsertContact calls = %d, want 1", len(client.calls))
	}
	if got := client.calls[0]; got.Email != "buyer@example.com" || got.Name != "Buyer" || got.Role != crm.RoleBuyer {
		t.Errorf("upserted contact = %+v", got)
	}
	if _, ok, err := ch.Get(queue, true); err != nil || ok {
		t.Errorf("message not acked: ok=%v err=%v", ok, err)
	}
}

func TestCRMHandleRedeliveryUpsertsAgainWithoutError(t *testing.T) {
	w, client, ch, queue, pool := newCRMWorkerFixture(t)
	userID := mustCRMUser(t, pool, "buyer@example.com", "Buyer")

	publishContactEvent(t, ch, queue, crm.ContactEvent{UserID: userID, Kind: crm.RoleBuyer})
	w.handle(t.Context(), getDelivery(t, ch, queue))

	publishContactEvent(t, ch, queue, crm.ContactEvent{UserID: userID, Kind: crm.RoleBuyer})
	w.handle(t.Context(), getDelivery(t, ch, queue))

	if len(client.calls) != 2 {
		t.Fatalf("UpsertContact calls = %d, want 2 (redelivery upserts again, not an error)", len(client.calls))
	}
}

func TestCRMHandleParksMessageAfterMaxAttempts(t *testing.T) {
	w, client, ch, queue, _ := newCRMWorkerFixture(t)
	client.err = errors.New("hubspot unavailable")

	publishContactEvent(t, ch, queue, crm.ContactEvent{UserID: -1, Kind: crm.RoleBuyer})
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
	var event crm.ContactEvent
	if err := json.Unmarshal(parked.Body, &event); err != nil {
		t.Fatal(err)
	}
	if event.UserID != -1 {
		t.Errorf("parked payload user_id = %d, want -1", event.UserID)
	}
}

func TestCRMHandleRetriesBeforeMaxAttempts(t *testing.T) {
	w, client, ch, queue, _ := newCRMWorkerFixture(t)
	client.err = errors.New("hubspot unavailable")

	publishContactEvent(t, ch, queue, crm.ContactEvent{UserID: -1, Kind: crm.RoleBuyer})
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
