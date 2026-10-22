package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestListOrganizersReturnsOnlyOwn(t *testing.T) {
	s := newTestSite(t)
	ownerID := s.createUser(t, "owner@example.com")
	strangerID := s.createUser(t, "stranger@example.com")
	s.createOrganizer(t, ownerID, "Kyiv Live", "kyiv-live")
	s.createOrganizer(t, strangerID, "Lviv Sound", "lviv-sound")
	token := s.tokenFor(t, ownerID)

	rec := s.request(t, http.MethodGet, "/api/v1/organizers", token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var page struct {
		Items []organizerJSON `json:"items"`
		Count int64           `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Count != 1 || len(page.Items) != 1 || page.Items[0].Slug != "kyiv-live" {
		t.Fatalf("page = %+v, want only kyiv-live", page)
	}
}

func TestGetOrganizerRejectsForeignSlug(t *testing.T) {
	s := newTestSite(t)
	ownerID := s.createUser(t, "owner2@example.com")
	strangerID := s.createUser(t, "stranger2@example.com")
	s.createOrganizer(t, ownerID, "Kyiv Live", "kyiv-live-2")
	token := s.tokenFor(t, strangerID)

	rec := s.request(t, http.MethodGet, "/api/v1/organizers/kyiv-live-2", token, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

func TestUpdateOrganizerRejectsEditor(t *testing.T) {
	s := newTestSite(t)
	ownerID := s.createUser(t, "owner3@example.com")
	editorID := s.createUser(t, "editor3@example.com")
	org := s.createOrganizer(t, ownerID, "Kyiv Live", "kyiv-live-3")
	s.addEditor(t, org, ownerID, editorID, "editor3@example.com")
	token := s.tokenFor(t, editorID)

	rec := s.request(t, http.MethodPatch, "/api/v1/organizers/kyiv-live-3", token, `{"name":"New Name"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
	}
}

func TestUpdateOrganizerValidatesName(t *testing.T) {
	s := newTestSite(t)
	ownerID := s.createUser(t, "owner4@example.com")
	s.createOrganizer(t, ownerID, "Kyiv Live", "kyiv-live-4")
	token := s.tokenFor(t, ownerID)

	rec := s.request(t, http.MethodPatch, "/api/v1/organizers/kyiv-live-4", token, `{"name":""}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"name"`) {
		t.Fatalf("body missing field error: %s", rec.Body.String())
	}
}

func TestUpdateOrganizerSavesNewName(t *testing.T) {
	s := newTestSite(t)
	ownerID := s.createUser(t, "owner5@example.com")
	s.createOrganizer(t, ownerID, "Kyiv Live", "kyiv-live-5")
	token := s.tokenFor(t, ownerID)

	rec := s.request(t, http.MethodPatch, "/api/v1/organizers/kyiv-live-5", token, `{"name":"Kyiv Live Renamed"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var org organizerJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &org); err != nil {
		t.Fatal(err)
	}
	if org.Name != "Kyiv Live Renamed" {
		t.Fatalf("name = %q, want updated name", org.Name)
	}
}
