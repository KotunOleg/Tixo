package orders

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/KotunOleg/Tixo/internal/db/dbtest"
	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/events"
	"github.com/KotunOleg/Tixo/internal/organizers"
	"github.com/KotunOleg/Tixo/internal/tiers"
	"github.com/KotunOleg/Tixo/internal/venues"
)

type fixture struct {
	pool    *pgxpool.Pool
	svc     *Service
	org     int64
	eventID int64
	tier    dbgen.TicketTier
}

func mustUser(t *testing.T, pool *pgxpool.Pool, name string) int64 {
	t.Helper()
	u, err := dbgen.New(pool).CreateUser(context.Background(), dbgen.CreateUserParams{Email: name + "@example.com", Name: name})
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

func mustEvent(t *testing.T, pool *pgxpool.Pool, org int64, slug string, seats int32, publish bool) (int64, dbgen.TicketTier) {
	t.Helper()
	ctx := context.Background()
	v, err := venues.New(pool).Create(ctx, org, venues.Input{
		Name: slug + " venue", Address: "1 Test St", City: "Kyiv", Lat: 50.45, Lng: 30.52, Capacity: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(30 * 24 * time.Hour).Truncate(time.Second)
	ev, err := events.New(pool).Create(ctx, org, events.Input{
		VenueID: v.ID, Slug: slug, Title: "Big Show", Genre: events.GenreRock, StartsAt: start, EndsAt: start.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	tier, err := tiers.New(pool).Create(ctx, org, ev.ID, tiers.Input{Name: "Standard", Price: 50000, Quantity: seats})
	if err != nil {
		t.Fatal(err)
	}
	if publish {
		if _, err := events.New(pool).SetStatus(ctx, org, ev.ID, events.StatusPublished); err != nil {
			t.Fatal(err)
		}
	}
	return ev.ID, tier
}

func newFixture(t *testing.T, seats int32) fixture {
	t.Helper()
	pool := dbtest.Pool(t)
	owner := mustUser(t, pool, "owner")
	org, err := organizers.New(pool).Create(context.Background(), owner, organizers.CreateInput{Name: "Kyiv Live", Slug: "kyiv-live"})
	if err != nil {
		t.Fatal(err)
	}
	eventID, tier := mustEvent(t, pool, org.ID, "big-show", seats, true)
	return fixture{pool: pool, svc: New(pool), org: org.ID, eventID: eventID, tier: tier}
}

func (f fixture) available(t *testing.T) int32 {
	t.Helper()
	var n int32
	if err := f.pool.QueryRow(context.Background(), "SELECT available FROM ticket_tiers WHERE id = $1", f.tier.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (f fixture) count(t *testing.T, table string) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPurchaseFixesPriceAndIssuesOneTicketPerSeat(t *testing.T) {
	f := newFixture(t, 10)
	buyer := mustUser(t, f.pool, "alice")

	got, replay, err := f.svc.Purchase(context.Background(), buyer, Input{TierID: f.tier.ID, Quantity: 3, Key: uuid.New()})
	if err != nil || replay {
		t.Fatalf("Purchase = replay %v, err %v", replay, err)
	}
	if got.UnitPrice != 50000 || got.Total != 150000 || got.Status != StatusPaid || got.EventSlug != "big-show" || got.TierName != "Standard" {
		t.Errorf("order = %+v, want unit 50000, total 150000, paid, big-show/Standard", got.Order)
	}
	if len(got.Tickets) != 3 {
		t.Errorf("tickets = %d, want 3", len(got.Tickets))
	}
	if n := f.available(t); n != 7 {
		t.Errorf("available = %d, want 7", n)
	}
}

func TestPurchaseWritesOneUnpublishedOutboxRowForTheOrder(t *testing.T) {
	f := newFixture(t, 10)
	buyer := mustUser(t, f.pool, "alice")

	got, _, err := f.svc.Purchase(context.Background(), buyer, Input{TierID: f.tier.ID, Quantity: 1, Key: uuid.New()})
	if err != nil {
		t.Fatalf("Purchase: %v", err)
	}

	rows, err := dbgen.New(f.pool).ClaimOutboxBatch(context.Background(), 10)
	if err != nil {
		t.Fatalf("ClaimOutboxBatch: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("outbox rows = %d, want 1", len(rows))
	}
	if rows[0].Topic != TopicTicketsIssue {
		t.Errorf("topic = %q, want %q", rows[0].Topic, TopicTicketsIssue)
	}
	var payload outboxPayload
	if err := json.Unmarshal(rows[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.OrderID != got.ID {
		t.Errorf("payload order_id = %d, want %d", payload.OrderID, got.ID)
	}
}

func TestConcurrentBuyersNeverOversell(t *testing.T) {
	f := newFixture(t, 5)
	const buyers = 50
	users := make([]int64, buyers)
	for i := range users {
		users[i] = mustUser(t, f.pool, fmt.Sprintf("buyer-%d", i))
	}

	var wg sync.WaitGroup
	errs := make([]error, buyers)
	start := make(chan struct{})
	for i := range buyers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, errs[i] = f.svc.Purchase(context.Background(), users[i], Input{TierID: f.tier.ID, Quantity: 1, Key: uuid.New()})
		}()
	}
	close(start)
	wg.Wait()

	ok, soldOut := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrSoldOut):
			soldOut++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if ok != 5 || soldOut != buyers-5 {
		t.Errorf("succeeded %d, sold out %d; want 5 and %d", ok, soldOut, buyers-5)
	}
	if n := f.available(t); n != 0 {
		t.Errorf("available = %d, want 0", n)
	}
	if o, tk := f.count(t, "orders"), f.count(t, "tickets"); o != 5 || tk != 5 {
		t.Errorf("orders = %d, tickets = %d; want 5 and 5", o, tk)
	}
}

func TestConcurrentRetriesWithOneKeyBuyOnce(t *testing.T) {
	f := newFixture(t, 10)
	buyer := mustUser(t, f.pool, "alice")
	in := Input{TierID: f.tier.ID, Quantity: 2, Key: uuid.New()}
	const retries = 10

	var wg sync.WaitGroup
	ids := make([]int64, retries)
	fresh := make([]bool, retries)
	errs := make([]error, retries)
	start := make(chan struct{})
	for i := range retries {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			d, replay, err := f.svc.Purchase(context.Background(), buyer, in)
			ids[i], fresh[i], errs[i] = d.ID, !replay, err
		}()
	}
	close(start)
	wg.Wait()

	created := 0
	for i := range retries {
		if errs[i] != nil {
			t.Fatalf("retry %d: %v", i, errs[i])
		}
		if ids[i] != ids[0] {
			t.Errorf("retry %d got order %d, want %d", i, ids[i], ids[0])
		}
		if fresh[i] {
			created++
		}
	}
	if created != 1 {
		t.Errorf("%d calls reported a new order, want 1", created)
	}
	if n := f.available(t); n != 8 {
		t.Errorf("available = %d, want 8 (seats taken once)", n)
	}
	if tk := f.count(t, "tickets"); tk != 2 {
		t.Errorf("tickets = %d, want 2", tk)
	}
}

func TestKeyReusedWithAnotherBodyIsRejected(t *testing.T) {
	f := newFixture(t, 10)
	buyer := mustUser(t, f.pool, "alice")
	key := uuid.New()
	if _, _, err := f.svc.Purchase(context.Background(), buyer, Input{TierID: f.tier.ID, Quantity: 1, Key: key}); err != nil {
		t.Fatal(err)
	}

	_, _, err := f.svc.Purchase(context.Background(), buyer, Input{TierID: f.tier.ID, Quantity: 4, Key: key})
	if !errors.Is(err, ErrKeyReused) {
		t.Errorf("err = %v, want ErrKeyReused", err)
	}
	if n := f.available(t); n != 9 {
		t.Errorf("available = %d, want 9", n)
	}
}

func TestPurchaseRejectsWhatIsNotForSale(t *testing.T) {
	f := newFixture(t, 2)
	buyer := mustUser(t, f.pool, "alice")
	ctx := context.Background()
	_, draftTier := mustEvent(t, f.pool, f.org, "draft-show", 10, false)
	cancelledID, cancelledTier := mustEvent(t, f.pool, f.org, "cancelled-show", 10, true)
	if _, err := events.New(f.pool).SetStatus(ctx, f.org, cancelledID, events.StatusCancelled); err != nil {
		t.Fatal(err)
	}
	startedID, startedTier := mustEvent(t, f.pool, f.org, "started-show", 10, true)
	if _, err := f.pool.Exec(ctx, "UPDATE events SET starts_at = now() - interval '1 hour', ends_at = now() + interval '1 hour' WHERE id = $1", startedID); err != nil {
		t.Fatal(err)
	}
	_, otherTier := mustEvent(t, f.pool, f.org, "other-show", 10, true)

	var verr *ValidationError
	cases := []struct {
		name  string
		in    Input
		check func(error) bool
	}{
		{"draft event is hidden", Input{TierID: draftTier.ID, Quantity: 1}, func(err error) bool { return errors.Is(err, ErrNotFound) }},
		{"cancelled event", Input{TierID: cancelledTier.ID, Quantity: 1}, func(err error) bool { return errors.Is(err, ErrNotOnSale) }},
		{"event already started", Input{TierID: startedTier.ID, Quantity: 1}, func(err error) bool { return errors.Is(err, ErrNotOnSale) }},
		{"more seats than left", Input{TierID: f.tier.ID, Quantity: 3}, func(err error) bool { return errors.Is(err, ErrSoldOut) }},
		{"unknown tier", Input{TierID: 999999, Quantity: 1}, func(err error) bool { return errors.Is(err, ErrNotFound) }},
		{"zero quantity", Input{TierID: f.tier.ID, Quantity: 0}, func(err error) bool { return errors.As(err, &verr) && verr.Fields[FieldQuantity] == CodeRange }},
		{"quantity above limit", Input{TierID: f.tier.ID, Quantity: 11}, func(err error) bool { return errors.As(err, &verr) && verr.Fields[FieldQuantity] == CodeRange }},
		{"tier of another event", Input{EventID: f.eventID, TierID: otherTier.ID, Quantity: 1}, func(err error) bool {
			return errors.As(err, &verr) && verr.Fields[FieldTier] == CodeInvalid
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.in.Key = uuid.New()
			_, _, err := f.svc.Purchase(ctx, buyer, tc.in)
			if !tc.check(err) {
				t.Errorf("err = %v", err)
			}
		})
	}
	if n := f.available(t); n != 2 {
		t.Errorf("available = %d after rejected purchases, want 2", n)
	}
	if o := f.count(t, "orders"); o != 0 {
		t.Errorf("orders = %d after rejected purchases, want 0", o)
	}
}

func TestPurchaseRequiresKey(t *testing.T) {
	f := newFixture(t, 2)
	buyer := mustUser(t, f.pool, "alice")
	_, _, err := f.svc.Purchase(context.Background(), buyer, Input{TierID: f.tier.ID, Quantity: 1})
	var verr *ValidationError
	if !errors.As(err, &verr) || verr.Fields[FieldKey] != CodeRequired {
		t.Errorf("err = %v, want idempotency_key required", err)
	}
}

func TestOrdersAreVisibleOnlyToTheBuyer(t *testing.T) {
	f := newFixture(t, 10)
	ctx := context.Background()
	alice := mustUser(t, f.pool, "alice")
	bob := mustUser(t, f.pool, "bob")
	key := uuid.New()
	order, _, err := f.svc.Purchase(ctx, alice, Input{TierID: f.tier.ID, Quantity: 1, Key: key})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := f.svc.Get(ctx, bob, order.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("bob Get(alice's order) err = %v, want ErrNotFound", err)
	}
	list, total, err := f.svc.List(ctx, bob, 20, 0)
	if err != nil || total != 0 || len(list) != 0 {
		t.Errorf("bob List = %d items, total %d, err %v; want empty", len(list), total, err)
	}
	bobs, replay, err := f.svc.Purchase(ctx, bob, Input{TierID: f.tier.ID, Quantity: 1, Key: key})
	if err != nil || replay || bobs.ID == order.ID {
		t.Errorf("bob with alice's key: order %d replay %v err %v; want his own new order", bobs.ID, replay, err)
	}
}

func TestListShowsNewestFirstWithTotal(t *testing.T) {
	f := newFixture(t, 10)
	ctx := context.Background()
	alice := mustUser(t, f.pool, "alice")
	var ids []int64
	for range 3 {
		d, _, err := f.svc.Purchase(ctx, alice, Input{TierID: f.tier.ID, Quantity: 1, Key: uuid.New()})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, d.ID)
	}

	page, total, err := f.svc.List(ctx, alice, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(page) != 2 || page[0].ID != ids[2] || page[1].ID != ids[1] {
		t.Errorf("List(2,0) = %v total %d; want [%d %d] total 3", orderIDs(page), total, ids[2], ids[1])
	}
}

func TestListReportsPDFReadyFromTicketPDFKeys(t *testing.T) {
	f := newFixture(t, 10)
	ctx := context.Background()
	alice := mustUser(t, f.pool, "alice")

	d, _, err := f.svc.Purchase(ctx, alice, Input{TierID: f.tier.ID, Quantity: 2, Key: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}

	page, _, err := f.svc.List(ctx, alice, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].PDFReady {
		t.Fatalf("PDFReady before any pdf_key is set = %v, want false", page[0].PDFReady)
	}

	tickets, err := f.svc.q.ListTicketsForOrder(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, "UPDATE tickets SET pdf_key = 'deadbeefdeadbeefdeadbeefdeadbeef.pdf' WHERE id = $1", tickets[0].ID); err != nil {
		t.Fatal(err)
	}
	page, _, err = f.svc.List(ctx, alice, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page[0].PDFReady {
		t.Fatal("PDFReady = true with one ticket still missing a pdf_key")
	}

	if _, err := f.pool.Exec(ctx, "UPDATE tickets SET pdf_key = 'deadbeefdeadbeefdeadbeefdeadbeef.pdf' WHERE order_id = $1", d.ID); err != nil {
		t.Fatal(err)
	}
	page, _, err = f.svc.List(ctx, alice, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !page[0].PDFReady {
		t.Fatal("PDFReady = false once every ticket has a pdf_key")
	}
}

func orderIDs(ds []Details) []int64 {
	out := make([]int64, len(ds))
	for i, d := range ds {
		out[i] = d.ID
	}
	return out
}
