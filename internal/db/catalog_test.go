package db_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
)

var demoStart = time.Date(2030, 6, 1, 19, 0, 0, 0, time.UTC)

func mustVenue(t *testing.T, q *dbgen.Queries, organizerID int64, name string) dbgen.Venue {
	t.Helper()
	v, err := q.CreateVenue(context.Background(), dbgen.CreateVenueParams{
		OrganizerID: organizerID, Name: name, Address: "vul. Khreshchatyk 1", City: "Kyiv",
		Lat: 50.45, Lng: 30.52, Capacity: 500,
	})
	if err != nil {
		t.Fatalf("create venue %s: %v", name, err)
	}
	return v
}

func mustSharedVenue(t *testing.T, pool *pgxpool.Pool, name string) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(),
		`INSERT INTO venues (name, address, city, lat, lng, capacity) VALUES ($1, '', 'Lviv', 49.84, 24.03, 1000) RETURNING id`,
		name).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func eventParams(organizerID, venueID int64, slug string) dbgen.CreateEventParams {
	return dbgen.CreateEventParams{
		OrganizerID: organizerID, VenueID: venueID, Slug: slug, Title: slug,
		StartsAt: demoStart, EndsAt: demoStart.Add(3 * time.Hour), Genre: "rock",
	}
}

func mustEvent(t *testing.T, q *dbgen.Queries, organizerID, venueID int64, slug string) dbgen.Event {
	t.Helper()
	e, err := q.CreateEvent(context.Background(), eventParams(organizerID, venueID, slug))
	if err != nil {
		t.Fatalf("create event %s: %v", slug, err)
	}
	return e
}

func updateParams(e dbgen.Event, organizerID int64) dbgen.UpdateEventParams {
	return dbgen.UpdateEventParams{
		ID: e.ID, OrganizerID: organizerID, VenueID: e.VenueID, Slug: e.Slug, Title: "hijacked",
		Description: e.Description, StartsAt: e.StartsAt, EndsAt: e.EndsAt, Genre: e.Genre,
	}
}

func TestOrganizerCannotTouchAnotherOrganizersEvent(t *testing.T) {
	_, q := newQueries(t)
	ctx := context.Background()
	a := mustOrganizer(t, q, "org-a")
	b := mustOrganizer(t, q, "org-b")
	ev := mustEvent(t, q, a.ID, mustVenue(t, q, a.ID, "A hall").ID, "a-show")

	_, err := q.GetEventForOrganizer(ctx, dbgen.GetEventForOrganizerParams{ID: ev.ID, OrganizerID: b.ID})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("B reads A's event: err = %v, want no rows", err)
	}
	_, err = q.UpdateEvent(ctx, updateParams(ev, b.ID))
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("B updates A's event: err = %v, want no rows", err)
	}
	_, err = q.SetEventStatus(ctx, dbgen.SetEventStatusParams{ID: ev.ID, OrganizerID: b.ID, Status: "cancelled"})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("B cancels A's event: err = %v, want no rows", err)
	}
	n, err := q.DeleteEvent(ctx, dbgen.DeleteEventParams{ID: ev.ID, OrganizerID: b.ID})
	if err != nil || n != 0 {
		t.Errorf("B deletes A's event: rows = %d, err = %v; want 0 rows", n, err)
	}
	list, err := q.ListEventsForOrganizer(ctx, dbgen.ListEventsForOrganizerParams{OrganizerID: b.ID, Lim: 10})
	if err != nil || len(list) != 0 {
		t.Errorf("B's event list = %d items, err = %v; want empty", len(list), err)
	}

	got, err := q.GetEventForOrganizer(ctx, dbgen.GetEventForOrganizerParams{ID: ev.ID, OrganizerID: a.ID})
	if err != nil || got.Title != ev.Title || got.Status != "draft" {
		t.Errorf("A's event after B's attempts = %+v, %v; want unchanged", got, err)
	}
}

func TestOrganizerCannotTouchAnotherOrganizersVenue(t *testing.T) {
	pool, q := newQueries(t)
	ctx := context.Background()
	a := mustOrganizer(t, q, "org-a")
	b := mustOrganizer(t, q, "org-b")
	own := mustVenue(t, q, a.ID, "A hall")
	shared := mustSharedVenue(t, pool, "Lviv Opera")

	for _, id := range []int64{own.ID, shared} {
		_, err := q.UpdateVenue(ctx, dbgen.UpdateVenueParams{ID: id, OrganizerID: b.ID, Name: "hijacked", City: "X", Capacity: 1})
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("B updates venue %d: err = %v, want no rows", id, err)
		}
		n, err := q.DeleteVenue(ctx, dbgen.DeleteVenueParams{ID: id, OrganizerID: b.ID})
		if err != nil || n != 0 {
			t.Errorf("B deletes venue %d: rows = %d, err = %v; want 0", id, n, err)
		}
	}
	_, err := q.UpdateVenue(ctx, dbgen.UpdateVenueParams{ID: shared, OrganizerID: a.ID, Name: "hijacked", City: "X", Capacity: 1})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("organizer edits a shared venue: err = %v, want no rows", err)
	}

	list, err := q.ListVenuesForOrganizer(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != shared {
		t.Errorf("B's venue choices = %+v, want only the shared venue", list)
	}
}

func TestEventCannotBePlacedAtAnotherOrganizersVenue(t *testing.T) {
	pool, q := newQueries(t)
	ctx := context.Background()
	a := mustOrganizer(t, q, "org-a")
	b := mustOrganizer(t, q, "org-b")
	bHall := mustVenue(t, q, b.ID, "B hall")
	shared := mustSharedVenue(t, pool, "Palace of Sports")

	_, err := q.CreateEvent(ctx, eventParams(a.ID, bHall.ID, "a-at-b"))
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("create at B's venue: err = %v, want no rows", err)
	}

	ev := mustEvent(t, q, a.ID, shared, "a-at-shared")
	moved := updateParams(ev, a.ID)
	moved.VenueID = bHall.ID
	_, err = q.UpdateEvent(ctx, moved)
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("move to B's venue: err = %v, want no rows", err)
	}
}

func TestEventMustEndAfterItStarts(t *testing.T) {
	_, q := newQueries(t)
	ctx := context.Background()
	a := mustOrganizer(t, q, "org-a")
	venue := mustVenue(t, q, a.ID, "A hall")

	for name, end := range map[string]time.Time{
		"same instant": demoStart,
		"before start": demoStart.Add(-time.Hour),
	} {
		p := eventParams(a.ID, venue.ID, "bad-times")
		p.EndsAt = end
		_, err := q.CreateEvent(ctx, p)
		wantPgCode(t, name, err, checkViolation)
	}
}

func TestEventAndVenueConstraints(t *testing.T) {
	_, q := newQueries(t)
	ctx := context.Background()
	a := mustOrganizer(t, q, "org-a")
	venue := mustVenue(t, q, a.ID, "A hall")
	ev := mustEvent(t, q, a.ID, venue.ID, "taken")

	_, err := q.CreateEvent(ctx, eventParams(a.ID, venue.ID, "taken"))
	wantPgCode(t, "duplicate event slug", err, uniqueViolation)

	_, err = q.SetEventStatus(ctx, dbgen.SetEventStatusParams{ID: ev.ID, OrganizerID: a.ID, Status: "sold-out"})
	wantPgCode(t, "unknown status", err, checkViolation)

	_, err = q.CreateVenue(ctx, dbgen.CreateVenueParams{OrganizerID: a.ID, Name: "A hall", City: "Kyiv", Capacity: 10})
	wantPgCode(t, "duplicate venue name", err, uniqueViolation)

	for name, p := range map[string]dbgen.CreateVenueParams{
		"lat out of range": {OrganizerID: a.ID, Name: "n1", City: "Kyiv", Lat: 91, Capacity: 10},
		"lng out of range": {OrganizerID: a.ID, Name: "n2", City: "Kyiv", Lng: -181, Capacity: 10},
		"zero capacity":    {OrganizerID: a.ID, Name: "n3", City: "Kyiv"},
	} {
		_, err := q.CreateVenue(ctx, p)
		wantPgCode(t, name, err, checkViolation)
	}

	n, err := q.DeleteVenue(ctx, dbgen.DeleteVenueParams{ID: venue.ID, OrganizerID: a.ID})
	if n != 0 {
		t.Errorf("deleted a venue that hosts events (rows = %d)", n)
	}
	wantPgCode(t, "delete venue with events", err, foreignKeyViolation)
}

func TestPublicListingShowsOnlyPublishedUpcomingEvents(t *testing.T) {
	_, q := newQueries(t)
	ctx := context.Background()
	a := mustOrganizer(t, q, "org-a")
	venue := mustVenue(t, q, a.ID, "A hall")

	draft := mustEvent(t, q, a.ID, venue.ID, "draft-show")
	published := mustEvent(t, q, a.ID, venue.ID, "published-show")
	cancelled := mustEvent(t, q, a.ID, venue.ID, "cancelled-show")
	for id, status := range map[int64]string{published.ID: "published", cancelled.ID: "cancelled"} {
		if _, err := q.SetEventStatus(ctx, dbgen.SetEventStatusParams{ID: id, OrganizerID: a.ID, Status: status}); err != nil {
			t.Fatal(err)
		}
	}

	list, err := q.ListUpcomingPublishedEvents(ctx, dbgen.ListUpcomingPublishedEventsParams{After: demoStart.Add(-time.Hour), Lim: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Event.ID != published.ID || list[0].VenueCity != "Kyiv" {
		t.Errorf("public list = %+v, want only the published event with its venue", list)
	}

	past, err := q.ListUpcomingPublishedEvents(ctx, dbgen.ListUpcomingPublishedEventsParams{After: demoStart.Add(24 * time.Hour), Lim: 10})
	if err != nil || len(past) != 0 {
		t.Errorf("list after the event ended = %d items, err = %v; want empty", len(past), err)
	}

	if _, err := q.GetPublishedEventBySlug(ctx, draft.Slug); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("draft by slug: err = %v, want no rows", err)
	}
	got, err := q.GetPublishedEventBySlug(ctx, published.Slug)
	if err != nil || got.Venue.ID != venue.ID {
		t.Errorf("published by slug = %+v, %v", got, err)
	}
}
