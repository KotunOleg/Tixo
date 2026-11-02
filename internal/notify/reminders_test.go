package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/KotunOleg/Tixo/internal/db/dbtest"
	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
)

type reminderFixture struct {
	pool *pgxpool.Pool
	q    *dbgen.Queries
	org  dbgen.Organizer
	ven  dbgen.Venue
	n    int
}

func newReminderFixture(t *testing.T) *reminderFixture {
	t.Helper()
	pool := dbtest.Pool(t)
	q := dbgen.New(pool)
	ctx := context.Background()
	org, err := q.CreateOrganizer(ctx, dbgen.CreateOrganizerParams{Slug: "org", Name: "Org", Contacts: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	ven, err := q.CreateVenue(ctx, dbgen.CreateVenueParams{
		OrganizerID: org.ID, Name: "Atlas", Address: "vul. Sichovykh Striltsiv 37", City: "Kyiv", Lat: 50.45, Lng: 30.5, Capacity: 500,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &reminderFixture{pool: pool, q: q, org: org, ven: ven}
}

func (f *reminderFixture) event(t *testing.T, title string, startsAt time.Time) dbgen.TicketTier {
	t.Helper()
	ctx := context.Background()
	f.n++
	ev, err := f.q.CreateEvent(ctx, dbgen.CreateEventParams{
		OrganizerID: f.org.ID, VenueID: f.ven.ID, Slug: fmt.Sprintf("event-%d", f.n), Title: title,
		StartsAt: startsAt, EndsAt: startsAt.Add(2 * time.Hour), Genre: "rock",
	})
	if err != nil {
		t.Fatal(err)
	}
	tier, err := f.q.CreateTicketTier(ctx, dbgen.CreateTicketTierParams{EventID: ev.ID, OrganizerID: f.org.ID, Name: "Standard", Price: 50000, Quantity: 100})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.q.SetEventStatus(ctx, dbgen.SetEventStatusParams{ID: ev.ID, OrganizerID: f.org.ID, Status: "published"}); err != nil {
		t.Fatal(err)
	}
	return tier
}

func (f *reminderFixture) holder(t *testing.T, email string, chatID int64, tiers ...dbgen.TicketTier) int64 {
	t.Helper()
	ctx := context.Background()
	f.n++
	u, err := f.q.CreateUser(ctx, dbgen.CreateUserParams{Email: email, Name: email})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.q.IssueTelegramLinkToken(ctx, dbgen.IssueTelegramLinkTokenParams{UserID: u.ID, TokenHash: []byte(email), ExpiresAt: botNow}); err != nil {
		t.Fatal(err)
	}
	if err := f.q.LinkTelegramChat(ctx, dbgen.LinkTelegramChatParams{UserID: u.ID, ChatID: chatID, Language: "uk", LinkedAt: botNow}); err != nil {
		t.Fatal(err)
	}
	for i, tier := range tiers {
		if _, err := f.q.CreateOrder(ctx, dbgen.CreateOrderParams{
			UserID: u.ID, EventID: tier.EventID, TierID: tier.ID, Quantity: 1, UnitPrice: tier.Price, Total: tier.Price,
			IdempotencyKey: pgtype.UUID{Bytes: [16]byte{byte(f.n), byte(i + 1)}, Valid: true},
		}); err != nil {
			t.Fatal(err)
		}
	}
	return u.ID
}

func (f *reminderFixture) outboxJobs(t *testing.T) []ReminderJob {
	t.Helper()
	rows, err := f.pool.Query(context.Background(), `SELECT payload FROM outbox WHERE topic = $1 ORDER BY id`, TopicTelegram)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var jobs []ReminderJob
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var j ReminderJob
		if err := json.Unmarshal(raw, &j); err != nil {
			t.Fatal(err)
		}
		jobs = append(jobs, j)
	}
	return jobs
}

func TestEnqueueTwoTicksQueueEachReminderOnce(t *testing.T) {
	f := newReminderFixture(t)
	show := f.event(t, "Show", botNow.Add(20*time.Hour))
	alice := f.holder(t, "alice@example.com", 42, show, show)
	r := NewReminders(f.pool)

	first, err := r.Enqueue(context.Background(), botNow, ReminderWindow)
	if err != nil || first != 1 {
		t.Fatalf("first tick queued %d (err %v), want 1 even with two orders", first, err)
	}
	second, err := r.Enqueue(context.Background(), botNow.Add(time.Hour), ReminderWindow)
	if err != nil || second != 0 {
		t.Fatalf("second tick queued %d (err %v), want 0", second, err)
	}
	jobs := f.outboxJobs(t)
	want := ReminderJob{UserID: alice, EventID: show.EventID, Kind: KindReminder24h}
	if len(jobs) != 1 || jobs[0] != want {
		t.Errorf("outbox = %+v, want exactly %+v", jobs, want)
	}
}

func TestEnqueueConcurrentSchedulersQueueEachReminderOnce(t *testing.T) {
	f := newReminderFixture(t)
	show := f.event(t, "Show", botNow.Add(3*time.Hour))
	for i := range 5 {
		f.holder(t, fmt.Sprintf("fan%d@example.com", i), int64(100+i), show)
	}
	r := NewReminders(f.pool)

	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Go(func() {
			_, errs[i] = r.Enqueue(context.Background(), botNow, ReminderWindow)
		})
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if jobs := f.outboxJobs(t); len(jobs) != 5 {
		t.Errorf("outbox has %d reminder jobs after 4 concurrent ticks, want 5 (one per holder)", len(jobs))
	}
}

func TestEnqueueRespectsTheWindow(t *testing.T) {
	f := newReminderFixture(t)
	inDay := f.event(t, "In a day", botNow.Add(23*time.Hour))
	inThreeDays := f.event(t, "In three days", botNow.Add(60*time.Hour))
	f.holder(t, "alice@example.com", 42, inDay, inThreeDays)
	r := NewReminders(f.pool)

	if n, err := r.Enqueue(context.Background(), botNow, ReminderWindow); err != nil || n != 1 {
		t.Fatalf("24h window queued %d (err %v), want only the event within a day", n, err)
	}
	if n, err := r.Enqueue(context.Background(), botNow, 72*time.Hour); err != nil || n != 1 {
		t.Fatalf("72h demo window queued %d (err %v), want the remaining event", n, err)
	}
	if _, err := r.Enqueue(context.Background(), botNow, 0); err == nil {
		t.Error("zero window accepted")
	}
}
