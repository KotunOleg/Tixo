package events

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"testing"

	"github.com/KotunOleg/Tixo/internal/db/dbtest"
	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
)

func searchSlugs(results []SearchResult) []string {
	slugs := make([]string, 0, len(results))
	for _, r := range results {
		slugs = append(slugs, r.Slug)
	}
	slices.Sort(slugs)
	return slugs
}

func TestSearchFindsUpcomingPublishedEventsByTextAndFilters(t *testing.T) {
	pool := dbtest.Pool(t)
	ctx := context.Background()
	seedMap(t, pool, []mapSeedEvent{
		{slug: "rock-night", venue: "Atlas", genre: GenreRock, status: StatusPublished, days: 3, prices: []int64{50000}},
		{slug: "jazz-evening", venue: "Bel Etage", genre: GenreJazz, status: StatusPublished, days: 8, prices: []int64{120000}},
		{slug: "lviv-folk", venue: "Lviv Opera", genre: GenreFolk, status: StatusPublished, days: 12},
		{slug: "rock-draft", venue: "Atlas", genre: GenreRock, status: StatusDraft, days: 3},
		{slug: "rock-past", venue: "Atlas", genre: GenreRock, status: StatusPublished, days: -2},
		{slug: "rock-cancelled", venue: "Atlas", genre: GenreRock, status: StatusCancelled, days: 4},
		{slug: "percent-100", venue: "Atlas", genre: GenrePop, status: StatusPublished, days: 5},
	})
	if _, err := pool.Exec(ctx, "UPDATE venues SET city = 'Lviv' WHERE name = 'Lviv Opera'"); err != nil {
		t.Fatal(err)
	}
	q := dbgen.New(pool)
	artist, err := q.CreateArtist(ctx, dbgen.CreateArtistParams{Name: "Okean Elzy"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO event_artists (event_id, artist_id) SELECT id, $1 FROM events WHERE slug = 'jazz-evening'", artist.ID); err != nil {
		t.Fatal(err)
	}
	svc := New(pool)

	for _, tc := range []struct {
		name  string
		query SearchQuery
		want  []string
	}{
		{"no filters lists only upcoming published", SearchQuery{}, []string{"jazz-evening", "lviv-folk", "percent-100", "rock-night"}},
		{"title is case insensitive", SearchQuery{Text: "ROCK"}, []string{"rock-night"}},
		{"artist name in the lineup", SearchQuery{Text: "okean"}, []string{"jazz-evening"}},
		{"venue name", SearchQuery{Text: "bel etage"}, []string{"jazz-evening"}},
		{"city as text", SearchQuery{Text: "lviv"}, []string{"lviv-folk"}},
		{"city filter", SearchQuery{City: "Kyiv"}, []string{"jazz-evening", "percent-100", "rock-night"}},
		{"like wildcards in text are literal", SearchQuery{Text: "%"}, nil},
		{"genre", SearchQuery{Genre: GenreJazz}, []string{"jazz-evening"}},
		{"max price in uah", SearchQuery{MaxPrice: "600"}, []string{"rock-night"}},
		{"date range", SearchQuery{From: "2030-05-07", To: "2030-05-13"}, []string{"jazz-evening", "lviv-folk"}},
		{"text and filters combine with and", SearchQuery{Text: "rock", Genre: GenreJazz}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := svc.Search(ctx, mapNow, tc.query)
			if err != nil {
				t.Fatal(err)
			}
			if slugs := searchSlugs(got); !slices.Equal(slugs, tc.want) {
				t.Errorf("Search(%+v) = %v, want %v", tc.query, slugs, tc.want)
			}
		})
	}
}

func TestSearchReturnsVenueAndCheapestPriceSortedByDate(t *testing.T) {
	pool := dbtest.Pool(t)
	seedMap(t, pool, []mapSeedEvent{
		{slug: "later", venue: "Atlas", genre: GenreRock, status: StatusPublished, days: 9, prices: []int64{90000, 40000}},
		{slug: "sooner", venue: "Atlas", genre: GenreRock, status: StatusPublished, days: 2},
	})
	got, err := New(pool).Search(context.Background(), mapNow, SearchQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Slug != "sooner" || got[1].Slug != "later" {
		t.Fatalf("Search order = %v, want sooner then later", searchSlugs(got))
	}
	if got[0].PriceFrom != nil {
		t.Errorf("event without tiers has price %d, want none", *got[0].PriceFrom)
	}
	if got[1].PriceFrom == nil || *got[1].PriceFrom != 40000 || got[1].VenueName != "Atlas" || got[1].VenueCity != "Kyiv" {
		t.Errorf("later = %+v, want price 40000 at Atlas, Kyiv", got[1])
	}
}

func TestSearchCapsTheResultCount(t *testing.T) {
	pool := dbtest.Pool(t)
	seed := make([]mapSeedEvent, 0, SearchMaxLimit+3)
	for i := range SearchMaxLimit + 3 {
		seed = append(seed, mapSeedEvent{slug: "show-" + strconv.Itoa(i), venue: "Atlas", genre: GenreRock, status: StatusPublished, days: i + 1})
	}
	seedMap(t, pool, seed)
	svc := New(pool)

	for _, tc := range []struct {
		limit, want int
	}{
		{0, SearchDefaultLimit},
		{3, 3},
		{SearchMaxLimit + 50, SearchMaxLimit},
	} {
		got, err := svc.Search(context.Background(), mapNow, SearchQuery{Limit: tc.limit})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != tc.want {
			t.Errorf("limit %d returned %d events, want %d", tc.limit, len(got), tc.want)
		}
	}
}

func TestSearchRejectsInvalidFilters(t *testing.T) {
	long := make([]byte, searchTextMaxLen+1)
	for i := range long {
		long[i] = 'a'
	}
	for _, tc := range []struct {
		name  string
		query SearchQuery
		field string
	}{
		{"unknown genre", SearchQuery{Genre: "noise"}, FieldGenre},
		{"bad date", SearchQuery{From: "next week"}, FieldFrom},
		{"to before from", SearchQuery{From: "2030-05-10", To: "2030-05-01"}, FieldTo},
		{"negative price", SearchQuery{MaxPrice: "-1"}, FieldMaxPrice},
		{"negative limit", SearchQuery{Limit: -1}, FieldLimit},
		{"text too long", SearchQuery{Text: string(long)}, FieldQuery},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(nil).Search(context.Background(), mapNow, tc.query)
			var verr *ValidationError
			if !errors.As(err, &verr) || verr.Fields[tc.field] == "" {
				t.Fatalf("Search(%+v) error = %v, want a validation error on %s", tc.query, err, tc.field)
			}
		})
	}
}
