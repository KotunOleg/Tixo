package worker

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/KotunOleg/Tixo/internal/notify"
	"github.com/KotunOleg/Tixo/internal/platform/rabbitmq"
)

type fakeDeliverer struct {
	jobs []notify.ReminderJob
	err  error
}

func (f *fakeDeliverer) Deliver(_ context.Context, job notify.ReminderJob) error {
	f.jobs = append(f.jobs, job)
	return f.err
}

func newTelegramWorkerFixture(t *testing.T) (*TelegramWorker, *fakeDeliverer, *amqp.Channel, string) {
	t.Helper()
	conn := dialWorkerTest(t)
	ch, err := rabbitmq.OpenConfirmChannel(conn)
	if err != nil {
		t.Fatalf("open confirm channel: %v", err)
	}
	t.Cleanup(func() { _ = ch.Close() })

	queue := "test.notify.telegram." + strings.ReplaceAll(t.Name(), "/", "_") + "." + uuid.NewString()
	if err := rabbitmq.DeclareTopology(ch, queue); err != nil {
		t.Fatalf("declare topology: %v", err)
	}
	t.Cleanup(func() {
		for _, q := range []string{queue, queue + ".retry", queue + ".parking"} {
			_, _ = ch.QueueDelete(q, false, false, false)
		}
	})
	sender := &fakeDeliverer{}
	return &TelegramWorker{Queue: queue, Channel: ch, Sender: sender, Log: testLogger()}, sender, ch, queue
}

func publishReminder(t *testing.T, ch *amqp.Channel, queue string, job notify.ReminderJob) {
	t.Helper()
	body, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rabbitmq.Publish(ctx, ch, rabbitmq.Exchange, queue, body); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

func TestTelegramWorkerAcksDeliveredReminder(t *testing.T) {
	w, sender, ch, queue := newTelegramWorkerFixture(t)
	job := notify.ReminderJob{UserID: 7, EventID: 9, Kind: notify.KindReminder24h}

	publishReminder(t, ch, queue, job)
	w.handle(t.Context(), getDelivery(t, ch, queue))

	if len(sender.jobs) != 1 || sender.jobs[0] != job {
		t.Fatalf("delivered %+v, want %+v", sender.jobs, job)
	}
	if _, ok, err := ch.Get(queue, true); err != nil || ok {
		t.Errorf("message not acked: ok=%v err=%v", ok, err)
	}
	if _, ok, err := ch.Get(queue+".retry", true); err != nil || ok {
		t.Errorf("delivered reminder went to retry: ok=%v err=%v", ok, err)
	}
}

func TestTelegramWorkerSendsFailedReminderToRetry(t *testing.T) {
	w, sender, ch, queue := newTelegramWorkerFixture(t)
	sender.err = &notify.RetryAfterError{After: 5 * time.Second}

	publishReminder(t, ch, queue, notify.ReminderJob{UserID: 7, EventID: 9, Kind: notify.KindReminder24h})
	w.handle(t.Context(), getDelivery(t, ch, queue))

	if _, ok, err := ch.Get(queue+".parking", true); err != nil || ok {
		t.Error("reminder parked on its first failure")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, ok, err := ch.Get(queue+".retry", true); err == nil && ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("rate-limited reminder never reached the retry queue")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestTelegramWorkerParksReminderAfterMaxAttempts(t *testing.T) {
	w, sender, ch, queue := newTelegramWorkerFixture(t)
	sender.err = errors.New("telegram: sendMessage: status 502")

	publishReminder(t, ch, queue, notify.ReminderJob{UserID: 7, EventID: 9, Kind: notify.KindReminder24h})
	msg := getDelivery(t, ch, queue)
	msg.Headers = amqp.Table{
		"x-death": []interface{}{
			amqp.Table{"queue": queue, "reason": "rejected", "count": int64(maxAttempts - 1)},
		},
	}
	w.handle(t.Context(), msg)

	parked, ok, err := ch.Get(queue+".parking", true)
	if err != nil || !ok {
		t.Fatalf("reminder not parked after max attempts: ok=%v err=%v", ok, err)
	}
	var job notify.ReminderJob
	if err := json.Unmarshal(parked.Body, &job); err != nil || job.UserID != 7 {
		t.Errorf("parked payload = %s (err %v)", parked.Body, err)
	}
}

func TestTelegramWorkerRejectsMalformedPayloadWithoutDelivering(t *testing.T) {
	w, sender, ch, queue := newTelegramWorkerFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rabbitmq.Publish(ctx, ch, rabbitmq.Exchange, queue, []byte("not json")); err != nil {
		t.Fatal(err)
	}
	w.handle(t.Context(), getDelivery(t, ch, queue))
	if len(sender.jobs) != 0 {
		t.Errorf("malformed payload was delivered: %+v", sender.jobs)
	}
}
