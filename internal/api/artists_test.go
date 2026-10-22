package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestCreateArtistAndSearch(t *testing.T) {
	s := newTestSite(t)
	ownerID := s.createUser(t, "owner50@example.com")
	token := s.tokenFor(t, ownerID)

	rec := s.request(t, http.MethodPost, "/api/v1/artists", token, `{"name":"Okean Elzy"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create artist = %d: %s", rec.Code, rec.Body.String())
	}

	search := s.request(t, http.MethodGet, "/api/v1/artists?q=okean", token, "")
	if search.Code != http.StatusOK {
		t.Fatalf("search = %d: %s", search.Code, search.Body.String())
	}
	var page struct {
		Items []artistJSON `json:"items"`
		Count int64        `json:"count"`
	}
	if err := json.Unmarshal(search.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Count != 1 || page.Items[0].Name != "Okean Elzy" {
		t.Fatalf("page = %+v, want one Okean Elzy", page)
	}
}

func TestSearchArtistsReturnsEmptyArrayNotNull(t *testing.T) {
	s := newTestSite(t)
	ownerID := s.createUser(t, "owner51@example.com")
	token := s.tokenFor(t, ownerID)

	rec := s.request(t, http.MethodGet, "/api/v1/artists?q=nobody-matches-this", token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["items"]) != "[]" {
		t.Fatalf("items = %s, want []", raw["items"])
	}
}

func TestCreateArtistRejectsBlankName(t *testing.T) {
	s := newTestSite(t)
	ownerID := s.createUser(t, "owner52@example.com")
	token := s.tokenFor(t, ownerID)

	rec := s.request(t, http.MethodPost, "/api/v1/artists", token, `{"name":""}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
}
