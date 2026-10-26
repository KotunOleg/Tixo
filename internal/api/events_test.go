package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/tiers"
	"github.com/KotunOleg/Tixo/internal/venues"
)

func (s testSite) createVenue(t *testing.T, org dbgen.Organizer, name string) int64 {
	t.Helper()
	v, err := s.venues.Create(t.Context(), org.ID, venues.Input{Name: name, Address: "1 Street", City: "Kyiv", Lat: 50.45, Lng: 30.52, Capacity: 100})
	if err != nil {
		t.Fatal(err)
	}
	return v.ID
}

func TestCreateEventAndChangeStatus(t *testing.T) {
	s := newTestSite(t)
	ownerID := s.createUser(t, "owner20@example.com")
	org := s.createOrganizer(t, ownerID, "Kyiv Live", "kyiv-live-20")
	token := s.tokenFor(t, ownerID)
	venueID := s.createVenue(t, org, "Hall 20")

	rec := s.request(t, http.MethodPost, "/api/v1/organizers/kyiv-live-20/events", token, fmt.Sprintf(
		`{"venueId":%d,"slug":"show-20","title":"Show","startsAt":"2030-06-01T19:00:00Z","endsAt":"2030-06-01T21:00:00Z","genre":"rock"}`, venueID))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create event = %d: %s", rec.Code, rec.Body.String())
	}
	var created eventJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Status != "draft" {
		t.Fatalf("status = %q, want draft", created.Status)
	}

	publish := s.request(t, http.MethodPost, fmt.Sprintf("/api/v1/organizers/kyiv-live-20/events/%d/status", created.ID), token, `{"status":"published"}`)
	if publish.Code != http.StatusOK {
		t.Fatalf("publish = %d: %s", publish.Code, publish.Body.String())
	}
	var published eventJSON
	if err := json.Unmarshal(publish.Body.Bytes(), &published); err != nil {
		t.Fatal(err)
	}
	if published.Status != "published" {
		t.Fatalf("status = %q, want published", published.Status)
	}
}

func TestSetStatusRejectsInvalidValue(t *testing.T) {
	s := newTestSite(t)
	ownerID := s.createUser(t, "owner21@example.com")
	org := s.createOrganizer(t, ownerID, "Kyiv Live", "kyiv-live-21")
	token := s.tokenFor(t, ownerID)
	venueID := s.createVenue(t, org, "Hall 21")
	rec := s.request(t, http.MethodPost, "/api/v1/organizers/kyiv-live-21/events", token, fmt.Sprintf(
		`{"venueId":%d,"slug":"show-21","title":"Show","startsAt":"2030-06-01T19:00:00Z","endsAt":"2030-06-01T21:00:00Z","genre":"rock"}`, venueID))
	var created eventJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	bad := s.request(t, http.MethodPost, fmt.Sprintf("/api/v1/organizers/kyiv-live-21/events/%d/status", created.ID), token, `{"status":"on-sale"}`)
	if bad.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", bad.Code, bad.Body.String())
	}
}

func TestDeleteEventWithTiersConflicts(t *testing.T) {
	s := newTestSite(t)
	ownerID := s.createUser(t, "owner22@example.com")
	org := s.createOrganizer(t, ownerID, "Kyiv Live", "kyiv-live-22")
	token := s.tokenFor(t, ownerID)
	venueID := s.createVenue(t, org, "Hall 22")
	rec := s.request(t, http.MethodPost, "/api/v1/organizers/kyiv-live-22/events", token, fmt.Sprintf(
		`{"venueId":%d,"slug":"show-22","title":"Show","startsAt":"2030-06-01T19:00:00Z","endsAt":"2030-06-01T21:00:00Z","genre":"rock"}`, venueID))
	var created eventJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if _, err := s.tiers.Create(t.Context(), org.ID, created.ID, tiers.Input{Name: "Standard", Price: 50000, Quantity: 100}); err != nil {
		t.Fatal(err)
	}

	del := s.request(t, http.MethodDelete, fmt.Sprintf("/api/v1/organizers/kyiv-live-22/events/%d", created.ID), token, "")
	if del.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", del.Code, del.Body.String())
	}
}

func TestGetEventRejectsForeignOrganizer(t *testing.T) {
	s := newTestSite(t)
	ownerID := s.createUser(t, "owner23@example.com")
	strangerID := s.createUser(t, "stranger23@example.com")
	org := s.createOrganizer(t, ownerID, "Kyiv Live", "kyiv-live-23")
	s.createOrganizer(t, strangerID, "Lviv Sound", "lviv-sound-23")
	token := s.tokenFor(t, ownerID)
	strangerToken := s.tokenFor(t, strangerID)
	venueID := s.createVenue(t, org, "Hall 23")
	rec := s.request(t, http.MethodPost, "/api/v1/organizers/kyiv-live-23/events", token, fmt.Sprintf(
		`{"venueId":%d,"slug":"show-23","title":"Show","startsAt":"2030-06-01T19:00:00Z","endsAt":"2030-06-01T21:00:00Z","genre":"rock"}`, venueID))
	var created eventJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	foreign := s.request(t, http.MethodGet, fmt.Sprintf("/api/v1/organizers/lviv-sound-23/events/%d", created.ID), strangerToken, "")
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", foreign.Code, foreign.Body.String())
	}
}

func TestListEventsCachesSecondRequest(t *testing.T) {
	s := newTestSite(t)
	ownerID := s.createUser(t, "owner24@example.com")
	org := s.createOrganizer(t, ownerID, "Kyiv Live", "kyiv-live-24")
	token := s.tokenFor(t, ownerID)
	venueID := s.createVenue(t, org, "Hall 24")
	create := s.request(t, http.MethodPost, "/api/v1/organizers/kyiv-live-24/events", token, fmt.Sprintf(
		`{"venueId":%d,"slug":"show-24","title":"Show","startsAt":"2030-06-01T19:00:00Z","endsAt":"2030-06-01T21:00:00Z","genre":"rock","priceFrom":1500,"posterKey":"posters/24.jpg"}`, venueID))
	if create.Code != http.StatusCreated {
		t.Fatalf("create event = %d: %s", create.Code, create.Body.String())
	}

	first := s.request(t, http.MethodGet, "/api/v1/organizers/kyiv-live-24/events", token, "")
	if first.Code != http.StatusOK {
		t.Fatalf("list = %d: %s", first.Code, first.Body.String())
	}
	if got := first.Header().Get("X-Cache"); got != "MISS" {
		t.Fatalf("X-Cache = %q, want MISS", got)
	}

	second := s.request(t, http.MethodGet, "/api/v1/organizers/kyiv-live-24/events", token, "")
	if got := second.Header().Get("X-Cache"); got != "HIT" {
		t.Fatalf("X-Cache = %q, want HIT", got)
	}
	if first.Body.String() != second.Body.String() {
		t.Fatalf("cached body differs from original:\nfirst:  %s\nsecond: %s", first.Body.String(), second.Body.String())
	}
}

func TestShowEventCachesSecondRequest(t *testing.T) {
	s := newTestSite(t)
	ownerID := s.createUser(t, "owner25@example.com")
	org := s.createOrganizer(t, ownerID, "Kyiv Live", "kyiv-live-25")
	token := s.tokenFor(t, ownerID)
	venueID := s.createVenue(t, org, "Hall 25")
	create := s.request(t, http.MethodPost, "/api/v1/organizers/kyiv-live-25/events", token, fmt.Sprintf(
		`{"venueId":%d,"slug":"show-25","title":"Show","startsAt":"2030-06-01T19:00:00Z","endsAt":"2030-06-01T21:00:00Z","genre":"rock"}`, venueID))
	var created eventJSON
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	path := fmt.Sprintf("/api/v1/organizers/kyiv-live-25/events/%d", created.ID)
	first := s.request(t, http.MethodGet, path, token, "")
	if got := first.Header().Get("X-Cache"); got != "MISS" {
		t.Fatalf("X-Cache = %q, want MISS", got)
	}
	second := s.request(t, http.MethodGet, path, token, "")
	if got := second.Header().Get("X-Cache"); got != "HIT" {
		t.Fatalf("X-Cache = %q, want HIT", got)
	}
}

func TestUpdateEventInvalidatesListAndShowCache(t *testing.T) {
	s := newTestSite(t)
	ownerID := s.createUser(t, "owner26@example.com")
	org := s.createOrganizer(t, ownerID, "Kyiv Live", "kyiv-live-26")
	token := s.tokenFor(t, ownerID)
	venueID := s.createVenue(t, org, "Hall 26")
	create := s.request(t, http.MethodPost, "/api/v1/organizers/kyiv-live-26/events", token, fmt.Sprintf(
		`{"venueId":%d,"slug":"show-26","title":"Show","startsAt":"2030-06-01T19:00:00Z","endsAt":"2030-06-01T21:00:00Z","genre":"rock"}`, venueID))
	var created eventJSON
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	listPath := "/api/v1/organizers/kyiv-live-26/events"
	showPath := fmt.Sprintf("/api/v1/organizers/kyiv-live-26/events/%d", created.ID)
	s.request(t, http.MethodGet, listPath, token, "")
	s.request(t, http.MethodGet, showPath, token, "")

	update := s.request(t, http.MethodPatch, showPath, token, fmt.Sprintf(
		`{"venueId":%d,"slug":"show-26","title":"Renamed Show","startsAt":"2030-06-01T19:00:00Z","endsAt":"2030-06-01T21:00:00Z","genre":"rock"}`, venueID))
	if update.Code != http.StatusOK {
		t.Fatalf("update = %d: %s", update.Code, update.Body.String())
	}

	listAfter := s.request(t, http.MethodGet, listPath, token, "")
	if got := listAfter.Header().Get("X-Cache"); got != "MISS" {
		t.Fatalf("X-Cache after update = %q, want MISS (list cache not invalidated)", got)
	}
	showAfter := s.request(t, http.MethodGet, showPath, token, "")
	if got := showAfter.Header().Get("X-Cache"); got != "MISS" {
		t.Fatalf("X-Cache after update = %q, want MISS (show cache not invalidated)", got)
	}
	var updated eventJSON
	if err := json.Unmarshal(showAfter.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Title != "Renamed Show" {
		t.Fatalf("title = %q, want Renamed Show (stale cache served)", updated.Title)
	}
}

func TestSetStatusInvalidatesListCache(t *testing.T) {
	s := newTestSite(t)
	ownerID := s.createUser(t, "owner27@example.com")
	org := s.createOrganizer(t, ownerID, "Kyiv Live", "kyiv-live-27")
	token := s.tokenFor(t, ownerID)
	venueID := s.createVenue(t, org, "Hall 27")
	create := s.request(t, http.MethodPost, "/api/v1/organizers/kyiv-live-27/events", token, fmt.Sprintf(
		`{"venueId":%d,"slug":"show-27","title":"Show","startsAt":"2030-06-01T19:00:00Z","endsAt":"2030-06-01T21:00:00Z","genre":"rock"}`, venueID))
	var created eventJSON
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	listPath := "/api/v1/organizers/kyiv-live-27/events"
	s.request(t, http.MethodGet, listPath, token, "")

	publish := s.request(t, http.MethodPost, fmt.Sprintf("/api/v1/organizers/kyiv-live-27/events/%d/status", created.ID), token, `{"status":"published"}`)
	if publish.Code != http.StatusOK {
		t.Fatalf("publish = %d: %s", publish.Code, publish.Body.String())
	}

	listAfter := s.request(t, http.MethodGet, listPath, token, "")
	if got := listAfter.Header().Get("X-Cache"); got != "MISS" {
		t.Fatalf("X-Cache after status change = %q, want MISS (list cache not invalidated)", got)
	}
}

func TestDeleteEventInvalidatesListCache(t *testing.T) {
	s := newTestSite(t)
	ownerID := s.createUser(t, "owner28@example.com")
	org := s.createOrganizer(t, ownerID, "Kyiv Live", "kyiv-live-28")
	token := s.tokenFor(t, ownerID)
	venueID := s.createVenue(t, org, "Hall 28")
	create := s.request(t, http.MethodPost, "/api/v1/organizers/kyiv-live-28/events", token, fmt.Sprintf(
		`{"venueId":%d,"slug":"show-28","title":"Show","startsAt":"2030-06-01T19:00:00Z","endsAt":"2030-06-01T21:00:00Z","genre":"rock"}`, venueID))
	var created eventJSON
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	listPath := "/api/v1/organizers/kyiv-live-28/events"
	s.request(t, http.MethodGet, listPath, token, "")

	del := s.request(t, http.MethodDelete, fmt.Sprintf("/api/v1/organizers/kyiv-live-28/events/%d", created.ID), token, "")
	if del.Code != http.StatusNoContent {
		t.Fatalf("delete = %d: %s", del.Code, del.Body.String())
	}

	listAfter := s.request(t, http.MethodGet, listPath, token, "")
	if got := listAfter.Header().Get("X-Cache"); got != "MISS" {
		t.Fatalf("X-Cache after delete = %q, want MISS (list cache not invalidated)", got)
	}
}
