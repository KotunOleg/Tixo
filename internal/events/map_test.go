package events

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/KotunOleg/Tixo/internal/db/dbtest"
	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
)

var mapNow = time.Date(2030, 5, 1, 12, 0, 0, 0, time.UTC)

type mapSeedEvent struct {
	slug, venue, genre, status string
	days                       int
	prices                     []int64
}

func seedMap(t *testing.T, pool *pgxpool.Pool, events []mapSeedEvent) {
	t.Helper()
	ctx := context.Background()
	svc := New(pool)
	q := dbgen.New(pool)
	org := mustOrganizer(t, pool, "kyiv-live")
	venueIDs := map[string]int64{}
	for _, e := range events {
		if _, ok := venueIDs[e.venue]; !ok {
			venueIDs[e.venue] = mustVenue(t, pool, org, e.venue)
		}
		start := mapNow.AddDate(0, 0, e.days)
		created, err := svc.Create(ctx, org, Input{
			VenueID: venueIDs[e.venue], Slug: e.slug, Title: e.slug, StartsAt: start, EndsAt: start.Add(2 * time.Hour), Genre: e.genre,
		})
		if err != nil {
			t.Fatal(err)
		}
		if e.status != StatusDraft {
			if _, err := svc.SetStatus(ctx, org, created.ID, e.status); err != nil {
				t.Fatal(err)
			}
		}
		for i, price := range e.prices {
			_, err := q.CreateTicketTier(ctx, dbgen.CreateTicketTierParams{
				EventID: created.ID, OrganizerID: org, Name: string(rune('A' + i)), Price: price, Quantity: 10,
			})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}

func mapSlugs(venues []MapVenue) []string {
	var slugs []string
	for _, v := range venues {
		for _, e := range v.Events {
			slugs = append(slugs, e.Slug)
		}
	}
	slices.Sort(slugs)
	return slugs
}

func TestMapShowsOnlyUpcomingPublishedEventsMatchingEveryFilter(t *testing.T) {
	pool := dbtest.Pool(t)
	seedMap(t, pool, []mapSeedEvent{
		{slug: "rock-a", venue: "Hall A", genre: GenreRock, status: StatusPublished, days: 10, prices: []int64{50000, 90000}},
		{slug: "pop-a", venue: "Hall A", genre: GenrePop, status: StatusPublished, days: 20},
		{slug: "jazz-b", venue: "Hall B", genre: GenreJazz, status: StatusPublished, days: 40, prices: []int64{0}},
		{slug: "metal-b", venue: "Hall B", genre: GenreMetal, status: StatusPublished, days: 5, prices: []int64{200000}},
		{slug: "draft-a", venue: "Hall A", genre: GenreRock, status: StatusDraft, days: 10, prices: []int64{100}},
		{slug: "cancelled-a", venue: "Hall A", genre: GenreRock, status: StatusCancelled, days: 10, prices: []int64{100}},
		{slug: "past-a", venue: "Hall A", genre: GenreRock, status: StatusPublished, days: -3, prices: []int64{100}},
		{slug: "folk-c", venue: "Hall C", genre: GenreFolk, status: StatusDraft, days: 10},
	})
	svc := New(pool)

	for _, tc := range []struct {
		name  string
		query MapQuery
		want  []string
	}{
		{"no filters", MapQuery{}, []string{"jazz-b", "metal-b", "pop-a", "rock-a"}},
		{"genre", MapQuery{Genre: GenreRock}, []string{"rock-a"}},
		{"max price in UAH against the cheapest tier", MapQuery{MaxPrice: "500"}, []string{"jazz-b", "rock-a"}},
		{"zero means free only", MapQuery{MaxPrice: "0"}, []string{"jazz-b"}},
		{"kyiv date range overlaps the event", MapQuery{From: "2030-05-10", To: "2030-05-21"}, []string{"pop-a", "rock-a"}},
		{"to alone ends at the end of that kyiv day", MapQuery{To: "2030-05-06"}, []string{"metal-b"}},
		{"from in the past still hides past events", MapQuery{From: "2030-01-01", Genre: GenreRock}, []string{"rock-a"}},
		{"filters combine with and", MapQuery{Genre: GenreJazz, MaxPrice: "100", To: "2030-05-30"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := svc.Map(context.Background(), mapNow, tc.query)
			if err != nil {
				t.Fatal(err)
			}
			if slugs := mapSlugs(got); !slices.Equal(slugs, tc.want) {
				t.Errorf("Map(%+v) = %v, want %v", tc.query, slugs, tc.want)
			}
			for _, v := range got {
				if len(v.Events) == 0 {
					t.Errorf("venue %s is in the result without events", v.Name)
				}
			}
		})
	}
}

func TestMapGroupsEventsByVenueWithPriceFromTheCheapestTierOnSale(t *testing.T) {
	pool := dbtest.Pool(t)
	seedMap(t, pool, []mapSeedEvent{
		{slug: "sold-out-cheap", venue: "Hall A", genre: GenreRock, status: StatusPublished, days: 3, prices: []int64{30000, 80000}},
		{slug: "no-tiers", venue: "Hall A", genre: GenreRock, status: StatusPublished, days: 4},
		{slug: "other-hall", venue: "Hall B", genre: GenreRock, status: StatusPublished, days: 2, prices: []int64{10000}},
	})
	if _, err := pool.Exec(context.Background(), "UPDATE ticket_tiers SET available = 0 WHERE price = 30000"); err != nil {
		t.Fatal(err)
	}

	got, err := New(pool).Map(context.Background(), mapNow, MapQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("venues = %d, want 2", len(got))
	}
	byVenue := map[string]MapVenue{}
	for _, v := range got {
		byVenue[v.Name] = v
	}
	hall := byVenue["Hall A"]
	if len(hall.Events) != 2 || hall.City != "Kyiv" || hall.Lat == 0 || hall.Lng == 0 {
		t.Fatalf("Hall A = %+v, want 2 events and the venue's city and coordinates", hall)
	}
	prices := map[string]*int64{}
	for _, e := range hall.Events {
		prices[e.Slug] = e.PriceFrom
	}
	if p := prices["sold-out-cheap"]; p == nil || *p != 80000 {
		t.Errorf("price from with the cheapest tier sold out = %v, want 80000 like the landing page", p)
	}
	if p := prices["no-tiers"]; p != nil {
		t.Errorf("price from without tiers = %d, want none", *p)
	}
}

func TestMapCapsEventsPerVenueAndCountsTheRest(t *testing.T) {
	pool := dbtest.Pool(t)
	var many []mapSeedEvent
	for i := range 7 {
		many = append(many, mapSeedEvent{slug: "show-" + string(rune('a'+i)), venue: "Busy Hall", genre: GenreRock, status: StatusPublished, days: 7 - i})
	}
	seedMap(t, pool, many)

	got, err := New(pool).Map(context.Background(), mapNow, MapQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Events) != MapEventsPerVenue || got[0].More != 2 {
		t.Fatalf("Map = %+v, want one venue with %d events and 2 more", got, MapEventsPerVenue)
	}
	if first := got[0].Events[0]; first.Slug != "show-g" {
		t.Errorf("first event = %s, want the soonest show-g", first.Slug)
	}
}

func TestMapRejectsInvalidFilters(t *testing.T) {
	svc := &Service{}
	for _, tc := range []struct {
		query MapQuery
		field string
		code  string
	}{
		{MapQuery{From: "2030-13-01"}, FieldFrom, CodeFormat},
		{MapQuery{To: "01.05.2030"}, FieldTo, CodeFormat},
		{MapQuery{From: "2030-05-10", To: "2030-05-09"}, FieldTo, CodeRange},
		{MapQuery{Genre: "disco"}, FieldGenre, CodeInvalid},
		{MapQuery{MaxPrice: "-1"}, FieldMaxPrice, CodeRange},
		{MapQuery{MaxPrice: "abc"}, FieldMaxPrice, CodeFormat},
		{MapQuery{MaxPrice: "1.5"}, FieldMaxPrice, CodeFormat},
		{MapQuery{MaxPrice: "99999999999999999999"}, FieldMaxPrice, CodeFormat},
		{MapQuery{MaxPrice: "9223372036854775807"}, FieldMaxPrice, CodeRange},
	} {
		_, err := svc.Map(context.Background(), mapNow, tc.query)
		var verr *ValidationError
		if !errors.As(err, &verr) || verr.Fields[tc.field] != tc.code {
			t.Errorf("Map(%+v) err = %v, want %s=%s", tc.query, err, tc.field, tc.code)
		}
	}
}
