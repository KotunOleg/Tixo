package tools

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/KotunOleg/Tixo/internal/db/dbtest"
	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/events"
	"github.com/KotunOleg/Tixo/internal/orders"
	"github.com/KotunOleg/Tixo/internal/organizers"
	"github.com/KotunOleg/Tixo/internal/tiers"
	"github.com/KotunOleg/Tixo/internal/venues"
)

type fixture struct {
	pool  *pgxpool.Pool
	tools *Tools
	org   int64
	buyer int64
}

func mustUser(t *testing.T, pool *pgxpool.Pool, name string) int64 {
	t.Helper()
	u, err := dbgen.New(pool).CreateUser(context.Background(), dbgen.CreateUserParams{Email: name + "@example.com", Name: name})
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	pool := dbtest.Pool(t)
	owner := mustUser(t, pool, "owner")
	org, err := organizers.New(pool).Create(context.Background(), owner, organizers.CreateInput{Name: "Kyiv Live", Slug: "kyiv-live"})
	if err != nil {
		t.Fatal(err)
	}
	base, _ := url.Parse("https://tixo.test")
	return fixture{
		pool: pool, tools: New(events.New(pool), orders.New(pool), *base, time.Now), org: org.ID, buyer: mustUser(t, pool, "buyer"),
	}
}

func (f fixture) event(t *testing.T, slug, city, status string, seats int32) dbgen.TicketTier {
	t.Helper()
	ctx := context.Background()
	v, err := venues.New(f.pool).Create(ctx, f.org, venues.Input{
		Name: slug + " hall", Address: "1 Test St", City: city, Lat: 50.45, Lng: 30.52, Capacity: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(30 * 24 * time.Hour).Truncate(time.Second)
	ev, err := events.New(f.pool).Create(ctx, f.org, events.Input{
		VenueID: v.ID, Slug: slug, Title: "Show " + slug, Genre: events.GenreRock, StartsAt: start, EndsAt: start.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	tier, err := tiers.New(f.pool).Create(ctx, f.org, ev.ID, tiers.Input{Name: "Standard", Price: 50000, Quantity: seats})
	if err != nil {
		t.Fatal(err)
	}
	if status != events.StatusDraft {
		if _, err := events.New(f.pool).SetStatus(ctx, f.org, ev.ID, events.StatusPublished); err != nil {
			t.Fatal(err)
		}
	}
	if status == events.StatusCancelled {
		if _, err := events.New(f.pool).SetStatus(ctx, f.org, ev.ID, events.StatusCancelled); err != nil {
			t.Fatal(err)
		}
	}
	return tier
}

func (f fixture) orderCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(), "SELECT count(*) FROM orders").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	var terr *Error
	if !errors.As(err, &terr) || terr.Code != code {
		t.Fatalf("error = %v, want tool error %q", err, code)
	}
}

func TestBookTicketMapsEveryFailureToAToolErrorWithoutBuying(t *testing.T) {
	f := newFixture(t)
	open := f.event(t, "open-show", "Kyiv", events.StatusPublished, 5)
	other := f.event(t, "other-show", "Kyiv", events.StatusPublished, 5)
	f.event(t, "draft-show", "Kyiv", events.StatusDraft, 5)
	cancelled := f.event(t, "cancelled-show", "Kyiv", events.StatusCancelled, 5)
	soldOut := f.event(t, "tiny-show", "Kyiv", events.StatusPublished, 1)
	key := "key-12345"

	for _, tc := range []struct {
		name  string
		user  int64
		in    BookTicketInput
		code  string
		inMsg string
	}{
		{"no signed in user", 0, BookTicketInput{EventSlug: "open-show", TierID: open.ID, Quantity: 1, IdempotencyKey: key}, CodeNoAccount, ""},
		{"key too short to be unique", f.buyer, BookTicketInput{EventSlug: "open-show", TierID: open.ID, Quantity: 1, IdempotencyKey: "abc"}, CodeInvalid, "idempotency_key"},
		{"zero tickets", f.buyer, BookTicketInput{EventSlug: "open-show", TierID: open.ID, Quantity: 0, IdempotencyKey: key}, CodeInvalid, "quantity"},
		{"more than ten tickets", f.buyer, BookTicketInput{EventSlug: "open-show", TierID: open.ID, Quantity: 11, IdempotencyKey: key}, CodeInvalid, "quantity"},
		{"invented slug", f.buyer, BookTicketInput{EventSlug: "no-such-show", TierID: open.ID, Quantity: 1, IdempotencyKey: key}, CodeNotFound, ""},
		{"draft is invisible", f.buyer, BookTicketInput{EventSlug: "draft-show", TierID: open.ID, Quantity: 1, IdempotencyKey: key}, CodeNotFound, ""},
		{"tier of another event", f.buyer, BookTicketInput{EventSlug: "open-show", TierID: other.ID, Quantity: 1, IdempotencyKey: key}, CodeInvalid, "tier_id"},
		{"cancelled event", f.buyer, BookTicketInput{EventSlug: "cancelled-show", TierID: cancelled.ID, Quantity: 1, IdempotencyKey: key}, CodeNotOnSale, ""},
		{"more than left", f.buyer, BookTicketInput{EventSlug: "tiny-show", TierID: soldOut.ID, Quantity: 2, IdempotencyKey: key}, CodeSoldOut, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.tools.BookTicket(context.Background(), tc.user, tc.in)
			wantCode(t, err, tc.code)
			if !strings.Contains(err.Error(), tc.inMsg) {
				t.Errorf("message %q does not name %q", err, tc.inMsg)
			}
		})
	}
	if n := f.orderCount(t); n != 0 {
		t.Fatalf("failed bookings created %d orders", n)
	}
}

func TestBookTicketRetryWithTheSameKeyReturnsTheSameOrder(t *testing.T) {
	f := newFixture(t)
	tier := f.event(t, "big-show", "Kyiv", events.StatusPublished, 10)
	in := BookTicketInput{EventSlug: "big-show", TierID: tier.ID, Quantity: 2, IdempotencyKey: "retry-key-1"}
	ctx := context.Background()

	first, err := f.tools.BookTicket(ctx, f.buyer, in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.tools.BookTicket(ctx, f.buyer, in)
	if err != nil {
		t.Fatal(err)
	}
	if first.OrderID != second.OrderID || first.Replayed || !second.Replayed {
		t.Fatalf("retry gave order %d (replayed=%v) after %d, want the same order replayed", second.OrderID, second.Replayed, first.OrderID)
	}
	if first.TotalUAH != 1000 || first.Tickets != 2 || first.URL != "https://tixo.test/orders/"+itoa(first.OrderID) {
		t.Errorf("order = %+v, want 2 tickets for 1000 UAH linking to the order page", first)
	}

	in.Quantity = 3
	_, err = f.tools.BookTicket(ctx, f.buyer, in)
	wantCode(t, err, CodeKeyReused)

	in.Quantity = 2
	other, err := f.tools.BookTicket(ctx, mustUser(t, f.pool, "someone-else"), in)
	if err != nil {
		t.Fatal(err)
	}
	if other.OrderID == first.OrderID {
		t.Fatal("another user's booking with the same key returned the first user's order")
	}
}

func TestBookTicketRaceForTheLastSeatHasOneWinner(t *testing.T) {
	f := newFixture(t)
	tier := f.event(t, "last-seat", "Kyiv", events.StatusPublished, 1)
	const buyers = 4
	errs := make([]error, buyers)
	var wg sync.WaitGroup
	for i := range buyers {
		user := mustUser(t, f.pool, "racer-"+itoa(int64(i)))
		wg.Go(func() {
			_, errs[i] = f.tools.BookTicket(context.Background(), user, BookTicketInput{
				EventSlug: "last-seat", TierID: tier.ID, Quantity: 1, IdempotencyKey: "race-key-" + itoa(int64(i)),
			})
		})
	}
	wg.Wait()
	wins := 0
	for _, err := range errs {
		if err == nil {
			wins++
			continue
		}
		wantCode(t, err, CodeSoldOut)
	}
	if wins != 1 || f.orderCount(t) != 1 {
		t.Fatalf("wins = %d, orders = %d, want exactly one", wins, f.orderCount(t))
	}
}

func TestCheckBookingValidatesAgainstTheDatabaseAndBuysNothing(t *testing.T) {
	f := newFixture(t)
	tier := f.event(t, "big-show", "Kyiv", events.StatusPublished, 3)
	other := f.event(t, "other-show", "Kyiv", events.StatusPublished, 3)
	cancelled := f.event(t, "cancelled-show", "Kyiv", events.StatusCancelled, 3)
	ctx := context.Background()

	b, err := f.tools.CheckBooking(ctx, BookTicketInput{EventSlug: "big-show", TierID: tier.ID, Quantity: 2})
	if err != nil {
		t.Fatal(err)
	}
	if b.TotalUAH != 1000 || b.TierName != "Standard" || b.EventName != "Show big-show" || b.BuyURL != "https://tixo.test/events/big-show/buy" {
		t.Errorf("booking = %+v, want 2 x Standard for 1000 UAH posting to the landing buy form", b)
	}

	for _, tc := range []struct {
		name string
		in   BookTicketInput
		code string
	}{
		{"invented tier", BookTicketInput{EventSlug: "big-show", TierID: other.ID, Quantity: 1}, CodeInvalid},
		{"more than left", BookTicketInput{EventSlug: "big-show", TierID: tier.ID, Quantity: 4}, CodeSoldOut},
		{"cancelled", BookTicketInput{EventSlug: "cancelled-show", TierID: cancelled.ID, Quantity: 1}, CodeNotOnSale},
		{"invented slug", BookTicketInput{EventSlug: "nope", TierID: tier.ID, Quantity: 1}, CodeNotFound},
		{"zero tickets", BookTicketInput{EventSlug: "big-show", TierID: tier.ID, Quantity: 0}, CodeInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.tools.CheckBooking(ctx, tc.in)
			wantCode(t, err, tc.code)
		})
	}
	if n := f.orderCount(t); n != 0 {
		t.Fatalf("CheckBooking created %d orders", n)
	}
}

func TestReadToolsShowPublicEventsOnly(t *testing.T) {
	f := newFixture(t)
	f.event(t, "kyiv-rock", "Kyiv", events.StatusPublished, 5)
	f.event(t, "lviv-rock", "Lviv", events.StatusPublished, 5)
	f.event(t, "secret-draft", "Kyiv", events.StatusDraft, 5)
	f.event(t, "called-off", "Kyiv", events.StatusCancelled, 5)
	ctx := context.Background()

	found, err := f.tools.SearchEvents(ctx, SearchEventsInput{Query: "rock"})
	if err != nil {
		t.Fatal(err)
	}
	if len(found.Events) != 2 || found.Events[0].PriceFromUAH == nil || *found.Events[0].PriceFromUAH != 500 {
		t.Fatalf("search = %+v, want the two published rock shows priced from 500 UAH", found.Events)
	}

	_, err = f.tools.SearchEvents(ctx, SearchEventsInput{DateFrom: "tomorrow"})
	wantCode(t, err, CodeInvalid)
	if !strings.Contains(err.Error(), "date_from") {
		t.Errorf("message %q does not name the date_from input", err)
	}

	_, err = f.tools.GetEvent(ctx, GetEventInput{Slug: "secret-draft"})
	wantCode(t, err, CodeNotFound)
	off, err := f.tools.GetEvent(ctx, GetEventInput{Slug: "called-off"})
	if err != nil {
		t.Fatal(err)
	}
	if off.OnSale || off.Status != events.StatusCancelled || len(off.Tiers) != 1 {
		t.Errorf("cancelled event = %+v, want visible, not on sale, with its tier", off)
	}

	lviv, err := f.tools.ListVenues(ctx, ListVenuesInput{City: "lviv"})
	if err != nil {
		t.Fatal(err)
	}
	if len(lviv.Venues) != 1 || lviv.Venues[0].Name != "lviv-rock hall" || lviv.Venues[0].UpcomingEvents != 1 {
		t.Fatalf("venues in Lviv = %+v, want only the Lviv hall with one event", lviv.Venues)
	}
	all, err := f.tools.ListVenues(ctx, ListVenuesInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Venues) != 2 {
		t.Errorf("venues = %d, want 2: draft and cancelled events do not list their halls", len(all.Venues))
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
