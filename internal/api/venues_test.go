package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestGetVenueAllowsSharedVenueListedInTheSameOrganizer(t *testing.T) {
	s := newTestSite(t)
	ownerID := s.createUser(t, "owner60@example.com")
	s.createOrganizer(t, ownerID, "Kyiv Live", "kyiv-live-60")
	token := s.tokenFor(t, ownerID)

	var sharedID int64
	if err := s.pool.QueryRow(t.Context(),
		"insert into venues (organizer_id, name, address, city, lat, lng, capacity) values (null, 'Shared Hall', 'addr', 'Kyiv', 50.45, 30.52, 100) returning id",
	).Scan(&sharedID); err != nil {
		t.Fatal(err)
	}

	list := s.request(t, http.MethodGet, "/api/v1/organizers/kyiv-live-60/venues", token, "")
	if !strings.Contains(list.Body.String(), `"shared":true`) {
		t.Fatalf("list does not show the shared venue: %s", list.Body.String())
	}

	get := s.request(t, http.MethodGet, fmt.Sprintf("/api/v1/organizers/kyiv-live-60/venues/%d", sharedID), token, "")
	if get.Code != http.StatusOK {
		t.Fatalf("GET shared venue by id = %d, want 200 (it is listed above): %s", get.Code, get.Body.String())
	}
}

func TestListVenuesReturnsPaginatedEnvelope(t *testing.T) {
	s := newTestSite(t)
	ownerID := s.createUser(t, "owner6@example.com")
	s.createOrganizer(t, ownerID, "Kyiv Live", "kyiv-live-6")
	token := s.tokenFor(t, ownerID)
	for i := range 3 {
		rec := s.request(t, http.MethodPost, "/api/v1/organizers/kyiv-live-6/venues", token,
			fmt.Sprintf(`{"name":"Hall %d","address":"1 Street","city":"Kyiv","lat":50.45,"lng":30.52,"capacity":100}`, i))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create venue %d = %d: %s", i, rec.Code, rec.Body.String())
		}
	}

	rec := s.request(t, http.MethodGet, "/api/v1/organizers/kyiv-live-6/venues?skip=0&limit=2", token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var page struct {
		Items    []venueJSON `json:"items"`
		Count    int64       `json:"count"`
		NextLink *string     `json:"nextLink"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Count != 3 || len(page.Items) != 2 || page.NextLink == nil {
		t.Fatalf("page = %+v, want count 3, 2 items, a nextLink", page)
	}
}

func TestCreateVenueValidatesInput(t *testing.T) {
	s := newTestSite(t)
	ownerID := s.createUser(t, "owner7@example.com")
	s.createOrganizer(t, ownerID, "Kyiv Live", "kyiv-live-7")
	token := s.tokenFor(t, ownerID)

	rec := s.request(t, http.MethodPost, "/api/v1/organizers/kyiv-live-7/venues", token, `{"name":"","city":"Kyiv","address":"1 Street","lat":50.45,"lng":30.52,"capacity":100}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
}

func TestGetVenueRejectsForeignOrganizer(t *testing.T) {
	s := newTestSite(t)
	ownerID := s.createUser(t, "owner8@example.com")
	strangerID := s.createUser(t, "stranger8@example.com")
	s.createOrganizer(t, strangerID, "Lviv Sound", "lviv-sound-8")
	s.createOrganizer(t, ownerID, "Kyiv Live", "kyiv-live-8")
	ownerToken := s.tokenFor(t, ownerID)
	strangerToken := s.tokenFor(t, strangerID)

	rec := s.request(t, http.MethodPost, "/api/v1/organizers/kyiv-live-8/venues", ownerToken, `{"name":"Hall","address":"1 Street","city":"Kyiv","lat":50.45,"lng":30.52,"capacity":100}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create venue = %d: %s", rec.Code, rec.Body.String())
	}
	var created venueJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	foreign := s.request(t, http.MethodGet, fmt.Sprintf("/api/v1/organizers/lviv-sound-8/venues/%d", created.ID), strangerToken, "")
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("foreign slug lookup = %d, want 404: %s", foreign.Code, foreign.Body.String())
	}
}

func TestDeleteVenueWithEventsConflicts(t *testing.T) {
	s := newTestSite(t)
	ownerID := s.createUser(t, "owner9@example.com")
	org := s.createOrganizer(t, ownerID, "Kyiv Live", "kyiv-live-9")
	token := s.tokenFor(t, ownerID)
	venueID := s.addVenueWithEvent(t, org, "kyiv-live-9-show")

	rec := s.request(t, http.MethodDelete, fmt.Sprintf("/api/v1/organizers/kyiv-live-9/venues/%d", venueID), token, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
}

func TestDeleteVenueSucceeds(t *testing.T) {
	s := newTestSite(t)
	ownerID := s.createUser(t, "owner10@example.com")
	s.createOrganizer(t, ownerID, "Kyiv Live", "kyiv-live-10")
	token := s.tokenFor(t, ownerID)
	rec := s.request(t, http.MethodPost, "/api/v1/organizers/kyiv-live-10/venues", token, `{"name":"Hall","address":"1 Street","city":"Kyiv","lat":50.45,"lng":30.52,"capacity":100}`)
	var created venueJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	del := s.request(t, http.MethodDelete, fmt.Sprintf("/api/v1/organizers/kyiv-live-10/venues/%d", created.ID), token, "")
	if del.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", del.Code, del.Body.String())
	}
}

func TestListVenuesCachesSecondRequest(t *testing.T) {
	s := newTestSite(t)
	ownerID := s.createUser(t, "owner70@example.com")
	s.createOrganizer(t, ownerID, "Kyiv Live", "kyiv-live-70")
	token := s.tokenFor(t, ownerID)
	create := s.request(t, http.MethodPost, "/api/v1/organizers/kyiv-live-70/venues", token,
		`{"name":"Hall 70","address":"1 Street","city":"Kyiv","lat":50.45,"lng":30.52,"capacity":100}`)
	if create.Code != http.StatusCreated {
		t.Fatalf("create venue = %d: %s", create.Code, create.Body.String())
	}

	first := s.request(t, http.MethodGet, "/api/v1/organizers/kyiv-live-70/venues", token, "")
	if got := first.Header().Get("X-Cache"); got != "MISS" {
		t.Fatalf("X-Cache = %q, want MISS", got)
	}
	second := s.request(t, http.MethodGet, "/api/v1/organizers/kyiv-live-70/venues", token, "")
	if got := second.Header().Get("X-Cache"); got != "HIT" {
		t.Fatalf("X-Cache = %q, want HIT", got)
	}
	if first.Body.String() != second.Body.String() {
		t.Fatalf("cached body differs from original:\nfirst:  %s\nsecond: %s", first.Body.String(), second.Body.String())
	}
}

func TestShowVenueCachesSecondRequest(t *testing.T) {
	s := newTestSite(t)
	ownerID := s.createUser(t, "owner71@example.com")
	s.createOrganizer(t, ownerID, "Kyiv Live", "kyiv-live-71")
	token := s.tokenFor(t, ownerID)
	create := s.request(t, http.MethodPost, "/api/v1/organizers/kyiv-live-71/venues", token,
		`{"name":"Hall 71","address":"1 Street","city":"Kyiv","lat":50.45,"lng":30.52,"capacity":100}`)
	var created venueJSON
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	path := fmt.Sprintf("/api/v1/organizers/kyiv-live-71/venues/%d", created.ID)
	first := s.request(t, http.MethodGet, path, token, "")
	if got := first.Header().Get("X-Cache"); got != "MISS" {
		t.Fatalf("X-Cache = %q, want MISS", got)
	}
	second := s.request(t, http.MethodGet, path, token, "")
	if got := second.Header().Get("X-Cache"); got != "HIT" {
		t.Fatalf("X-Cache = %q, want HIT", got)
	}
}

func TestUpdateVenueInvalidatesListAndShowCache(t *testing.T) {
	s := newTestSite(t)
	ownerID := s.createUser(t, "owner72@example.com")
	s.createOrganizer(t, ownerID, "Kyiv Live", "kyiv-live-72")
	token := s.tokenFor(t, ownerID)
	create := s.request(t, http.MethodPost, "/api/v1/organizers/kyiv-live-72/venues", token,
		`{"name":"Hall 72","address":"1 Street","city":"Kyiv","lat":50.45,"lng":30.52,"capacity":100}`)
	var created venueJSON
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	listPath := "/api/v1/organizers/kyiv-live-72/venues"
	showPath := fmt.Sprintf("/api/v1/organizers/kyiv-live-72/venues/%d", created.ID)
	s.request(t, http.MethodGet, listPath, token, "")
	s.request(t, http.MethodGet, showPath, token, "")

	update := s.request(t, http.MethodPatch, showPath, token,
		`{"name":"Renamed Hall","address":"1 Street","city":"Kyiv","lat":50.45,"lng":30.52,"capacity":100}`)
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
	var updated venueJSON
	if err := json.Unmarshal(showAfter.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Name != "Renamed Hall" {
		t.Fatalf("name = %q, want Renamed Hall (stale cache served)", updated.Name)
	}
}

func TestDeleteVenueInvalidatesListCache(t *testing.T) {
	s := newTestSite(t)
	ownerID := s.createUser(t, "owner73@example.com")
	s.createOrganizer(t, ownerID, "Kyiv Live", "kyiv-live-73")
	token := s.tokenFor(t, ownerID)
	create := s.request(t, http.MethodPost, "/api/v1/organizers/kyiv-live-73/venues", token,
		`{"name":"Hall 73","address":"1 Street","city":"Kyiv","lat":50.45,"lng":30.52,"capacity":100}`)
	var created venueJSON
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	listPath := "/api/v1/organizers/kyiv-live-73/venues"
	s.request(t, http.MethodGet, listPath, token, "")

	del := s.request(t, http.MethodDelete, fmt.Sprintf("/api/v1/organizers/kyiv-live-73/venues/%d", created.ID), token, "")
	if del.Code != http.StatusNoContent {
		t.Fatalf("delete = %d: %s", del.Code, del.Body.String())
	}

	listAfter := s.request(t, http.MethodGet, listPath, token, "")
	if got := listAfter.Header().Get("X-Cache"); got != "MISS" {
		t.Fatalf("X-Cache after delete = %q, want MISS (list cache not invalidated)", got)
	}
}
