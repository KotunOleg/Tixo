package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestAddAndRemoveLineupArtist(t *testing.T) {
	s := newTestSite(t)
	ownerID := s.createUser(t, "owner40@example.com")
	org := s.createOrganizer(t, ownerID, "Kyiv Live", "kyiv-live-40")
	token := s.tokenFor(t, ownerID)
	venueID := s.createVenue(t, org, "Hall 40")
	event := s.createEventFor(t, token, "kyiv-live-40", "show-40", venueID)
	artist, err := s.artists.Create(context.Background(), "Okean Elzy")
	if err != nil {
		t.Fatal(err)
	}

	rec := s.request(t, http.MethodPost, fmt.Sprintf("/api/v1/organizers/kyiv-live-40/events/%d/lineup", event.ID), token,
		fmt.Sprintf(`{"artistId":%d,"isHeadliner":true}`, artist.ID))
	if rec.Code != http.StatusCreated {
		t.Fatalf("add lineup = %d: %s", rec.Code, rec.Body.String())
	}

	list := s.request(t, http.MethodGet, fmt.Sprintf("/api/v1/organizers/kyiv-live-40/events/%d/lineup", event.ID), token, "")
	var page struct {
		Items []lineupEntryJSON `json:"items"`
		Count int64             `json:"count"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Count != 1 || page.Items[0].Artist.Name != "Okean Elzy" || !page.Items[0].IsHeadliner {
		t.Fatalf("lineup = %+v, want one headliner Okean Elzy", page)
	}

	del := s.request(t, http.MethodDelete, fmt.Sprintf("/api/v1/organizers/kyiv-live-40/events/%d/lineup/%d", event.ID, artist.ID), token, "")
	if del.Code != http.StatusNoContent {
		t.Fatalf("remove lineup = %d: %s", del.Code, del.Body.String())
	}
}

func TestAddLineupArtistRejectsUnknownArtist(t *testing.T) {
	s := newTestSite(t)
	ownerID := s.createUser(t, "owner41@example.com")
	org := s.createOrganizer(t, ownerID, "Kyiv Live", "kyiv-live-41")
	token := s.tokenFor(t, ownerID)
	venueID := s.createVenue(t, org, "Hall 41")
	event := s.createEventFor(t, token, "kyiv-live-41", "show-41", venueID)

	rec := s.request(t, http.MethodPost, fmt.Sprintf("/api/v1/organizers/kyiv-live-41/events/%d/lineup", event.ID), token, `{"artistId":999999}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
}
