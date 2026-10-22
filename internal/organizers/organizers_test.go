package organizers

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/KotunOleg/Tixo/internal/db/dbtest"
	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
)

func mustUser(t *testing.T, pool *pgxpool.Pool, email string) int64 {
	t.Helper()
	u, err := dbgen.New(pool).CreateUser(context.Background(), dbgen.CreateUserParams{Email: email, Name: email})
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

func TestCreateMakesTheUserOwner(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	alice := mustUser(t, pool, "alice@example.com")

	org, err := svc.Create(context.Background(), alice, CreateInput{Name: "  Kyiv Live ", Slug: "Kyiv-Live"})
	if err != nil {
		t.Fatal(err)
	}
	if org.Name != "Kyiv Live" || org.Slug != "kyiv-live" || org.Plan != "free" {
		t.Errorf("created %+v", org)
	}
	m, err := svc.ForMember(context.Background(), alice, "kyiv-live")
	if err != nil || m.Role != RoleOwner || m.Organizer.ID != org.ID {
		t.Errorf("creator membership = %+v, %v; want owner", m, err)
	}
}

func TestCreateValidatesInput(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	alice := mustUser(t, pool, "alice@example.com")
	cases := []struct {
		in       CreateInput
		field    string
		wantCode string
	}{
		{CreateInput{Name: "", Slug: "kyiv-live"}, FieldName, CodeRequired},
		{CreateInput{Name: "K", Slug: "kyiv-live"}, FieldName, CodeLength},
		{CreateInput{Name: strings.Repeat("я", 101), Slug: "kyiv-live"}, FieldName, CodeLength},
		{CreateInput{Name: "Kyiv Live", Slug: ""}, FieldSlug, CodeRequired},
		{CreateInput{Name: "Kyiv Live", Slug: "ky"}, FieldSlug, CodeLength},
		{CreateInput{Name: "Kyiv Live", Slug: "kyiv live"}, FieldSlug, CodeFormat},
		{CreateInput{Name: "Kyiv Live", Slug: "-kyiv"}, FieldSlug, CodeFormat},
		{CreateInput{Name: "Kyiv Live", Slug: "київ"}, FieldSlug, CodeFormat},
		{CreateInput{Name: "Kyiv Live", Slug: "new"}, FieldSlug, CodeReserved},
	}
	for _, tc := range cases {
		_, err := svc.Create(context.Background(), alice, tc.in)
		var verr *ValidationError
		if !errors.As(err, &verr) || verr.Fields[tc.field] != tc.wantCode {
			t.Errorf("Create(%+v) err = %v, want %s=%s", tc.in, err, tc.field, tc.wantCode)
		}
	}
	var n int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM organizers").Scan(&n); err != nil || n != 0 {
		t.Errorf("organizers after invalid input = %d, %v", n, err)
	}
}

func TestTakenSlugIsAValidationErrorEvenUnderRace(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	alice := mustUser(t, pool, "alice@example.com")
	bob := mustUser(t, pool, "bob@example.com")

	const n = 6
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Go(func() {
			user := alice
			if i%2 == 1 {
				user = bob
			}
			_, errs[i] = svc.Create(context.Background(), user, CreateInput{Name: "Kyiv Live", Slug: "kyiv-live"})
		})
	}
	wg.Wait()
	created := 0
	for _, err := range errs {
		var verr *ValidationError
		switch {
		case err == nil:
			created++
		case errors.As(err, &verr) && verr.Fields[FieldSlug] == CodeTaken:
		default:
			t.Errorf("concurrent create: %v, want success or slug taken", err)
		}
	}
	if created != 1 {
		t.Errorf("created %d organizers with one slug, want 1", created)
	}
	var memberships int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM memberships").Scan(&memberships); err != nil || memberships != 1 {
		t.Errorf("memberships = %d, %v; want 1 (no orphan from a failed create)", memberships, err)
	}
}

func TestNonMemberCannotOpenOrganizer(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	alice := mustUser(t, pool, "alice@example.com")
	bob := mustUser(t, pool, "bob@example.com")
	if _, err := svc.Create(context.Background(), alice, CreateInput{Name: "Kyiv Live", Slug: "kyiv-live"}); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"kyiv-live", "no-such-org"} {
		if _, err := svc.ForMember(context.Background(), bob, slug); !errors.Is(err, ErrNotFound) {
			t.Errorf("bob ForMember(%s) err = %v, want ErrNotFound", slug, err)
		}
	}
	list, err := svc.ListForUser(context.Background(), bob)
	if err != nil || len(list) != 0 {
		t.Errorf("bob's organizers = %+v, %v; want none", list, err)
	}
}

func TestEventsListsOnlyTheMembersOrganizer(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	q := dbgen.New(pool)
	alice := mustUser(t, pool, "alice@example.com")
	bob := mustUser(t, pool, "bob@example.com")
	a, err := svc.Create(context.Background(), alice, CreateInput{Name: "Kyiv Live", Slug: "kyiv-live"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.Create(context.Background(), bob, CreateInput{Name: "Lviv Sound", Slug: "lviv-sound"})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2030, 6, 1, 19, 0, 0, 0, time.UTC)
	for _, org := range []dbgen.Organizer{a, b} {
		v, err := q.CreateVenue(context.Background(), dbgen.CreateVenueParams{
			OrganizerID: org.ID, Name: org.Name + " hall", City: "Kyiv", Lat: 50.45, Lng: 30.52, Capacity: 100,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := q.CreateEvent(context.Background(), dbgen.CreateEventParams{
			OrganizerID: org.ID, VenueID: v.ID, Slug: org.Slug + "-show", Title: org.Name + " show",
			StartsAt: start, EndsAt: start.Add(time.Hour), Genre: "rock",
		}); err != nil {
			t.Fatal(err)
		}
	}

	m, err := svc.ForMember(context.Background(), alice, "kyiv-live")
	if err != nil {
		t.Fatal(err)
	}
	events, err := svc.Events(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Title != "Kyiv Live show" {
		t.Errorf("alice's events = %+v, want only Kyiv Live show", events)
	}
}
