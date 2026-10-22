package apitokens

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/KotunOleg/Tixo/internal/db/dbtest"
	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
)

func seedUser(t *testing.T, pool *pgxpool.Pool, email string) int64 {
	t.Helper()
	u, err := dbgen.New(pool).CreateUser(context.Background(), dbgen.CreateUserParams{Email: email, Name: "Test"})
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

func TestIssueReturnsRawTokenOnlyOnce(t *testing.T) {
	pool := dbtest.Pool(t)
	userID := seedUser(t, pool, "owner@example.com")
	svc := New(pool)

	raw, token, err := svc.Issue(context.Background(), userID, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	if raw == "" {
		t.Fatal("issue did not return a raw token")
	}
	if token.TokenHash == raw {
		t.Fatal("stored hash equals the raw token")
	}

	stored, err := svc.List(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || stored[0].TokenHash == raw {
		t.Fatalf("listed tokens = %+v, want one token without the raw value", stored)
	}
}

func TestIssueRejectsBlankName(t *testing.T) {
	pool := dbtest.Pool(t)
	userID := seedUser(t, pool, "owner2@example.com")
	svc := New(pool)

	_, _, err := svc.Issue(context.Background(), userID, "   ")
	var verr *ValidationError
	if !errors.As(err, &verr) || verr.Fields[FieldName] != CodeRequired {
		t.Fatalf("err = %v, want validation error on name", err)
	}
}

func TestResolveFindsUserByRawToken(t *testing.T) {
	pool := dbtest.Pool(t)
	userID := seedUser(t, pool, "owner3@example.com")
	svc := New(pool)
	raw, _, err := svc.Issue(context.Background(), userID, "ci")
	if err != nil {
		t.Fatal(err)
	}

	u, err := svc.Resolve(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if u.ID != userID {
		t.Fatalf("resolved user id = %d, want %d", u.ID, userID)
	}
}

func TestResolveRejectsUnknownOrRevokedToken(t *testing.T) {
	pool := dbtest.Pool(t)
	userID := seedUser(t, pool, "owner4@example.com")
	svc := New(pool)
	raw, token, err := svc.Issue(context.Background(), userID, "phone")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Resolve(context.Background(), "not-a-real-token"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("err = %v, want ErrInvalidToken", err)
	}

	if err := svc.Revoke(context.Background(), userID, token.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Resolve(context.Background(), raw); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("err after revoke = %v, want ErrInvalidToken", err)
	}
}

func TestRevokeRejectsForeignToken(t *testing.T) {
	pool := dbtest.Pool(t)
	ownerID := seedUser(t, pool, "owner5@example.com")
	strangerID := seedUser(t, pool, "stranger5@example.com")
	svc := New(pool)
	_, token, err := svc.Issue(context.Background(), ownerID, "laptop")
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.Revoke(context.Background(), strangerID, token.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestParseBearerRejectsMissingOrMalformedHeader(t *testing.T) {
	cases := []struct {
		name   string
		header string
	}{
		{"empty", ""},
		{"no prefix", "tok_abc"},
		{"empty token", "Bearer "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := ParseBearer(c.header); !errors.Is(err, ErrInvalidBearer) {
				t.Fatalf("err = %v, want ErrInvalidBearer", err)
			}
		})
	}
}
