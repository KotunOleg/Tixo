package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

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
