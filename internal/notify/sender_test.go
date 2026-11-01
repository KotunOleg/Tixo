package notify

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/web/i18n"
	"github.com/KotunOleg/Tixo/web"
)

type senderFixture struct {
	*reminderFixture
	sender *Sender
	client *fakeClient
}

func newSenderFixture(t *testing.T) *senderFixture {
	t.Helper()
	f := newReminderFixture(t)
	tr, err := i18n.New(web.FS)
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeClient{}
	s := NewSender(f.pool, client, tr, "https://tixo.example/", slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.now = func() time.Time { return botNow }
	return &senderFixture{reminderFixture: f, sender: s, client: client}
}

func (f *senderFixture) queued(t *testing.T, title string, chatID int64) ReminderJob {
	t.Helper()
	tier := f.event(t, title, botNow.Add(20*time.Hour))
	userID := f.holder(t, "fan@example.com", chatID, tier)
	if res, err := NewReminders(f.pool).Enqueue(context.Background(), botNow, ReminderWindow); err != nil || res.Queued != 1 {
		t.Fatalf("enqueue = %+v, %v", res, err)
	}
	return ReminderJob{UserID: userID, EventID: tier.EventID, Kind: KindReminder24h}
}

func (f *senderFixture) state(t *testing.T, job ReminderJob) dbgen.GetEventReminderRow {
	t.Helper()
	r, err := f.q.GetEventReminder(context.Background(), dbgen.GetEventReminderParams{UserID: job.UserID, EventID: job.EventID, Kind: job.Kind})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestDeliverTwiceSendsOneReminder(t *testing.T) {
	f := newSenderFixture(t)
	job := f.queued(t, "Jazz night", 42)

	for range 2 {
		if err := f.sender.Deliver(context.Background(), job); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.client.sent) != 1 {
		t.Fatalf("redelivered job sent %d messages, want 1", len(f.client.sent))
	}
	if r := f.state(t, job); !r.SentAt.Valid {
		t.Error("sent_at not set after a successful send")
	}
}

func TestReminderTextIsPlainWithEventDetailsAndTicketsLink(t *testing.T) {
	f := newSenderFixture(t)
	title := "<b>Rock</b> [free](https://evil.example) {{.URL}}"
	job := f.queued(t, title, 42)

	if err := f.sender.Deliver(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	text := f.client.sent[0].text
	for _, want := range []string{title, "03.11.2026 10:00", "Atlas, Kyiv", "https://tixo.example/tickets"} {
		if !strings.Contains(text, want) {
			t.Errorf("reminder %q does not contain %q", text, want)
		}
	}
}

func TestReminderUsesTheLanguageOfTheLink(t *testing.T) {
	f := newSenderFixture(t)
	job := f.queued(t, "Show", 42)
	if _, err := f.pool.Exec(context.Background(), `UPDATE telegram_links SET language = 'en'`); err != nil {
		t.Fatal(err)
	}
	if err := f.sender.Deliver(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if text := f.client.sent[0].text; !strings.Contains(text, "Event reminder") || !strings.Contains(text, "https://tixo.example/en/tickets") {
		t.Errorf("english reminder = %q", text)
	}
}

func TestDeliverSkipsWhenSendingNoLongerMakesSense(t *testing.T) {
	cases := []struct {
		name   string
		change string
		now    time.Duration
		want   string
	}{
		{"event cancelled after queueing", `UPDATE events SET status = 'cancelled'`, 0, "event_cancelled"},
		{"event moved back to draft", `UPDATE events SET status = 'draft'`, 0, "event_draft"},
		{"chat unlinked after queueing", `UPDATE telegram_links SET chat_id = NULL, linked_at = NULL`, 0, SkipUnlinked},
		{"event already started", ``, 21 * time.Hour, SkipStarted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSenderFixture(t)
			job := f.queued(t, "Show", 42)
			if tc.change != "" {
				if _, err := f.pool.Exec(context.Background(), tc.change); err != nil {
					t.Fatal(err)
				}
			}
			f.sender.now = func() time.Time { return botNow.Add(tc.now) }

			if err := f.sender.Deliver(context.Background(), job); err != nil {
				t.Fatal(err)
			}
			if len(f.client.sent) != 0 {
				t.Errorf("sent %+v, want nothing", f.client.sent)
			}
			if r := f.state(t, job); r.SkippedReason.String != tc.want || r.SentAt.Valid {
				t.Errorf("skipped_reason = %q, sent = %v; want %q and not sent", r.SkippedReason.String, r.SentAt.Valid, tc.want)
			}
		})
	}
}

func TestDeliverClassifiesTelegramErrors(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantErr    bool
		wantSkip   string
		wantLinked bool
	}{
		{"user blocked the bot", ErrForbidden, false, SkipBlocked, false},
		{"chat not found", &PermanentError{Status: 400, Description: "Bad Request: chat not found"}, false, SkipRejected, true},
		{"rate limited goes to retry", &RetryAfterError{After: 3 * time.Second}, true, "", true},
		{"server error goes to retry", errors.New("telegram: sendMessage: status 502"), true, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSenderFixture(t)
			job := f.queued(t, "Show", 42)
			f.client.err = tc.err

			err := f.sender.Deliver(context.Background(), job)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, want error %v", err, tc.wantErr)
			}
			r := f.state(t, job)
			if r.SkippedReason.String != tc.wantSkip || r.SentAt.Valid {
				t.Errorf("skipped_reason = %q, sent = %v; want %q and not sent", r.SkippedReason.String, r.SentAt.Valid, tc.wantSkip)
			}
			if r.ChatID.Valid != tc.wantLinked {
				t.Errorf("chat linked = %v, want %v", r.ChatID.Valid, tc.wantLinked)
			}
		})
	}
}

func TestDeliverDropsJobWhoseReminderRowIsGone(t *testing.T) {
	f := newSenderFixture(t)
	if err := f.sender.Deliver(context.Background(), ReminderJob{UserID: 1, EventID: 1, Kind: KindReminder24h}); err != nil {
		t.Errorf("err = %v, want nil so the message is acked instead of retried forever", err)
	}
}
