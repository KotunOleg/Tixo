package main

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/KotunOleg/Tixo/internal/platform/rabbitmq"
)

func TestSchedulerIsRegistered(t *testing.T) {
	if _, ok := commands["scheduler"]; !ok {
		t.Fatal(`commands["scheduler"] is not registered`)
	}
}

func TestSchedulerFailsFastWhenRabbitMQIsUnreachable(t *testing.T) {
	setUnreachableWorkerEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	start := time.Now()
	err := run(ctx, []string{"scheduler"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("scheduler started against an unreachable broker, want an error")
	}
	if time.Since(start) > 3*time.Second {
		t.Error("scheduler ignored the context deadline instead of failing fast")
	}
}

func TestSchedulerQueuesKeepMessagesPublishedBeforeAnyWorkerStarts(t *testing.T) {
	url := os.Getenv("TEST_AMQP_URL")
	if url == "" {
		t.Skip("TEST_AMQP_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := rabbitmq.Dial(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	ch, err := rabbitmq.OpenConfirmChannel(conn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ch.Close() })

	for _, queue := range workerQueues {
		for _, q := range []string{queue, queue + ".retry", queue + ".parking"} {
			if _, err := ch.QueueDelete(q, false, false, false); err != nil {
				t.Fatalf("delete %s: %v", q, err)
			}
		}
	}
	if err := declareWorkerQueues(ch); err != nil {
		t.Fatal(err)
	}
	for _, queue := range workerQueues {
		t.Run(queue, func(t *testing.T) {
			if err := rabbitmq.Publish(ctx, ch, rabbitmq.Exchange, queue, []byte(`{"user_id":1}`)); err != nil {
				t.Fatal(err)
			}
			msg, ok, err := ch.Get(queue, true)
			if err != nil || !ok {
				t.Fatalf("message relayed to %s before its worker started was dropped: ok=%v err=%v", queue, ok, err)
			}
			if string(msg.Body) != `{"user_id":1}` {
				t.Errorf("body = %s", msg.Body)
			}
		})
	}
}
