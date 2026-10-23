package analytics

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/KotunOleg/Tixo/internal/db/dbtest"
	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/events"
	"github.com/KotunOleg/Tixo/internal/features"
	"github.com/KotunOleg/Tixo/internal/organizers"
	"github.com/KotunOleg/Tixo/internal/tiers"
	"github.com/KotunOleg/Tixo/internal/venues"
)

var fixedNow = time.Date(2026, 10, 27, 12, 0, 0, 0, kyiv)

type fixture struct {
	t     *testing.T
	pool  *pgxpool.Pool
	svc   *Service
	buyer int64
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	pool := dbtest.Pool(t)
	buyer, err := dbgen.New(pool).CreateUser(context.Background(), dbgen.CreateUserParams{Email: "buyer@example.com", Name: "Buyer"})
	if err != nil {
		t.Fatal(err)
	}
	svc := New(pool, features.New(pool))
	svc.now = func() time.Time { return fixedNow }
	return fixture{t: t, pool: pool, svc: svc, buyer: buyer.ID}
}

func (f fixture) organizer(slug, plan string) organizers.Membership {
	f.t.Helper()
	ctx := context.Background()
	owner, err := dbgen.New(f.pool).CreateUser(ctx, dbgen.CreateUserParams{Email: slug + "@example.com", Name: slug})
	if err != nil {
		f.t.Fatal(err)
	}
	org, err := organizers.New(f.pool).Create(ctx, owner.ID, organizers.CreateInput{Name: slug, Slug: slug})
	if err != nil {
		f.t.Fatal(err)
	}
	org, err = dbgen.New(f.pool).SetOrganizerPlan(ctx, dbgen.SetOrganizerPlanParams{ID: org.ID, Plan: plan})
	if err != nil {
		f.t.Fatal(err)
	}
	return organizers.Membership{Organizer: org, Role: organizers.RoleOwner}
}

type testEvent struct {
	id   int64
	tier int64
}

func (f fixture) event(m organizers.Membership, slug, genre, status string) testEvent {
	f.t.Helper()
	ctx := context.Background()
	orgID := m.Organizer.ID
	v, err := venues.New(f.pool).Create(ctx, orgID, venues.Input{
		Name: slug + " venue", Address: "1 Test St", City: "Kyiv", Lat: 50.45, Lng: 30.52, Capacity: 1000,
	})
	if err != nil {
		f.t.Fatal(err)
	}
	start := time.Now().Add(30 * 24 * time.Hour).Truncate(time.Second)
	ev, err := events.New(f.pool).Create(ctx, orgID, events.Input{
		VenueID: v.ID, Slug: slug, Title: "Show " + slug, Genre: genre, StartsAt: start, EndsAt: start.Add(2 * time.Hour),
	})
	if err != nil {
		f.t.Fatal(err)
	}
	tier, err := tiers.New(f.pool).Create(ctx, orgID, ev.ID, tiers.Input{Name: "Standard", Price: 50000, Quantity: 500})
	if err != nil {
		f.t.Fatal(err)
	}
	if status != events.StatusDraft {
		if _, err := events.New(f.pool).SetStatus(ctx, orgID, ev.ID, status); err != nil {
			f.t.Fatal(err)
		}
	}
	return testEvent{id: ev.ID, tier: tier.ID}
}

func (f fixture) order(ev testEvent, qty int32, status string, at time.Time) {
	f.t.Helper()
	_, err := f.pool.Exec(context.Background(), `
		INSERT INTO orders (user_id, event_id, tier_id, quantity, unit_price, total, status, idempotency_key, created_at)
		VALUES ($1, $2, $3, $4, 50000, $4 * 50000, $5, gen_random_uuid(), $6)`,
		f.buyer, ev.id, ev.tier, qty, status, at)
	if err != nil {
		f.t.Fatal(err)
	}
}

func (f fixture) report(m organizers.Membership) Report {
	f.t.Helper()
	r, err := f.svc.Report(context.Background(), m, DefaultDays)
	if err != nil {
		f.t.Fatal(err)
	}
	return r
}

func dayOf(r Report, date string) Day {
	for _, d := range r.ByDay {
		if d.Date.Format(dayKey) == date {
			return d
		}
	}
	return Day{}
}

func TestOnlyPaidAndFulfilledOrdersCountAsSales(t *testing.T) {
	cases := []struct {
		status      string
		wantTickets int64
	}{
		{"paid", 2},
		{"fulfilled", 2},
		{"cancelled", 0},
		{"pending", 0},
	}
	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			f := newFixture(t)
			m := f.organizer("kyiv-live", "premium")
			ev := f.event(m, "big-show", events.GenreRock, events.StatusPublished)
			f.order(ev, 2, tc.status, fixedNow.Add(-time.Hour))

			r := f.report(m)
			if r.Tickets != tc.wantTickets || r.Revenue != tc.wantTickets*50000 {
				t.Errorf("KPI = %d tickets, %d revenue; want %d tickets", r.Tickets, r.Revenue, tc.wantTickets)
			}
			if got := dayOf(r, "2026-10-27").Tickets; got != tc.wantTickets {
				t.Errorf("today's tickets = %d, want %d", got, tc.wantTickets)
			}
			if tc.wantTickets == 0 && len(r.ByEvent) != 0 {
				t.Errorf("ByEvent = %+v, want empty", r.ByEvent)
			}
		})
	}
}

func TestDaysAreCountedInKyivTimeAndHaveNoGaps(t *testing.T) {
	f := newFixture(t)
	m := f.organizer("kyiv-live", "premium")
	ev := f.event(m, "big-show", events.GenreRock, events.StatusPublished)
	f.order(ev, 1, "paid", time.Date(2026, 10, 26, 22, 30, 0, 0, time.UTC))
	f.order(ev, 3, "paid", time.Date(2026, 9, 27, 21, 0, 0, 0, time.UTC))
	f.order(ev, 5, "paid", time.Date(2026, 9, 27, 20, 59, 0, 0, time.UTC))
	f.order(ev, 7, "paid", time.Date(2026, 10, 27, 22, 0, 0, 0, time.UTC))

	r := f.report(m)
	if len(r.ByDay) != DefaultDays {
		t.Fatalf("ByDay has %d days, want %d", len(r.ByDay), DefaultDays)
	}
	if first, last := r.ByDay[0].Date.Format(dayKey), r.ByDay[DefaultDays-1].Date.Format(dayKey); first != "2026-09-28" || last != "2026-10-27" {
		t.Errorf("window = %s..%s, want 2026-09-28..2026-10-27", first, last)
	}
	cases := []struct {
		name string
		day  string
		want int64
	}{
		{"00:30 Kyiv belongs to the Kyiv day, not the UTC one", "2026-10-27", 1},
		{"UTC day before stays empty", "2026-10-26", 0},
		{"first Kyiv midnight of the window is included", "2026-09-28", 3},
		{"a day without sales is a zero, not a gap", "2026-10-10", 0},
	}
	for _, tc := range cases {
		if got := dayOf(r, tc.day).Tickets; got != tc.want {
			t.Errorf("%s: %s tickets = %d, want %d", tc.name, tc.day, got, tc.want)
		}
	}
	if r.Tickets != 4 {
		t.Errorf("KPI tickets = %d, want 4 (orders before the window and after today excluded)", r.Tickets)
	}
}

func TestReportNeverIncludesAnotherOrganizersData(t *testing.T) {
	f := newFixture(t)
	mine := f.organizer("kyiv-live", "premium")
	theirs := f.organizer("odesa-jazz", "premium")
	myEvent := f.event(mine, "my-show", events.GenreRock, events.StatusPublished)
	theirEvent := f.event(theirs, "their-show", events.GenreJazz, events.StatusPublished)
	f.event(theirs, "their-draft", events.GenreJazz, events.StatusDraft)
	f.order(myEvent, 1, "paid", fixedNow.Add(-time.Hour))
	f.order(theirEvent, 9, "paid", fixedNow.Add(-time.Hour))

	r := f.report(mine)
	if r.Tickets != 1 || r.PublishedEvents != 1 {
		t.Errorf("KPI = %d tickets, %d published; want 1, 1", r.Tickets, r.PublishedEvents)
	}
	if len(r.ByEvent) != 1 || r.ByEvent[0].ID != myEvent.id {
		t.Errorf("ByEvent = %+v, want only my event", r.ByEvent)
	}
	if len(r.ByGenre) != 1 || r.ByGenre[0] != (GenreCount{Genre: events.GenreRock, Events: 1}) {
		t.Errorf("ByGenre = %+v, want only rock=1", r.ByGenre)
	}
}

func TestEventsBeyondTopTenFoldIntoOthers(t *testing.T) {
	f := newFixture(t)
	m := f.organizer("kyiv-live", "premium")
	for i := range TopEvents + 2 {
		ev := f.event(m, fmt.Sprintf("show-%02d", i), events.GenreRock, events.StatusPublished)
		f.order(ev, int32(min(10, TopEvents+2-i)), "paid", fixedNow.Add(-time.Hour))
	}

	r := f.report(m)
	if len(r.ByEvent) != TopEvents+1 {
		t.Fatalf("ByEvent has %d rows, want %d", len(r.ByEvent), TopEvents+1)
	}
	if r.ByEvent[0].Tickets != 10 || r.ByEvent[0].Title != "Show show-00" || r.ByEvent[0].IsOthers() {
		t.Errorf("first row = %+v, want best seller", r.ByEvent[0])
	}
	others := r.ByEvent[TopEvents]
	if !others.IsOthers() || others.OthersCount != 2 || others.Tickets != 2+1 {
		t.Errorf("others = %+v, want 2 events with 3 tickets", others)
	}
	var sum int64
	for _, e := range r.ByEvent {
		sum += e.Tickets
	}
	if sum != r.Tickets {
		t.Errorf("ByEvent sums to %d, KPI says %d", sum, r.Tickets)
	}
}

func TestGenresSkipCancelledEvents(t *testing.T) {
	f := newFixture(t)
	m := f.organizer("kyiv-live", "premium")
	f.event(m, "rock-1", events.GenreRock, events.StatusPublished)
	f.event(m, "rock-2", events.GenreRock, events.StatusDraft)
	f.event(m, "jazz-1", events.GenreJazz, events.StatusPublished)
	f.event(m, "jazz-2", events.GenreJazz, events.StatusCancelled)

	r := f.report(m)
	want := []GenreCount{{events.GenreRock, 2}, {events.GenreJazz, 1}}
	if fmt.Sprint(r.ByGenre) != fmt.Sprint(want) {
		t.Errorf("ByGenre = %+v, want %+v", r.ByGenre, want)
	}
	if r.PublishedEvents != 2 {
		t.Errorf("PublishedEvents = %d, want 2", r.PublishedEvents)
	}
}

func TestEmptyOrganizerGetsZeroDaysNotAnError(t *testing.T) {
	f := newFixture(t)
	r := f.report(f.organizer("kyiv-live", "premium"))
	if r.HasSales() || len(r.ByDay) != DefaultDays || len(r.ByEvent) != 0 || len(r.ByGenre) != 0 {
		t.Errorf("empty report = %+v", r)
	}
}

func TestReportIsRefusedWithoutTheFeature(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	free := f.organizer("lviv-sound", "free")
	premium := f.organizer("kyiv-live", "premium")

	if _, err := f.svc.Report(ctx, free, DefaultDays); !errors.Is(err, ErrFeatureDisabled) {
		t.Errorf("free organizer: err = %v, want ErrFeatureDisabled", err)
	}
	if err := features.New(f.pool).Enable(ctx, free.Organizer.ID, features.SalesAnalytics); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Report(ctx, free, DefaultDays); err != nil {
		t.Errorf("free organizer with override: err = %v", err)
	}
	for _, days := range []int{0, -1, MaxDays + 1} {
		if _, err := f.svc.Report(ctx, premium, days); !errors.Is(err, ErrInvalidDays) {
			t.Errorf("days=%d: err = %v, want ErrInvalidDays", days, err)
		}
	}
}
