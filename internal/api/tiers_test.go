package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func (s testSite) createEventFor(t *testing.T, token, slug, eventSlug string, venueID int64) eventJSON {
	t.Helper()
	rec := s.request(t, http.MethodPost, fmt.Sprintf("/api/v1/organizers/%s/events", slug), token,
		fmt.Sprintf(`{"venueId":%d,"slug":%q,"title":"Show","startsAt":"2030-06-01T19:00:00Z","endsAt":"2030-06-01T21:00:00Z","genre":"rock"}`, venueID, eventSlug))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create event = %d: %s", rec.Code, rec.Body.String())
	}
	var e eventJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatal(err)
	}
	return e
}

func (s testSite) markTierSold(t *testing.T, tierID int64) {
	t.Helper()
	if _, err := s.pool.Exec(t.Context(), "UPDATE ticket_tiers SET available = available - 1 WHERE id = $1", tierID); err != nil {
		t.Fatal(err)
	}
}

func TestCreateTierAndDeleteUnsold(t *testing.T) {
	s := newTestSite(t)
	ownerID := s.createUser(t, "owner30@example.com")
	org := s.createOrganizer(t, ownerID, "Kyiv Live", "kyiv-live-30")
	token := s.tokenFor(t, ownerID)
	venueID := s.createVenue(t, org, "Hall 30")
	event := s.createEventFor(t, token, "kyiv-live-30", "show-30", venueID)

	rec := s.request(t, http.MethodPost, fmt.Sprintf("/api/v1/organizers/kyiv-live-30/events/%d/tiers", event.ID), token, `{"name":"Standard","price":50000,"quantity":100}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create tier = %d: %s", rec.Code, rec.Body.String())
	}
	var tier tierJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &tier); err != nil {
		t.Fatal(err)
	}
	if tier.Available != 100 {
		t.Fatalf("available = %d, want 100", tier.Available)
	}

	del := s.request(t, http.MethodDelete, fmt.Sprintf("/api/v1/organizers/kyiv-live-30/events/%d/tiers/%d", event.ID, tier.ID), token, "")
	if del.Code != http.StatusNoContent {
		t.Fatalf("delete tier = %d: %s", del.Code, del.Body.String())
	}
}

func TestCreateTierValidatesInput(t *testing.T) {
	s := newTestSite(t)
	ownerID := s.createUser(t, "owner31@example.com")
	org := s.createOrganizer(t, ownerID, "Kyiv Live", "kyiv-live-31")
	token := s.tokenFor(t, ownerID)
	venueID := s.createVenue(t, org, "Hall 31")
	event := s.createEventFor(t, token, "kyiv-live-31", "show-31", venueID)

	rec := s.request(t, http.MethodPost, fmt.Sprintf("/api/v1/organizers/kyiv-live-31/events/%d/tiers", event.ID), token, `{"name":"","price":50000,"quantity":100}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
}

func TestDeleteSoldTierConflicts(t *testing.T) {
	s := newTestSite(t)
	ownerID := s.createUser(t, "owner32@example.com")
	org := s.createOrganizer(t, ownerID, "Kyiv Live", "kyiv-live-32")
	token := s.tokenFor(t, ownerID)
	venueID := s.createVenue(t, org, "Hall 32")
	event := s.createEventFor(t, token, "kyiv-live-32", "show-32", venueID)
	rec := s.request(t, http.MethodPost, fmt.Sprintf("/api/v1/organizers/kyiv-live-32/events/%d/tiers", event.ID), token, `{"name":"Standard","price":50000,"quantity":100}`)
	var tier tierJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &tier); err != nil {
		t.Fatal(err)
	}
	s.markTierSold(t, tier.ID)

	del := s.request(t, http.MethodDelete, fmt.Sprintf("/api/v1/organizers/kyiv-live-32/events/%d/tiers/%d", event.ID, tier.ID), token, "")
	if del.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", del.Code, del.Body.String())
	}
}
