package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/KotunOleg/Tixo/internal/apitokens"
	"github.com/KotunOleg/Tixo/internal/artists"
	"github.com/KotunOleg/Tixo/internal/db/dbtest"
	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/events"
	"github.com/KotunOleg/Tixo/internal/lineup"
	"github.com/KotunOleg/Tixo/internal/orders"
	"github.com/KotunOleg/Tixo/internal/organizers"
	"github.com/KotunOleg/Tixo/internal/tiers"
	"github.com/KotunOleg/Tixo/internal/venues"
)

type testSite struct {
	h       http.Handler
	pool    *pgxpool.Pool
	orgs    *organizers.Service
	venues  *venues.Service
	events  *events.Service
	tiers   *tiers.Service
	lineup  *lineup.Service
	artists *artists.Service
	tokens  *apitokens.Service
	orders  *orders.Service
}

func newTestSite(t *testing.T) testSite {
	t.Helper()
	pool := dbtest.Pool(t)
	s := testSite{
		pool: pool, orgs: organizers.New(pool), venues: venues.New(pool), events: events.New(pool), tiers: tiers.New(pool),
		lineup: lineup.New(pool), artists: artists.New(pool), tokens: apitokens.New(pool),
		orders: orders.New(pool),
	}
	r := chi.NewRouter()
	Mount(r, Deps{
		Organizers: s.orgs, Venues: s.venues, Events: s.events, Tiers: s.tiers, Lineup: s.lineup, Artists: s.artists, Orders: s.orders,
		APITokens: s.tokens, Logger: discardLog(),
	})
	s.h = r
	return s
}

func (s testSite) createUser(t *testing.T, email string) int64 {
	t.Helper()
	u, err := dbgen.New(s.pool).CreateUser(context.Background(), dbgen.CreateUserParams{Email: email, Name: "Test"})
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

func (s testSite) tokenFor(t *testing.T, userID int64) string {
	t.Helper()
	raw, _, err := s.tokens.Issue(context.Background(), userID, "test")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func (s testSite) createOrganizer(t *testing.T, ownerID int64, name, slug string) dbgen.Organizer {
	t.Helper()
	org, err := s.orgs.Create(context.Background(), ownerID, organizers.CreateInput{Name: name, Slug: slug})
	if err != nil {
		t.Fatal(err)
	}
	return org
}

func (s testSite) addEditor(t *testing.T, org dbgen.Organizer, ownerID, editorID int64, editorEmail string) {
	t.Helper()
	if _, err := s.orgs.AddMember(context.Background(), organizers.Membership{Organizer: org, Role: organizers.RoleOwner}, editorEmail); err != nil {
		t.Fatal(err)
	}
}

func (s testSite) request(t *testing.T, method, target, bearer string, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequestWithContext(t.Context(), method, target, r)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	s.h.ServeHTTP(rec, req)
	return rec
}

func (s testSite) addVenueWithEvent(t *testing.T, org dbgen.Organizer, slug string) int64 {
	t.Helper()
	ctx := context.Background()
	v, err := s.venues.Create(ctx, org.ID, venues.Input{Name: "Hall", Address: "1 Street", City: "Kyiv", Lat: 50.45, Lng: 30.52, Capacity: 100})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2030, 6, 1, 19, 0, 0, 0, time.UTC)
	if _, err := s.events.Create(ctx, org.ID, events.Input{
		VenueID: v.ID, Slug: slug, Title: "Show", StartsAt: start, EndsAt: start.Add(time.Hour), Genre: "rock",
	}); err != nil {
		t.Fatal(err)
	}
	return v.ID
}
