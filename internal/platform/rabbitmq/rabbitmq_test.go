package rabbitmq

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
)

func TestCheckerFailsFastWhenBrokerIsDown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := Checker("amqp://guest:guest@127.0.0.1:1/").Check(ctx); err == nil {
		t.Error("check passed with no broker")
	}
	if time.Since(start) > 2*time.Second {
		t.Error("check ignored the context deadline")
	}
}

func TestCheckerDoesNotLeakCredentials(t *testing.T) {
	err := Checker("amqp://tixo:s3cret-pass@127.0.0.1:1/").Check(context.Background())
	if err != nil && strings.Contains(err.Error(), "s3cret-pass") {
		t.Errorf("error exposes broker password: %v", err)
	}
}

func TestCheckerAgainstRealBroker(t *testing.T) {
	url := os.Getenv("TEST_AMQP_URL")
	if url == "" {
		t.Skip("TEST_AMQP_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := Checker(url).Check(ctx); err != nil {
		t.Errorf("check failed against live broker: %v", err)
	}
}

func dialTest(t *testing.T) *amqp.Connection {
	t.Helper()
	url := os.Getenv("TEST_AMQP_URL")
	if url == "" {
		t.Skip("TEST_AMQP_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := Dial(ctx, url)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func testQueue(t *testing.T) string {
	t.Helper()
	return "test." + strings.ReplaceAll(t.Name(), "/", "_") + "." + uuid.NewString()
}

func cleanupQueue(t *testing.T, ch *amqp.Channel, queue string) {
	t.Helper()
	t.Cleanup(func() {
		for _, q := range []string{queue, queue + ".retry", queue + ".parking"} {
			_, _ = ch.QueueDelete(q, false, false, false)
		}
	})
}

func TestDeclareTopologyIsIdempotent(t *testing.T) {
	conn := dialTest(t)
	ch, err := conn.Channel()
	if err != nil {
		t.Fatalf("channel: %v", err)
	}
	defer func() { _ = ch.Close() }()
	queue := testQueue(t)
	cleanupQueue(t, ch, queue)

	if err := DeclareTopology(ch, queue); err != nil {
		t.Fatalf("declare topology: %v", err)
	}
	if err := DeclareTopology(ch, queue); err != nil {
		t.Fatalf("declare topology again: %v", err)
	}
}

func TestPublishIsConfirmedAndDeliveredToQueue(t *testing.T) {
	conn := dialTest(t)
	declareCh, err := conn.Channel()
	if err != nil {
		t.Fatalf("channel: %v", err)
	}
	defer func() { _ = declareCh.Close() }()
	queue := testQueue(t)
	cleanupQueue(t, declareCh, queue)
	if err := DeclareTopology(declareCh, queue); err != nil {
		t.Fatalf("declare topology: %v", err)
	}

	pubCh, err := OpenConfirmChannel(conn)
	if err != nil {
		t.Fatalf("open confirm channel: %v", err)
	}
	defer func() { _ = pubCh.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := Publish(ctx, pubCh, Exchange, queue, []byte(`{"order_id":1}`)); err != nil {
		t.Fatalf("publish: %v", err)
	}

	msg, ok, err := declareCh.Get(queue, true)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !ok {
		t.Fatal("no message delivered to queue")
	}
	if string(msg.Body) != `{"order_id":1}` {
		t.Errorf("body = %q, want %q", msg.Body, `{"order_id":1}`)
	}
}
