package seed

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/KotunOleg/Tixo/internal/db/dbtest"
	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/events"
)

var seedNow = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

func TestSeedTwiceGivesTheSameCounts(t *testing.T) {
	pool := dbtest.Pool(t)
	ctx := context.Background()

	first, err := Run(ctx, pool, seedNow)
	if err != nil {
		t.Fatal(err)
	}
	want := Report{
		Organizers: int64(len(organizers)), Users: int64(len(organizers)), Venues: int64(len(venues)),
		Events: int64(len(demoEvents)), Artists: int64(len(artists)),
	}
	for _, e := range demoEvents {
		want.Tiers += int64(len(e.tiers))
		want.Lineup += int64(len(e.lineup))
	}
	if first != want {
		t.Errorf("first run = %v, want %v", first, want)
	}

	second, err := Run(ctx, pool, seedNow.Add(48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Errorf("second run = %v, want the same counts as the first %v", second, first)
	}
}

func TestSeedLeavesUserDataAlone(t *testing.T) {
	pool := dbtest.Pool(t)
	ctx := context.Background()
	q := dbgen.New(pool)

	mine, err := q.CreateOrganizer(ctx, dbgen.CreateOrganizerParams{Slug: "my-org", Name: "Mine", Contacts: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	hall, err := q.CreateVenue(ctx, dbgen.CreateVenueParams{OrganizerID: mine.ID, Name: "My hall", City: "Odesa", Lat: 46.48, Lng: 30.72, Capacity: 50})
	if err != nil {
		t.Fatal(err)
	}
	taken, err := q.CreateEvent(ctx, dbgen.CreateEventParams{
		OrganizerID: mine.ID, VenueID: hall.ID, Slug: demoEvents[0].slug, Title: "My own show",
		StartsAt: seedNow, EndsAt: seedNow.Add(time.Hour), Genre: "jazz",
	})
	if err != nil {
		t.Fatal(err)
	}

	rep, err := Run(ctx, pool, seedNow)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Events != int64(len(demoEvents)) {
		t.Errorf("events = %d, want %d: the seed event with a taken slug is skipped, the user's stays", rep.Events, len(demoEvents))
	}

	got, err := q.GetEventForOrganizer(ctx, dbgen.GetEventForOrganizerParams{ID: taken.ID, OrganizerID: mine.ID})
	if err != nil || got.Title != "My own show" || got.VenueID != hall.ID {
		t.Errorf("user's event after seed = %+v, %v; want untouched", got, err)
	}
	lineup, err := q.ListEventArtists(ctx, taken.ID)
	if err != nil || len(lineup) != 0 {
		t.Errorf("seed added %d artists to the user's event (%v)", len(lineup), err)
	}
}

func TestSeedEventsUseKnownGenres(t *testing.T) {
	for _, e := range demoEvents {
		if !events.IsGenre(e.genre) {
			t.Errorf("seed event %s has genre %q outside events.Genres()", e.slug, e.genre)
		}
	}
}

func TestSeedHasPastAndUpcomingPublishedEvents(t *testing.T) {
	pool := dbtest.Pool(t)
	ctx := context.Background()
	if _, err := Run(ctx, pool, seedNow); err != nil {
		t.Fatal(err)
	}
	q := dbgen.New(pool)

	upcoming, err := q.ListUpcomingPublishedEvents(ctx, dbgen.ListUpcomingPublishedEventsParams{After: seedNow, Lim: 100})
	if err != nil {
		t.Fatal(err)
	}
	all, err := q.ListUpcomingPublishedEvents(ctx, dbgen.ListUpcomingPublishedEventsParams{After: seedNow.AddDate(-1, 0, 0), Lim: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(upcoming) < 3 || len(all) <= len(upcoming) {
		t.Errorf("upcoming = %d, all published = %d; want several upcoming and at least one past event", len(upcoming), len(all))
	}
}
