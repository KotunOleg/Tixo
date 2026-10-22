package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/KotunOleg/Tixo/internal/apitokens"
	"github.com/KotunOleg/Tixo/internal/auth"
	"github.com/KotunOleg/Tixo/internal/db/dbtest"
	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
)

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestRequireAPIUser(t *testing.T) {
	pool := dbtest.Pool(t)
	user, err := dbgen.New(pool).CreateUser(context.Background(), dbgen.CreateUserParams{Email: "owner@example.com", Name: "Owner"})
	if err != nil {
		t.Fatal(err)
	}
	tokens := apitokens.New(pool)
	raw, token, err := tokens.Issue(context.Background(), user.ID, "ci")
	if err != nil {
		t.Fatal(err)
	}
	if err := tokens.Revoke(context.Background(), user.ID, token.ID); err != nil {
		t.Fatal(err)
	}
	revoked := raw

	raw2, _, err := tokens.Issue(context.Background(), user.ID, "laptop")
	if err != nil {
		t.Fatal(err)
	}

	var gotUserID int64
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, _ := auth.CurrentUser(r.Context())
		gotUserID = u.ID
		w.WriteHeader(http.StatusOK)
	})
	handler := RequireAPIUser(tokens, discardLog())(next)

	cases := []struct {
		name   string
		header string
		want   int
	}{
		{"missing header", "", http.StatusUnauthorized},
		{"malformed header", raw2, http.StatusUnauthorized},
		{"unknown token", "Bearer not-a-real-token", http.StatusUnauthorized},
		{"revoked token", "Bearer " + revoked, http.StatusUnauthorized},
		{"valid token", "Bearer " + raw2, http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotUserID = 0
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/organizers", nil)
			if c.header != "" {
				req.Header.Set("Authorization", c.header)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, c.want, rec.Body.String())
			}
			if c.want == http.StatusOK && gotUserID != user.ID {
				t.Fatalf("context user id = %d, want %d", gotUserID, user.ID)
			}
		})
	}
}
