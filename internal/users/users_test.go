package users

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/KotunOleg/Tixo/internal/db/dbtest"
	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
)

func countUsers(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM users").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func olena() GoogleProfile {
	return GoogleProfile{Sub: "google-sub-1", Email: "Olena@Example.com", EmailVerified: true, Name: "Олена"}
}

func TestFirstGoogleSignInRegistersUser(t *testing.T) {
	pool := dbtest.Pool(t)
	u, err := New(pool).SignInWithGoogle(context.Background(), olena())
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "olena@example.com" || u.Name != "Олена" || u.GoogleSub.String != "google-sub-1" {
		t.Errorf("registered user = %+v", u)
	}
	if n := countUsers(t, pool); n != 1 {
		t.Errorf("users = %d, want 1", n)
	}
}

func TestRepeatedSignInUpdatesTheSameUser(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	first, err := svc.SignInWithGoogle(context.Background(), olena())
	if err != nil {
		t.Fatal(err)
	}
	renamed := olena()
	renamed.Name = "Олена Коваль"
	renamed.Email = "olena.koval@example.com"
	again, err := svc.SignInWithGoogle(context.Background(), renamed)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != first.ID || again.Name != "Олена Коваль" || again.Email != "olena.koval@example.com" {
		t.Errorf("second sign-in = %+v, want user %d with new name and email", again, first.ID)
	}
	if n := countUsers(t, pool); n != 1 {
		t.Errorf("users = %d, want 1: second sign-in registered a duplicate", n)
	}
}

func TestVerifiedEmailLinksExistingUserWithoutGoogleAccount(t *testing.T) {
	pool := dbtest.Pool(t)
	existing, err := dbgen.New(pool).CreateUser(context.Background(), dbgen.CreateUserParams{Email: "olena@example.com", Name: "Olena"})
	if err != nil {
		t.Fatal(err)
	}
	u, err := New(pool).SignInWithGoogle(context.Background(), olena())
	if err != nil {
		t.Fatal(err)
	}
	if u.ID != existing.ID || u.GoogleSub.String != "google-sub-1" {
		t.Errorf("signed in as %+v, want existing user %d linked to the google account", u, existing.ID)
	}
	if n := countUsers(t, pool); n != 1 {
		t.Errorf("users = %d, want 1", n)
	}
}

func TestUnverifiedEmailIsRejectedAndLinksNothing(t *testing.T) {
	pool := dbtest.Pool(t)
	q := dbgen.New(pool)
	existing, err := q.CreateUser(context.Background(), dbgen.CreateUserParams{Email: "olena@example.com", Name: "Olena"})
	if err != nil {
		t.Fatal(err)
	}
	p := olena()
	p.EmailVerified = false
	if _, err := New(pool).SignInWithGoogle(context.Background(), p); !errors.Is(err, ErrEmailNotVerified) {
		t.Fatalf("err = %v, want ErrEmailNotVerified", err)
	}
	after, err := q.GetUser(context.Background(), existing.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.GoogleSub.Valid {
		t.Errorf("unverified google account took over user %d", existing.ID)
	}
	if n := countUsers(t, pool); n != 1 {
		t.Errorf("users = %d, want 1", n)
	}
}

func TestEmailOfAnotherGoogleAccountIsNotTakenOver(t *testing.T) {
	pool := dbtest.Pool(t)
	q := dbgen.New(pool)
	other, err := q.CreateUser(context.Background(), dbgen.CreateUserParams{
		Email: "olena@example.com", Name: "Olena", GoogleSub: pgtype.Text{String: "google-sub-other", Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(pool).SignInWithGoogle(context.Background(), olena()); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("err = %v, want ErrEmailTaken", err)
	}
	after, err := q.GetUser(context.Background(), other.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.GoogleSub.String != "google-sub-other" {
		t.Errorf("user %d relinked to %q", other.ID, after.GoogleSub.String)
	}
}

func TestProfileWithoutSubOrEmailIsRejected(t *testing.T) {
	pool := dbtest.Pool(t)
	cases := map[string]GoogleProfile{
		"no sub":   {Email: "a@example.com", EmailVerified: true, Name: "A"},
		"no email": {Sub: "s", EmailVerified: true, Name: "A"},
	}
	for name, p := range cases {
		if _, err := New(pool).SignInWithGoogle(context.Background(), p); !errors.Is(err, ErrInvalidProfile) {
			t.Errorf("%s: err = %v, want ErrInvalidProfile", name, err)
		}
	}
	if n := countUsers(t, pool); n != 0 {
		t.Errorf("users = %d, want 0", n)
	}
}

func TestEmptyGoogleNameFallsBackToEmail(t *testing.T) {
	pool := dbtest.Pool(t)
	p := olena()
	p.Name = "  "
	u, err := New(pool).SignInWithGoogle(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if u.Name != "olena" {
		t.Errorf("name = %q, want the email local part", u.Name)
	}
}

func TestConcurrentFirstSignInsCreateOneUser(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	ids := make([]int64, n)
	for i := range n {
		wg.Go(func() {
			u, err := svc.SignInWithGoogle(context.Background(), olena())
			errs[i], ids[i] = err, u.ID
		})
	}
	wg.Wait()
	for i := range n {
		if errs[i] != nil {
			t.Errorf("sign-in %d: %v", i, errs[i])
		} else if ids[i] != ids[0] {
			t.Errorf("sign-in %d got user %d, sign-in 0 got %d", i, ids[i], ids[0])
		}
	}
	if c := countUsers(t, pool); c != 1 {
		t.Errorf("users = %d, want 1", c)
	}
}
