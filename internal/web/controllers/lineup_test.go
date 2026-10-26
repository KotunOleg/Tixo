package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
)

func TestLineupAddExistingArtistAndList(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	s.createOrganizer(t, alice, "Kyiv Live", "kyiv-live")
	venueID := mustEventVenue(t, s, "kyiv-live", alice)
	s.postMultipart(t, "/org/kyiv-live/events/new", eventFields(venueID, "big-show"), "", "", nil, alice)
	id := strconv.FormatInt(mustShowEventID(t, s, "kyiv-live"), 10)

	a, err := dbgen.New(s.pool).CreateArtist(context.Background(), dbgen.CreateArtistParams{Name: "Okean Elzy", SortName: "Okean Elzy"})
	if err != nil {
		t.Fatal(err)
	}

	rec := s.postForm(t, "/org/kyiv-live/events/"+id+"/lineup", url.Values{"artist_id": {strconv.FormatInt(a.ID, 10)}, "is_headliner": {"1"}}, alice)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("add to lineup = %d\n%s", rec.Code, rec.Body.String())
	}

	show := s.request(t, http.MethodGet, "/org/kyiv-live/events/"+id, alice).Body.String()
	if !strings.Contains(show, "Okean Elzy") {
		t.Errorf("event page must show the added artist, got:\n%s", show)
	}
	if !strings.Contains(show, "Виконавця додано до лінійки.") {
		t.Error("event page must show the lineup-added flash")
	}
}

func TestLineupAddDoesNotDuplicateOnConflict(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	s.createOrganizer(t, alice, "Kyiv Live", "kyiv-live")
	venueID := mustEventVenue(t, s, "kyiv-live", alice)
	s.postMultipart(t, "/org/kyiv-live/events/new", eventFields(venueID, "big-show"), "", "", nil, alice)
	id := strconv.FormatInt(mustShowEventID(t, s, "kyiv-live"), 10)
	eventID, _ := strconv.ParseInt(id, 10, 64)

	a, err := dbgen.New(s.pool).CreateArtist(context.Background(), dbgen.CreateArtistParams{Name: "Okean Elzy", SortName: "Okean Elzy"})
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"artist_id": {strconv.FormatInt(a.ID, 10)}}
	s.postForm(t, "/org/kyiv-live/events/"+id+"/lineup", form, alice)
	s.postForm(t, "/org/kyiv-live/events/"+id+"/lineup", form, alice)

	rows, err := dbgen.New(s.pool).ListEventArtists(context.Background(), eventID)
	if err != nil || len(rows) != 1 {
		t.Errorf("adding the same artist twice must not duplicate, got %+v, %v", rows, err)
	}
}

func TestLineupAddCreatesNewArtistByName(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	s.createOrganizer(t, alice, "Kyiv Live", "kyiv-live")
	venueID := mustEventVenue(t, s, "kyiv-live", alice)
	s.postMultipart(t, "/org/kyiv-live/events/new", eventFields(venueID, "big-show"), "", "", nil, alice)
	id := strconv.FormatInt(mustShowEventID(t, s, "kyiv-live"), 10)

	rec := s.postForm(t, "/org/kyiv-live/events/"+id+"/lineup", url.Values{"new_artist_name": {"Brand New Band"}}, alice)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("add new artist = %d\n%s", rec.Code, rec.Body.String())
	}

	show := s.request(t, http.MethodGet, "/org/kyiv-live/events/"+id, alice).Body.String()
	if !strings.Contains(show, "Brand New Band") {
		t.Errorf("event page must show the newly created artist, got:\n%s", show)
	}
}

func TestLineupAddRejectsForeignEvent(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	bob := s.signInAs(t, "sub-bob", "bob@example.com", "Bob")
	s.createOrganizer(t, alice, "Kyiv Live", "kyiv-live")
	s.createOrganizer(t, bob, "Lviv Sound", "lviv-sound")
	venueID := mustEventVenue(t, s, "kyiv-live", alice)
	s.postMultipart(t, "/org/kyiv-live/events/new", eventFields(venueID, "big-show"), "", "", nil, alice)
	id := strconv.FormatInt(mustShowEventID(t, s, "kyiv-live"), 10)

	a, err := dbgen.New(s.pool).CreateArtist(context.Background(), dbgen.CreateArtistParams{Name: "Okean Elzy", SortName: "Okean Elzy"})
	if err != nil {
		t.Fatal(err)
	}
	rec := s.postForm(t, "/org/lviv-sound/events/"+id+"/lineup", url.Values{"artist_id": {strconv.FormatInt(a.ID, 10)}}, bob)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bob adding to alice's event via lviv-sound = %d, want 404", rec.Code)
	}
}

func TestLineupSearchFindsArtistsAndHidesAlreadyAdded(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	s.createOrganizer(t, alice, "Kyiv Live", "kyiv-live")
	venueID := mustEventVenue(t, s, "kyiv-live", alice)
	s.postMultipart(t, "/org/kyiv-live/events/new", eventFields(venueID, "big-show"), "", "", nil, alice)
	id := strconv.FormatInt(mustShowEventID(t, s, "kyiv-live"), 10)

	added, err := dbgen.New(s.pool).CreateArtist(context.Background(), dbgen.CreateArtistParams{Name: "Okean Elzy", SortName: "Okean Elzy"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dbgen.New(s.pool).CreateArtist(context.Background(), dbgen.CreateArtistParams{Name: "Okean Jazz Trio", SortName: "Okean Jazz Trio"}); err != nil {
		t.Fatal(err)
	}
	s.postForm(t, "/org/kyiv-live/events/"+id+"/lineup", url.Values{"artist_id": {strconv.FormatInt(added.ID, 10)}}, alice)

	show := s.request(t, http.MethodGet, "/org/kyiv-live/events/"+id+"?q=Okean", alice).Body.String()
	if !strings.Contains(show, "Okean Jazz Trio") {
		t.Errorf("search results must include the unadded match, got:\n%s", show)
	}
	if strings.Count(show, "Okean Elzy") != 1 {
		t.Errorf("already-added artist must appear in the lineup but not again in search hits, got:\n%s", show)
	}
}

func TestLineupSearchEndpointReturnsJSONMatches(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	s.createOrganizer(t, alice, "Kyiv Live", "kyiv-live")
	venueID := mustEventVenue(t, s, "kyiv-live", alice)
	s.postMultipart(t, "/org/kyiv-live/events/new", eventFields(venueID, "big-show"), "", "", nil, alice)
	id := strconv.FormatInt(mustShowEventID(t, s, "kyiv-live"), 10)

	a, err := dbgen.New(s.pool).CreateArtist(context.Background(), dbgen.CreateArtistParams{Name: "Okean Elzy", SortName: "Okean Elzy"})
	if err != nil {
		t.Fatal(err)
	}

	rec := s.request(t, http.MethodGet, "/org/kyiv-live/events/"+id+"/lineup/search?q=Okean", alice)
	if rec.Code != http.StatusOK {
		t.Fatalf("search = %d\n%s", rec.Code, rec.Body.String())
	}
	var results []artistSearchResult
	if err := json.Unmarshal(rec.Body.Bytes(), &results); err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != a.ID || results[0].Label != "Okean Elzy" {
		t.Errorf("results = %+v, want one Okean Elzy", results)
	}
}

func TestLineupSearchEndpointRejectsShortQuery(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	s.createOrganizer(t, alice, "Kyiv Live", "kyiv-live")
	venueID := mustEventVenue(t, s, "kyiv-live", alice)
	s.postMultipart(t, "/org/kyiv-live/events/new", eventFields(venueID, "big-show"), "", "", nil, alice)
	id := strconv.FormatInt(mustShowEventID(t, s, "kyiv-live"), 10)

	if _, err := dbgen.New(s.pool).CreateArtist(context.Background(), dbgen.CreateArtistParams{Name: "Okean Elzy", SortName: "Okean Elzy"}); err != nil {
		t.Fatal(err)
	}

	rec := s.request(t, http.MethodGet, "/org/kyiv-live/events/"+id+"/lineup/search?q=Ok", alice)
	if rec.Code != http.StatusOK {
		t.Fatalf("search = %d\n%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "[]\n" {
		t.Errorf("short query body = %q, want empty array", rec.Body.String())
	}
}

func TestLineupSearchEndpointRejectsForeignSlug(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	bob := s.signInAs(t, "sub-bob", "bob@example.com", "Bob")
	s.createOrganizer(t, alice, "Kyiv Live", "kyiv-live")
	s.createOrganizer(t, bob, "Lviv Sound", "lviv-sound")
	venueID := mustEventVenue(t, s, "kyiv-live", alice)
	s.postMultipart(t, "/org/kyiv-live/events/new", eventFields(venueID, "big-show"), "", "", nil, alice)
	id := strconv.FormatInt(mustShowEventID(t, s, "kyiv-live"), 10)

	rec := s.request(t, http.MethodGet, "/org/lviv-sound/events/"+id+"/lineup/search?q=Okean", bob)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bob searching via lviv-sound = %d, want 404", rec.Code)
	}
}

func TestLineupSearchEndpointRequiresAuth(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	s.createOrganizer(t, alice, "Kyiv Live", "kyiv-live")
	venueID := mustEventVenue(t, s, "kyiv-live", alice)
	s.postMultipart(t, "/org/kyiv-live/events/new", eventFields(venueID, "big-show"), "", "", nil, alice)
	id := strconv.FormatInt(mustShowEventID(t, s, "kyiv-live"), 10)

	rec := s.request(t, http.MethodGet, "/org/kyiv-live/events/"+id+"/lineup/search?q=Okean", nil)
	if rec.Code != http.StatusFound {
		t.Fatalf("anonymous search = %d, want redirect to login", rec.Code)
	}
}

func TestLineupRemoveDeletesEntry(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	s.createOrganizer(t, alice, "Kyiv Live", "kyiv-live")
	venueID := mustEventVenue(t, s, "kyiv-live", alice)
	s.postMultipart(t, "/org/kyiv-live/events/new", eventFields(venueID, "big-show"), "", "", nil, alice)
	id := strconv.FormatInt(mustShowEventID(t, s, "kyiv-live"), 10)
	eventID, _ := strconv.ParseInt(id, 10, 64)

	a, err := dbgen.New(s.pool).CreateArtist(context.Background(), dbgen.CreateArtistParams{Name: "Okean Elzy", SortName: "Okean Elzy"})
	if err != nil {
		t.Fatal(err)
	}
	s.postForm(t, "/org/kyiv-live/events/"+id+"/lineup", url.Values{"artist_id": {strconv.FormatInt(a.ID, 10)}}, alice)

	rec := s.postForm(t, "/org/kyiv-live/events/"+id+"/lineup/"+strconv.FormatInt(a.ID, 10)+"/remove", nil, alice)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("remove from lineup = %d\n%s", rec.Code, rec.Body.String())
	}
	rows, err := dbgen.New(s.pool).ListEventArtists(context.Background(), eventID)
	if err != nil || len(rows) != 0 {
		t.Errorf("lineup must be empty after remove, got %+v, %v", rows, err)
	}
}

func TestLineupRemoveRejectsForeignEvent(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	bob := s.signInAs(t, "sub-bob", "bob@example.com", "Bob")
	s.createOrganizer(t, alice, "Kyiv Live", "kyiv-live")
	s.createOrganizer(t, bob, "Lviv Sound", "lviv-sound")
	venueID := mustEventVenue(t, s, "kyiv-live", alice)
	s.postMultipart(t, "/org/kyiv-live/events/new", eventFields(venueID, "big-show"), "", "", nil, alice)
	id := strconv.FormatInt(mustShowEventID(t, s, "kyiv-live"), 10)
	eventID, _ := strconv.ParseInt(id, 10, 64)

	a, err := dbgen.New(s.pool).CreateArtist(context.Background(), dbgen.CreateArtistParams{Name: "Okean Elzy", SortName: "Okean Elzy"})
	if err != nil {
		t.Fatal(err)
	}
	s.postForm(t, "/org/kyiv-live/events/"+id+"/lineup", url.Values{"artist_id": {strconv.FormatInt(a.ID, 10)}}, alice)

	rec := s.postForm(t, "/org/lviv-sound/events/"+id+"/lineup/"+strconv.FormatInt(a.ID, 10)+"/remove", nil, bob)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bob removing from alice's event via lviv-sound = %d, want 404", rec.Code)
	}
	rows, _ := dbgen.New(s.pool).ListEventArtists(context.Background(), eventID)
	if len(rows) != 1 {
		t.Errorf("lineup must survive a foreign-tenant remove attempt, got %+v", rows)
	}
}
