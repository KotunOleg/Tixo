package lineup

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/KotunOleg/Tixo/internal/artists"
	"github.com/KotunOleg/Tixo/internal/db/dbtest"
	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/events"
	"github.com/KotunOleg/Tixo/internal/organizers"
	"github.com/KotunOleg/Tixo/internal/venues"
)

func mustOrganizer(t *testing.T, pool *pgxpool.Pool, slug string) int64 {
	t.Helper()
	u, err := dbgen.New(pool).CreateUser(context.Background(), dbgen.CreateUserParams{Email: slug + "@example.com", Name: slug})
	if err != nil {
		t.Fatal(err)
	}
	org, err := organizers.New(pool).Create(context.Background(), u.ID, organizers.CreateInput{Name: slug, Slug: slug})
	if err != nil {
		t.Fatal(err)
	}
	return org.ID
}

func mustEvent(t *testing.T, pool *pgxpool.Pool, organizerID int64, slug string) int64 {
	t.Helper()
	v, err := venues.New(pool).Create(context.Background(), organizerID, venues.Input{
		Name: slug + " venue", Address: "1 Test St", City: "Kyiv", Lat: 50.45, Lng: 30.52, Capacity: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2030, 6, 1, 19, 0, 0, 0, time.UTC)
	e, err := events.New(pool).Create(context.Background(), organizerID, events.Input{
		VenueID: v.ID, Slug: slug, Title: "Big Show", Genre: events.GenreRock, StartsAt: start, EndsAt: start.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	return e.ID
}

func TestAddAndListLineup(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	org := mustOrganizer(t, pool, "kyiv-live")
	eventID := mustEvent(t, pool, org, "big-show")
	a, err := artists.New(pool).Create(context.Background(), "Okean Elzy")
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.Add(context.Background(), org, eventID, a.ID, true); err != nil {
		t.Fatal(err)
	}

	rows, err := svc.List(context.Background(), org, eventID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Artist.ID != a.ID || !rows[0].IsHeadliner {
		t.Errorf("List = %+v, want one headliner row for %d", rows, a.ID)
	}
}

func TestAddDoesNotDuplicateOnConflict(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	org := mustOrganizer(t, pool, "kyiv-live")
	eventID := mustEvent(t, pool, org, "big-show")
	a, err := artists.New(pool).Create(context.Background(), "Okean Elzy")
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.Add(context.Background(), org, eventID, a.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := svc.Add(context.Background(), org, eventID, a.ID, true); err != nil {
		t.Fatal(err)
	}

	rows, err := svc.List(context.Background(), org, eventID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || !rows[0].IsHeadliner {
		t.Errorf("re-adding the same artist must update, not duplicate: %+v", rows)
	}
}

func TestAddRejectsForeignEvent(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	org := mustOrganizer(t, pool, "kyiv-live")
	other := mustOrganizer(t, pool, "lviv-sound")
	eventID := mustEvent(t, pool, org, "big-show")
	a, err := artists.New(pool).Create(context.Background(), "Okean Elzy")
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.Add(context.Background(), other, eventID, a.ID, false); !errors.Is(err, ErrEventNotFound) {
		t.Errorf("Add(foreign event) = %v, want ErrEventNotFound", err)
	}
}

func TestAddRejectsMissingArtist(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	org := mustOrganizer(t, pool, "kyiv-live")
	eventID := mustEvent(t, pool, org, "big-show")

	if err := svc.Add(context.Background(), org, eventID, 999999, false); !errors.Is(err, ErrArtistNotFound) {
		t.Errorf("Add(missing artist) = %v, want ErrArtistNotFound", err)
	}
}

func TestListIsEmptyForForeignOrganizer(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	org := mustOrganizer(t, pool, "kyiv-live")
	other := mustOrganizer(t, pool, "lviv-sound")
	eventID := mustEvent(t, pool, org, "big-show")
	a, err := artists.New(pool).Create(context.Background(), "Okean Elzy")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Add(context.Background(), org, eventID, a.ID, false); err != nil {
		t.Fatal(err)
	}

	rows, err := svc.List(context.Background(), other, eventID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("List(foreign organizer) = %+v, want empty", rows)
	}
}

func TestRemoveDeletesLineupEntry(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	org := mustOrganizer(t, pool, "kyiv-live")
	eventID := mustEvent(t, pool, org, "big-show")
	a, err := artists.New(pool).Create(context.Background(), "Okean Elzy")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Add(context.Background(), org, eventID, a.ID, false); err != nil {
		t.Fatal(err)
	}

	if err := svc.Remove(context.Background(), org, eventID, a.ID); err != nil {
		t.Fatal(err)
	}
	rows, err := svc.List(context.Background(), org, eventID)
	if err != nil || len(rows) != 0 {
		t.Errorf("List after remove = %+v, %v, want empty", rows, err)
	}
}

func TestRemoveRejectsForeignEvent(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	org := mustOrganizer(t, pool, "kyiv-live")
	other := mustOrganizer(t, pool, "lviv-sound")
	eventID := mustEvent(t, pool, org, "big-show")
	a, err := artists.New(pool).Create(context.Background(), "Okean Elzy")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Add(context.Background(), org, eventID, a.ID, false); err != nil {
		t.Fatal(err)
	}

	if err := svc.Remove(context.Background(), other, eventID, a.ID); !errors.Is(err, ErrEventNotFound) {
		t.Errorf("Remove(foreign event) = %v, want ErrEventNotFound", err)
	}
	rows, _ := svc.List(context.Background(), org, eventID)
	if len(rows) != 1 {
		t.Errorf("lineup must survive a foreign-tenant remove attempt, got %+v", rows)
	}
}

func TestRemoveMissingPairingIsNoop(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	org := mustOrganizer(t, pool, "kyiv-live")
	eventID := mustEvent(t, pool, org, "big-show")

	if err := svc.Remove(context.Background(), org, eventID, 999999); err != nil {
		t.Errorf("Remove(never-added artist) = %v, want nil (idempotent)", err)
	}
}
