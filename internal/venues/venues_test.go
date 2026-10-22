package venues

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/KotunOleg/Tixo/internal/db/dbtest"
	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/organizers"
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

func validInput() Input {
	return Input{Name: "Palats Sportu", Address: "Sportyvna sq, 1", City: "Kyiv", Lat: 50.44, Lng: 30.52, Capacity: 10000}
}

func TestCreateAndListScopedToOrganizer(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	a := mustOrganizer(t, pool, "kyiv-live")
	b := mustOrganizer(t, pool, "lviv-sound")

	v, err := svc.Create(context.Background(), a, validInput())
	if err != nil {
		t.Fatal(err)
	}

	listA, err := svc.List(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	if len(listA) != 1 || listA[0].ID != v.ID {
		t.Errorf("organizer A venues = %+v, want only %d", listA, v.ID)
	}

	listB, err := svc.List(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	if len(listB) != 0 {
		t.Errorf("organizer B must not see A's venue: %+v", listB)
	}
}

func TestCreateValidatesInput(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	org := mustOrganizer(t, pool, "kyiv-live")
	base := validInput()
	cases := []struct {
		mutate   func(in Input) Input
		field    string
		wantCode string
	}{
		{func(in Input) Input { in.Name = ""; return in }, FieldName, CodeRequired},
		{func(in Input) Input { in.Name = "A"; return in }, FieldName, CodeLength},
		{func(in Input) Input { in.Address = ""; return in }, FieldAddress, CodeRequired},
		{func(in Input) Input { in.City = ""; return in }, FieldCity, CodeRequired},
		{func(in Input) Input { in.City = "K"; return in }, FieldCity, CodeLength},
		{func(in Input) Input { in.Lat = 91; return in }, FieldLat, CodeRange},
		{func(in Input) Input { in.Lat = -91; return in }, FieldLat, CodeRange},
		{func(in Input) Input { in.Lng = 181; return in }, FieldLng, CodeRange},
		{func(in Input) Input { in.Capacity = 0; return in }, FieldCapacity, CodeRange},
		{func(in Input) Input { in.Capacity = -5; return in }, FieldCapacity, CodeRange},
	}
	for _, tc := range cases {
		_, err := svc.Create(context.Background(), org, tc.mutate(base))
		var verr *ValidationError
		if !errors.As(err, &verr) || verr.Fields[tc.field] != tc.wantCode {
			t.Errorf("Create(%+v) err = %v, want %s=%s", tc.mutate(base), err, tc.field, tc.wantCode)
		}
	}
}

func TestCreateRejectsDuplicateNameWithinOrganizer(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	org := mustOrganizer(t, pool, "kyiv-live")
	if _, err := svc.Create(context.Background(), org, validInput()); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Create(context.Background(), org, validInput())
	var verr *ValidationError
	if !errors.As(err, &verr) || verr.Fields[FieldName] != CodeTaken {
		t.Errorf("duplicate name err = %v, want name=taken", err)
	}
}

func TestGetRejectsVenueFromAnotherOrganizer(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	a := mustOrganizer(t, pool, "kyiv-live")
	b := mustOrganizer(t, pool, "lviv-sound")
	v, err := svc.Create(context.Background(), a, validInput())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(context.Background(), b, v.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(other organizer's venue) = %v, want ErrNotFound", err)
	}
	if _, err := svc.Update(context.Background(), b, v.ID, validInput()); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update(other organizer's venue) = %v, want ErrNotFound", err)
	}
}

func TestUpdateChangesFields(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	org := mustOrganizer(t, pool, "kyiv-live")
	v, err := svc.Create(context.Background(), org, validInput())
	if err != nil {
		t.Fatal(err)
	}
	updated, err := svc.Update(context.Background(), org, v.ID, Input{Name: " NSC Olimpiyskiy ", Address: "Velyka Vasylkivska, 55", City: "Kyiv", Lat: 50.43, Lng: 30.52, Capacity: 70000})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "NSC Olimpiyskiy" || updated.Capacity != 70000 {
		t.Errorf("updated = %+v", updated)
	}
}

func TestDeleteBlockedWhenVenueHasEvents(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	org := mustOrganizer(t, pool, "kyiv-live")
	v, err := svc.Create(context.Background(), org, validInput())
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2030, 6, 1, 19, 0, 0, 0, time.UTC)
	if _, err := dbgen.New(pool).CreateEvent(context.Background(), dbgen.CreateEventParams{
		OrganizerID: org, VenueID: v.ID, Slug: "show", Title: "Show", Genre: "rock", StartsAt: start, EndsAt: start.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	if err := svc.Delete(context.Background(), org, v.ID); !errors.Is(err, ErrHasEvents) {
		t.Errorf("Delete(venue with events) = %v, want ErrHasEvents", err)
	}
	if _, err := svc.Get(context.Background(), org, v.ID); err != nil {
		t.Errorf("venue must still exist after a blocked delete: %v", err)
	}
}

func TestDeleteRemovesUnusedVenue(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	org := mustOrganizer(t, pool, "kyiv-live")
	v, err := svc.Create(context.Background(), org, validInput())
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(context.Background(), org, v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(context.Background(), org, v.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(deleted venue) = %v, want ErrNotFound", err)
	}
}

func TestDeleteMissingVenueNotFound(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	org := mustOrganizer(t, pool, "kyiv-live")
	if err := svc.Delete(context.Background(), org, 999999); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete(missing venue) = %v, want ErrNotFound", err)
	}
}
