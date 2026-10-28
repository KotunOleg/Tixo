package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/features"
	"github.com/KotunOleg/Tixo/internal/orders"
)

func (s testSite) setPlan(t *testing.T, org dbgen.Organizer, plan string) {
	t.Helper()
	if _, err := dbgen.New(s.pool).SetOrganizerPlan(t.Context(), dbgen.SetOrganizerPlanParams{ID: org.ID, Plan: plan}); err != nil {
		t.Fatal(err)
	}
}

func TestAnalyticsAccessMatrix(t *testing.T) {
	s := newTestSite(t)
	ctx := t.Context()
	premiumOwner := s.createUser(t, "premium@example.com")
	premiumEditor := s.createUser(t, "editor@example.com")
	freeOwner := s.createUser(t, "free@example.com")
	overrideOwner := s.createUser(t, "override@example.com")
	stranger := s.createUser(t, "stranger@example.com")

	premium := s.createOrganizer(t, premiumOwner, "Kyiv Live", "kyiv-live")
	s.setPlan(t, premium, "premium")
	s.addEditor(t, premium, premiumOwner, premiumEditor, "editor@example.com")
	s.createOrganizer(t, freeOwner, "Lviv Sound", "lviv-sound")
	override := s.createOrganizer(t, overrideOwner, "Odesa Jazz", "odesa-jazz")
	if err := s.flags.Enable(ctx, override.ID, features.SalesAnalytics); err != nil {
		t.Fatal(err)
	}

	revokedUser := s.createUser(t, "revoked@example.com")
	s.createOrganizer(t, revokedUser, "Revoked Org", "revoked-org")
	revoked, tok, err := s.tokens.Issue(ctx, revokedUser, "old")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.tokens.Revoke(ctx, revokedUser, tok.ID); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name      string
		slug      string
		bearer    string
		query     string
		wantCode  int
		wantError string
	}{
		{"no token", "kyiv-live", "", "", http.StatusUnauthorized, "unauthorized"},
		{"forged token", "kyiv-live", "tixo_forged", "", http.StatusUnauthorized, "unauthorized"},
		{"revoked token", "revoked-org", revoked, "", http.StatusUnauthorized, "unauthorized"},
		{"member of another organizer", "kyiv-live", s.tokenFor(t, stranger), "", http.StatusNotFound, "organizer not found"},
		{"free plan member", "lviv-sound", s.tokenFor(t, freeOwner), "", http.StatusForbidden, errFeatureMissing},
		{"free plan with flag override", "odesa-jazz", s.tokenFor(t, overrideOwner), "", http.StatusOK, ""},
		{"premium owner", "kyiv-live", s.tokenFor(t, premiumOwner), "", http.StatusOK, ""},
		{"premium editor", "kyiv-live", s.tokenFor(t, premiumEditor), "", http.StatusOK, ""},
		{"days not a number", "kyiv-live", s.tokenFor(t, premiumOwner), "?days=abc", http.StatusBadRequest, ""},
		{"days above the limit", "kyiv-live", s.tokenFor(t, premiumOwner), "?days=91", http.StatusBadRequest, ""},
		{"bad days is rejected before the feature check", "lviv-sound", s.tokenFor(t, freeOwner), "?days=91", http.StatusBadRequest, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := s.request(t, http.MethodGet, "/api/v1/organizers/"+tc.slug+"/analytics"+tc.query, tc.bearer, "")
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.wantError == "" {
				return
			}
			var body struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error != tc.wantError {
				t.Errorf("error = %q, %v; want %q", body.Error, err, tc.wantError)
			}
		})
	}
}

func TestAnalyticsJSONReflectsPurchases(t *testing.T) {
	s := newTestSite(t)
	tierID := s.eventOnSale(t, "kyiv-live", "big-show", 50, true)
	org, err := dbgen.New(s.pool).GetOrganizerBySlug(t.Context(), "kyiv-live")
	if err != nil {
		t.Fatal(err)
	}
	s.setPlan(t, org, "premium")
	buyer := s.createUser(t, "buyer@example.com")
	if _, _, err := s.orders.Purchase(t.Context(), buyer, orders.Input{TierID: tierID, Quantity: 3, Key: uuid.New()}); err != nil {
		t.Fatal(err)
	}

	rec := s.request(t, http.MethodGet, "/api/v1/organizers/kyiv-live/analytics?days=7", s.tokenFor(t, mustOwner(t, s, org)), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var got analyticsJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Days != 7 || len(got.ByDay) != 7 || got.ByDay[6].Date != got.To {
		t.Errorf("window = %d days, %d points, last %q, to %q", got.Days, len(got.ByDay), got.ByDay[len(got.ByDay)-1].Date, got.To)
	}
	if got.Totals != (analyticsTotalsJSON{Tickets: 3, Revenue: 150000, PublishedEvents: 1}) {
		t.Errorf("totals = %+v", got.Totals)
	}
	if len(got.ByEvent) != 1 || got.ByEvent[0].Kind != rowKindEvent || got.ByEvent[0].Slug != "big-show" || got.ByEvent[0].Tickets != 3 {
		t.Errorf("by_event = %+v", got.ByEvent)
	}
	if len(got.ByGenre) != 1 || got.ByGenre[0] != (analyticsGenreJSON{Genre: "rock", Events: 1}) {
		t.Errorf("by_genre = %+v", got.ByGenre)
	}
}

func mustOwner(t *testing.T, s testSite, org dbgen.Organizer) int64 {
	t.Helper()
	var id int64
	if err := s.pool.QueryRow(t.Context(), "SELECT user_id FROM memberships WHERE organizer_id = $1 AND role = 'owner'", org.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
