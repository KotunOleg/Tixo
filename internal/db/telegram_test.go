package db_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
)

func linkChat(t *testing.T, q *dbgen.Queries, userID, chatID int64) {
	t.Helper()
	ctx := context.Background()
	if err := q.IssueTelegramLinkToken(ctx, dbgen.IssueTelegramLinkTokenParams{
		UserID: userID, TokenHash: []byte{byte(userID), byte(chatID)}, ExpiresAt: demoStart,
	}); err != nil {
		t.Fatal(err)
	}
	if err := q.LinkTelegramChat(ctx, dbgen.LinkTelegramChatParams{
		UserID: userID, ChatID: chatID, Language: "uk", LinkedAt: demoStart,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestTelegramLinkTokenIsSingleUseAndExpires(t *testing.T) {
	_, q := newQueries(t)
	ctx := context.Background()
	alice := mustUser(t, q, "alice@example.com")
	hash := []byte("hash-1")
	expires := demoStart.Add(15 * time.Minute)
	if err := q.IssueTelegramLinkToken(ctx, dbgen.IssueTelegramLinkTokenParams{UserID: alice.ID, TokenHash: hash, ExpiresAt: expires}); err != nil {
		t.Fatal(err)
	}

	_, err := q.ConsumeTelegramLinkToken(ctx, dbgen.ConsumeTelegramLinkTokenParams{TokenHash: hash, Now: expires})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("expired token: err = %v, want no rows", err)
	}
	_, err = q.ConsumeTelegramLinkToken(ctx, dbgen.ConsumeTelegramLinkTokenParams{TokenHash: []byte("other"), Now: demoStart})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("unknown token: err = %v, want no rows", err)
	}
	got, err := q.ConsumeTelegramLinkToken(ctx, dbgen.ConsumeTelegramLinkTokenParams{TokenHash: hash, Now: demoStart})
	if err != nil || got != alice.ID {
		t.Fatalf("live token: user = %d, err = %v, want %d", got, err, alice.ID)
	}
	_, err = q.ConsumeTelegramLinkToken(ctx, dbgen.ConsumeTelegramLinkTokenParams{TokenHash: hash, Now: demoStart})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("replayed token: err = %v, want no rows", err)
	}
}

func TestTelegramChatBelongsToOneUserAtATime(t *testing.T) {
	_, q := newQueries(t)
	ctx := context.Background()
	alice := mustUser(t, q, "alice@example.com")
	bob := mustUser(t, q, "bob@example.com")
	linkChat(t, q, alice.ID, 42)
	if err := q.IssueTelegramLinkToken(ctx, dbgen.IssueTelegramLinkTokenParams{UserID: bob.ID, TokenHash: []byte("b"), ExpiresAt: demoStart}); err != nil {
		t.Fatal(err)
	}

	err := q.LinkTelegramChat(ctx, dbgen.LinkTelegramChatParams{UserID: bob.ID, ChatID: 42, Language: "en", LinkedAt: demoStart})
	wantPgCode(t, "link a chat still held by another user", err, uniqueViolation)

	if err := q.ReleaseTelegramChat(ctx, 42); err != nil {
		t.Fatal(err)
	}
	if err := q.LinkTelegramChat(ctx, dbgen.LinkTelegramChatParams{UserID: bob.ID, ChatID: 42, Language: "en", LinkedAt: demoStart}); err != nil {
		t.Fatal(err)
	}
	a, _ := q.GetTelegramLink(ctx, alice.ID)
	b, _ := q.GetTelegramLink(ctx, bob.ID)
	if a.ChatID.Valid || a.LinkedAt.Valid {
		t.Errorf("alice still linked after bob took the chat: %+v", a)
	}
	if b.ChatID.Int64 != 42 || b.Language != "en" {
		t.Errorf("bob link = %+v, want chat 42 in en", b)
	}

	if n, err := q.UnlinkTelegramChat(ctx, 42); err != nil || n != 1 {
		t.Errorf("unlink by chat: rows = %d, err = %v", n, err)
	}
	if n, _ := q.UnlinkTelegramChat(ctx, 42); n != 0 {
		t.Errorf("second unlink by chat touched %d rows, want 0", n)
	}
}

func TestQueueEventRemindersPicksTicketHoldersWithLinkedChatOnce(t *testing.T) {
	pool, q := newQueries(t)
	ctx := context.Background()
	org := mustOrganizer(t, q, "org")
	venue := mustVenue(t, q, org.ID, "Hall")
	now := demoStart.Add(-12 * time.Hour)

	event := func(slug, status string, startsAt time.Time) dbgen.TicketTier {
		p := eventParams(org.ID, venue.ID, slug)
		p.StartsAt, p.EndsAt = startsAt, startsAt.Add(2*time.Hour)
		ev, err := q.CreateEvent(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		tier := mustTier(t, q, org.ID, ev.ID, "Standard", 100)
		if _, err := q.SetEventStatus(ctx, dbgen.SetEventStatusParams{ID: ev.ID, OrganizerID: org.ID, Status: status}); err != nil {
			t.Fatal(err)
		}
		return tier
	}
	soon := event("soon", "published", demoStart)
	draft := event("draft", "draft", demoStart)
	cancelled := event("cancelled", "cancelled", demoStart)
	started := event("started", "published", now.Add(-time.Minute))
	later := event("later", "published", now.Add(25*time.Hour))
	edge := event("edge", "published", now.Add(24*time.Hour))

	alice := mustUser(t, q, "alice@example.com")
	bob := mustUser(t, q, "bob@example.com")
	carol := mustUser(t, q, "carol@example.com")
	dave := mustUser(t, q, "dave@example.com")
	linkChat(t, q, alice.ID, 1)
	linkChat(t, q, carol.ID, 3)
	linkChat(t, q, dave.ID, 4)

	key := byte(0)
	buy := func(userID int64, tier dbgen.TicketTier) dbgen.Order {
		key++
		o, err := mustOrder(t, q, userID, tier, pgtype.UUID{Bytes: [16]byte{key}, Valid: true})
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	for _, tier := range []dbgen.TicketTier{soon, soon, draft, cancelled, started, later, edge} {
		buy(alice.ID, tier)
	}
	buy(bob.ID, soon)
	refunded := buy(dave.ID, soon)
	if _, err := pool.Exec(ctx, `UPDATE orders SET status = 'cancelled' WHERE id = $1`, refunded.ID); err != nil {
		t.Fatal(err)
	}

	params := dbgen.QueueEventRemindersParams{Kind: "reminder_24h", Now: now, Until: now.Add(24 * time.Hour)}
	rows, err := q.QueueEventReminders(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	got := map[[2]int64]bool{}
	for _, r := range rows {
		got[[2]int64{r.UserID, r.EventID}] = true
	}
	want := map[[2]int64]bool{{alice.ID, soon.EventID}: true, {alice.ID, edge.EventID}: true}
	if len(rows) != len(want) || len(got) != len(want) {
		t.Fatalf("queued %+v, want alice for soon and edge only", rows)
	}
	for k := range want {
		if !got[k] {
			t.Errorf("missing reminder %v in %+v", k, rows)
		}
	}

	again, err := q.QueueEventReminders(ctx, params)
	if err != nil || len(again) != 0 {
		t.Errorf("second tick queued %+v (err %v), want nothing", again, err)
	}
}

func TestEventReminderIsMarkedSentOnceAndSkipDoesNotOverwriteSent(t *testing.T) {
	_, q := newQueries(t)
	ctx := context.Background()
	org := mustOrganizer(t, q, "org")
	ev := mustEvent(t, q, org.ID, mustVenue(t, q, org.ID, "Hall").ID, "show")
	tier := mustTier(t, q, org.ID, ev.ID, "Standard", 10)
	if _, err := q.SetEventStatus(ctx, dbgen.SetEventStatusParams{ID: ev.ID, OrganizerID: org.ID, Status: "published"}); err != nil {
		t.Fatal(err)
	}
	alice := mustUser(t, q, "alice@example.com")
	linkChat(t, q, alice.ID, 7)
	if _, err := mustOrder(t, q, alice.ID, tier, pgtype.UUID{Bytes: [16]byte{1}, Valid: true}); err != nil {
		t.Fatal(err)
	}
	now := demoStart.Add(-time.Hour)
	if _, err := q.QueueEventReminders(ctx, dbgen.QueueEventRemindersParams{Kind: "reminder_24h", Now: now, Until: demoStart}); err != nil {
		t.Fatal(err)
	}
	key := dbgen.GetEventReminderParams{UserID: alice.ID, EventID: ev.ID, Kind: "reminder_24h"}

	r, err := q.GetEventReminder(ctx, key)
	if err != nil || r.SentAt.Valid || r.ChatID.Int64 != 7 || r.VenueName != "Hall" {
		t.Fatalf("fresh reminder = %+v, err %v", r, err)
	}
	if err := q.MarkNotificationSent(ctx, dbgen.MarkNotificationSentParams{UserID: alice.ID, EventID: ev.ID, Kind: "reminder_24h", SentAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := q.MarkNotificationSkipped(ctx, dbgen.MarkNotificationSkippedParams{UserID: alice.ID, EventID: ev.ID, Kind: "reminder_24h", Reason: "late"}); err != nil {
		t.Fatal(err)
	}
	r, _ = q.GetEventReminder(ctx, key)
	if !r.SentAt.Valid || r.SkippedReason.Valid {
		t.Errorf("after send and a late skip: %+v, want sent and not skipped", r)
	}

	if err := q.UnlinkTelegramUser(ctx, alice.ID); err != nil {
		t.Fatal(err)
	}
	r, err = q.GetEventReminder(ctx, key)
	if err != nil || r.ChatID.Valid {
		t.Errorf("reminder after unlink = %+v, err %v; want it readable with no chat", r, err)
	}
}
