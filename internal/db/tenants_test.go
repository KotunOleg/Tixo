package db_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/KotunOleg/Tixo/internal/db/dbtest"
	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
)

const (
	uniqueViolation     = "23505"
	checkViolation      = "23514"
	foreignKeyViolation = "23503"
)

func pgCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

func wantPgCode(t *testing.T, what string, err error, code string) {
	t.Helper()
	if got := pgCode(err); got != code {
		t.Errorf("%s: err = %v, want SQLSTATE %s", what, err, code)
	}
}

func newQueries(t *testing.T) (*pgxpool.Pool, *dbgen.Queries) {
	t.Helper()
	pool := dbtest.Pool(t)
	return pool, dbgen.New(pool)
}

func mustOrganizer(t *testing.T, q *dbgen.Queries, slug string) dbgen.Organizer {
	t.Helper()
	o, err := q.CreateOrganizer(context.Background(), dbgen.CreateOrganizerParams{
		Slug: slug, Name: slug, Contacts: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create organizer %s: %v", slug, err)
	}
	return o
}

func mustUser(t *testing.T, q *dbgen.Queries, email string) dbgen.User {
	t.Helper()
	u, err := q.CreateUser(context.Background(), dbgen.CreateUserParams{Email: email, Name: email})
	if err != nil {
		t.Fatalf("create user %s: %v", email, err)
	}
	return u
}

func TestOrganizerSlugIsUniqueAndURLSafe(t *testing.T) {
	_, q := newQueries(t)
	ctx := context.Background()
	mustOrganizer(t, q, "kyiv-live")

	cases := []struct {
		slug string
		code string
	}{
		{"kyiv-live", uniqueViolation},
		{"Kyiv-Live", checkViolation},
		{"kyiv live", checkViolation},
		{"-kyiv", checkViolation},
		{"", checkViolation},
	}
	for _, c := range cases {
		_, err := q.CreateOrganizer(ctx, dbgen.CreateOrganizerParams{Slug: c.slug, Name: "x", Contacts: json.RawMessage(`{}`)})
		wantPgCode(t, "slug "+c.slug, err, c.code)
	}
}

func TestOrganizerRejectsUnknownPlanAndNonObjectContacts(t *testing.T) {
	_, q := newQueries(t)
	ctx := context.Background()
	o := mustOrganizer(t, q, "lviv-sound")

	_, err := q.SetOrganizerPlan(ctx, dbgen.SetOrganizerPlanParams{ID: o.ID, Plan: "gold"})
	wantPgCode(t, "plan gold", err, checkViolation)

	_, err = q.UpdateOrganizer(ctx, dbgen.UpdateOrganizerParams{ID: o.ID, Name: o.Name, Contacts: json.RawMessage(`["a"]`)})
	wantPgCode(t, "contacts array", err, checkViolation)

	if o.Plan != "free" {
		t.Errorf("new organizer plan = %q, want free", o.Plan)
	}
}

func TestUserEmailIsCaseInsensitivelyUnique(t *testing.T) {
	_, q := newQueries(t)
	ctx := context.Background()
	u := mustUser(t, q, "Fan@Example.com")
	if u.Email != "fan@example.com" {
		t.Errorf("stored email = %q, want lower-cased", u.Email)
	}

	_, err := q.CreateUser(ctx, dbgen.CreateUserParams{Email: "FAN@example.com", Name: "dup"})
	wantPgCode(t, "duplicate email", err, uniqueViolation)

	got, err := q.GetUserByEmail(ctx, "fan@EXAMPLE.com")
	if err != nil || got.ID != u.ID {
		t.Errorf("GetUserByEmail = %v, %v; want user %d", got.ID, err, u.ID)
	}
}

func TestListOrganizersForUserShowsOnlyOwnMemberships(t *testing.T) {
	_, q := newQueries(t)
	ctx := context.Background()
	alice := mustUser(t, q, "alice@example.com")
	bob := mustUser(t, q, "bob@example.com")
	a := mustOrganizer(t, q, "alpha")
	b := mustOrganizer(t, q, "beta")

	for _, m := range []dbgen.AddMembershipParams{
		{UserID: alice.ID, OrganizerID: a.ID, Role: "owner"},
		{UserID: bob.ID, OrganizerID: b.ID, Role: "owner"},
		{UserID: bob.ID, OrganizerID: a.ID, Role: "editor"},
	} {
		if _, err := q.AddMembership(ctx, m); err != nil {
			t.Fatal(err)
		}
	}

	got, err := q.ListOrganizersForUser(ctx, alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Organizer.ID != a.ID || got[0].Role != "owner" {
		t.Errorf("alice sees %+v, want only alpha as owner", got)
	}

	_, err = q.AddMembership(ctx, dbgen.AddMembershipParams{UserID: alice.ID, OrganizerID: b.ID, Role: "admin"})
	wantPgCode(t, "role admin", err, checkViolation)
	_, err = q.AddMembership(ctx, dbgen.AddMembershipParams{UserID: alice.ID, OrganizerID: a.ID, Role: "editor"})
	wantPgCode(t, "second membership", err, uniqueViolation)
	_, err = q.AddMembership(ctx, dbgen.AddMembershipParams{UserID: alice.ID, OrganizerID: 999999, Role: "editor"})
	wantPgCode(t, "missing organizer", err, foreignKeyViolation)
}
