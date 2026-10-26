package events

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/KotunOleg/Tixo/internal/db/dbtest"
	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
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

func mustVenue(t *testing.T, pool *pgxpool.Pool, organizerID int64, name string) int64 {
	t.Helper()
	v, err := venues.New(pool).Create(context.Background(), organizerID, venues.Input{
		Name: name, Address: "1 Test St", City: "Kyiv", Lat: 50.45, Lng: 30.52, Capacity: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	return v.ID
}

func validEventInput(venueID int64) Input {
	start := time.Date(2030, 6, 1, 19, 0, 0, 0, time.UTC)
	return Input{VenueID: venueID, Slug: "big-show", Title: "Big Show", StartsAt: start, EndsAt: start.Add(2 * time.Hour), Genre: GenreRock}
}

func TestCreateAndGetScopedToOrganizer(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	a := mustOrganizer(t, pool, "kyiv-live")
	b := mustOrganizer(t, pool, "lviv-sound")
	venueA := mustVenue(t, pool, a, "Palats Sportu")

	e, err := svc.Create(context.Background(), a, validEventInput(venueA))
	if err != nil {
		t.Fatal(err)
	}
	if e.Status != StatusDraft {
		t.Errorf("new event status = %q, want draft", e.Status)
	}

	if _, err := svc.Get(context.Background(), b, e.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("foreign organizer Get = %v, want ErrNotFound", err)
	}
	if _, err := svc.Get(context.Background(), a, e.ID); err != nil {
		t.Errorf("owner Get = %v, want nil", err)
	}
}

func TestCreateRejectsVenueFromAnotherOrganizer(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	a := mustOrganizer(t, pool, "kyiv-live")
	b := mustOrganizer(t, pool, "lviv-sound")
	venueB := mustVenue(t, pool, b, "Opera House")

	_, err := svc.Create(context.Background(), a, validEventInput(venueB))
	var verr *ValidationError
	if !errors.As(err, &verr) || verr.Fields[FieldVenue] != CodeNotFound {
		t.Errorf("Create with foreign venue err = %v, want venue_id=not_found", err)
	}
}

func TestCreateValidatesInput(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	org := mustOrganizer(t, pool, "kyiv-live")
	venue := mustVenue(t, pool, org, "Palats Sportu")
	base := validEventInput(venue)
	cases := []struct {
		mutate   func(in Input) Input
		field    string
		wantCode string
	}{
		{func(in Input) Input { in.VenueID = 0; return in }, FieldVenue, CodeRequired},
		{func(in Input) Input { in.Slug = ""; return in }, FieldSlug, CodeRequired},
		{func(in Input) Input { in.Slug = "Big Show"; return in }, FieldSlug, CodeFormat},
		{func(in Input) Input { in.Title = ""; return in }, FieldTitle, CodeRequired},
		{func(in Input) Input { in.EndsAt = in.StartsAt; return in }, FieldEndsAt, CodeRange},
		{func(in Input) Input { in.EndsAt = in.StartsAt.Add(-time.Hour); return in }, FieldEndsAt, CodeRange},
		{func(in Input) Input { in.Genre = "polka"; return in }, FieldGenre, CodeInvalid},
		{func(in Input) Input { p := int64(-100); in.PriceFrom = &p; return in }, FieldPriceFrom, CodeRange},
	}
	for _, tc := range cases {
		_, err := svc.Create(context.Background(), org, tc.mutate(base))
		var verr *ValidationError
		if !errors.As(err, &verr) || verr.Fields[tc.field] != tc.wantCode {
			t.Errorf("Create(%+v) err = %v, want %s=%s", tc.mutate(base), err, tc.field, tc.wantCode)
		}
	}
}

func TestCreateRejectsDuplicateSlugAcrossOrganizers(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	a := mustOrganizer(t, pool, "kyiv-live")
	b := mustOrganizer(t, pool, "lviv-sound")
	venueA := mustVenue(t, pool, a, "Palats Sportu")
	venueB := mustVenue(t, pool, b, "Opera House")
	if _, err := svc.Create(context.Background(), a, validEventInput(venueA)); err != nil {
		t.Fatal(err)
	}

	_, err := svc.Create(context.Background(), b, validEventInput(venueB))
	var verr *ValidationError
	if !errors.As(err, &verr) || verr.Fields[FieldSlug] != CodeTaken {
		t.Errorf("duplicate slug across organizers err = %v, want slug=taken", err)
	}
}

func TestUpdateRejectsForeignEvent(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	a := mustOrganizer(t, pool, "kyiv-live")
	b := mustOrganizer(t, pool, "lviv-sound")
	venueA := mustVenue(t, pool, a, "Palats Sportu")
	e, err := svc.Create(context.Background(), a, validEventInput(venueA))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Update(context.Background(), b, e.ID, validEventInput(venueA)); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update(foreign event) = %v, want ErrNotFound", err)
	}
}

func TestUpdateRejectsVenueFromAnotherOrganizer(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	a := mustOrganizer(t, pool, "kyiv-live")
	b := mustOrganizer(t, pool, "lviv-sound")
	venueA := mustVenue(t, pool, a, "Palats Sportu")
	venueB := mustVenue(t, pool, b, "Opera House")
	e, err := svc.Create(context.Background(), a, validEventInput(venueA))
	if err != nil {
		t.Fatal(err)
	}

	in := validEventInput(venueB)
	_, err = svc.Update(context.Background(), a, e.ID, in)
	var verr *ValidationError
	if !errors.As(err, &verr) || verr.Fields[FieldVenue] != CodeNotFound {
		t.Errorf("Update with foreign venue err = %v, want venue_id=not_found", err)
	}
}

func TestSetStatusTransitions(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	org := mustOrganizer(t, pool, "kyiv-live")
	venue := mustVenue(t, pool, org, "Palats Sportu")
	e, err := svc.Create(context.Background(), org, validEventInput(venue))
	if err != nil {
		t.Fatal(err)
	}

	published, err := svc.SetStatus(context.Background(), org, e.ID, StatusPublished)
	if err != nil || published.Status != StatusPublished {
		t.Fatalf("SetStatus(published) = %+v, %v", published, err)
	}
	if _, err := svc.SetStatus(context.Background(), org, e.ID, "deleted"); !errors.Is(err, ErrBadStatus) {
		t.Errorf("SetStatus(unknown) = %v, want ErrBadStatus", err)
	}
	if _, err := svc.SetStatus(context.Background(), org+1000, e.ID, StatusCancelled); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetStatus(foreign organizer) = %v, want ErrNotFound", err)
	}
}

func TestDeleteRemovesEvent(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	org := mustOrganizer(t, pool, "kyiv-live")
	venue := mustVenue(t, pool, org, "Palats Sportu")
	e, err := svc.Create(context.Background(), org, validEventInput(venue))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(context.Background(), org, e.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(context.Background(), org, e.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(deleted event) = %v, want ErrNotFound", err)
	}
}

func TestDeleteMissingEventNotFound(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	org := mustOrganizer(t, pool, "kyiv-live")
	if err := svc.Delete(context.Background(), org, 999999); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete(missing event) = %v, want ErrNotFound", err)
	}
}
