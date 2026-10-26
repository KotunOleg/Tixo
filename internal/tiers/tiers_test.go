package tiers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

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
	in := events.Input{
		VenueID: v.ID, Slug: slug, Title: "Big Show", Genre: events.GenreRock,
		StartsAt: start, EndsAt: start.Add(2 * time.Hour),
	}
	e, err := events.New(pool).Create(context.Background(), organizerID, in)
	if err != nil {
		t.Fatal(err)
	}
	return e.ID
}

func validInput() Input {
	return Input{Name: "Standard", Price: 50000, Quantity: 100}
}

func sellOne(t *testing.T, pool *pgxpool.Pool, tierID int64) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), "UPDATE ticket_tiers SET available = available - 1 WHERE id = $1", tierID); err != nil {
		t.Fatal(err)
	}
}

func TestCreateAndListScopedToEvent(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	org := mustOrganizer(t, pool, "kyiv-live")
	eventID := mustEvent(t, pool, org, "big-show")

	tier, err := svc.Create(context.Background(), org, eventID, validInput())
	if err != nil {
		t.Fatal(err)
	}
	if tier.Available != tier.Quantity {
		t.Errorf("new tier available = %d, want %d", tier.Available, tier.Quantity)
	}

	rows, err := svc.List(context.Background(), org, eventID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != tier.ID {
		t.Errorf("List = %+v, want [%+v]", rows, tier)
	}
}

func TestListIsEmptyForForeignOrganizer(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	org := mustOrganizer(t, pool, "kyiv-live")
	other := mustOrganizer(t, pool, "lviv-sound")
	eventID := mustEvent(t, pool, org, "big-show")
	if _, err := svc.Create(context.Background(), org, eventID, validInput()); err != nil {
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

func TestCreateRejectsForeignEvent(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	org := mustOrganizer(t, pool, "kyiv-live")
	other := mustOrganizer(t, pool, "lviv-sound")
	eventID := mustEvent(t, pool, org, "big-show")

	if _, err := svc.Create(context.Background(), other, eventID, validInput()); !errors.Is(err, ErrNotFound) {
		t.Errorf("Create(foreign event) = %v, want ErrNotFound", err)
	}
}

func TestCreateValidatesInput(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	org := mustOrganizer(t, pool, "kyiv-live")
	eventID := mustEvent(t, pool, org, "big-show")
	cases := []struct {
		mutate   func(in Input) Input
		field    string
		wantCode string
	}{
		{func(in Input) Input { in.Name = ""; return in }, FieldName, CodeRequired},
		{func(in Input) Input { in.Name = "A"; return in }, FieldName, CodeLength},
		{func(in Input) Input { in.Price = -1; return in }, FieldPrice, CodeRange},
		{func(in Input) Input { in.Quantity = 0; return in }, FieldQuantity, CodeRange},
	}
	for _, tc := range cases {
		_, err := svc.Create(context.Background(), org, eventID, tc.mutate(validInput()))
		var verr *ValidationError
		if !errors.As(err, &verr) || verr.Fields[tc.field] != tc.wantCode {
			t.Errorf("Create(%+v) err = %v, want %s=%s", tc.mutate(validInput()), err, tc.field, tc.wantCode)
		}
	}
}

func TestCreateRejectsDuplicateNameForEvent(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	org := mustOrganizer(t, pool, "kyiv-live")
	eventID := mustEvent(t, pool, org, "big-show")
	if _, err := svc.Create(context.Background(), org, eventID, validInput()); err != nil {
		t.Fatal(err)
	}

	_, err := svc.Create(context.Background(), org, eventID, validInput())
	var verr *ValidationError
	if !errors.As(err, &verr) || verr.Fields[FieldName] != CodeTaken {
		t.Errorf("duplicate name err = %v, want name=taken", err)
	}
}

func TestUpdateChangesFields(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	org := mustOrganizer(t, pool, "kyiv-live")
	eventID := mustEvent(t, pool, org, "big-show")
	tier, err := svc.Create(context.Background(), org, eventID, validInput())
	if err != nil {
		t.Fatal(err)
	}

	updated, err := svc.Update(context.Background(), org, tier.ID, Input{Name: "VIP", Price: 90000, Quantity: 50})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "VIP" || updated.Price != 90000 || updated.Quantity != 50 || updated.Available != 50 {
		t.Errorf("Update result = %+v, want VIP/90000/50/50", updated)
	}
}

func TestUpdateRejectsForeignTier(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	org := mustOrganizer(t, pool, "kyiv-live")
	other := mustOrganizer(t, pool, "lviv-sound")
	eventID := mustEvent(t, pool, org, "big-show")
	tier, err := svc.Create(context.Background(), org, eventID, validInput())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Update(context.Background(), other, tier.ID, validInput()); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update(foreign tier) = %v, want ErrNotFound", err)
	}
}

func TestUpdateRejectsQuantityBelowSold(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	org := mustOrganizer(t, pool, "kyiv-live")
	eventID := mustEvent(t, pool, org, "big-show")
	tier, err := svc.Create(context.Background(), org, eventID, Input{Name: "Standard", Price: 50000, Quantity: 10})
	if err != nil {
		t.Fatal(err)
	}
	for range 8 {
		sellOne(t, pool, tier.ID)
	}

	_, err = svc.Update(context.Background(), org, tier.ID, Input{Name: "Standard", Price: 50000, Quantity: 1})
	var verr *ValidationError
	if !errors.As(err, &verr) || verr.Fields[FieldQuantity] != CodeRange {
		t.Errorf("Update(quantity below sold) err = %v, want quantity=range", err)
	}
}

func TestDeleteBlockedWhenSold(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	org := mustOrganizer(t, pool, "kyiv-live")
	eventID := mustEvent(t, pool, org, "big-show")
	tier, err := svc.Create(context.Background(), org, eventID, validInput())
	if err != nil {
		t.Fatal(err)
	}
	sellOne(t, pool, tier.ID)

	if err := svc.Delete(context.Background(), org, tier.ID); !errors.Is(err, ErrSold) {
		t.Errorf("Delete(sold tier) = %v, want ErrSold", err)
	}
	if _, err := svc.Get(context.Background(), org, tier.ID); err != nil {
		t.Errorf("sold tier should survive delete attempt, Get = %v", err)
	}
}

func TestDeleteRemovesUnsoldTier(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	org := mustOrganizer(t, pool, "kyiv-live")
	eventID := mustEvent(t, pool, org, "big-show")
	tier, err := svc.Create(context.Background(), org, eventID, validInput())
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.Delete(context.Background(), org, tier.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(context.Background(), org, tier.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(deleted tier) = %v, want ErrNotFound", err)
	}
}

func TestDeleteMissingTierNotFound(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	org := mustOrganizer(t, pool, "kyiv-live")
	if err := svc.Delete(context.Background(), org, 999999); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete(missing tier) = %v, want ErrNotFound", err)
	}
}
