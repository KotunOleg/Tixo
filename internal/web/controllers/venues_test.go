package controllers

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
)

func venueForm(name, city string) url.Values {
	return url.Values{"name": {name}, "address": {"1 Test St"}, "city": {city}, "lat": {"50.45"}, "lng": {"30.52"}, "capacity": {"500"}}
}

func TestVenueCrudIsScopedToOrganizer(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	bob := s.signInAs(t, "sub-bob", "bob@example.com", "Bob")
	s.createOrganizer(t, alice, "Kyiv Live", "kyiv-live")
	s.createOrganizer(t, bob, "Lviv Sound", "lviv-sound")

	rec := s.postForm(t, "/org/kyiv-live/venues/new", venueForm("Palats Sportu", "Kyiv"), alice)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/org/kyiv-live/venues" {
		t.Fatalf("create venue = %d → %q\n%s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}

	list := s.request(t, http.MethodGet, "/org/kyiv-live/venues", alice).Body.String()
	if !strings.Contains(list, "Palats Sportu") {
		t.Error("owner's venue list must show the created venue")
	}

	foreignList := s.request(t, http.MethodGet, "/org/kyiv-live/venues", bob)
	if foreignList.Code != http.StatusNotFound {
		t.Errorf("bob (not a member) GET kyiv-live venues = %d, want 404", foreignList.Code)
	}

	q := dbgen.New(s.pool)
	v, err := q.ListVenuesForOrganizer(context.Background(), mustOrgID(t, s, "kyiv-live"))
	if err != nil || len(v) != 1 {
		t.Fatalf("expected one venue, got %+v, %v", v, err)
	}
	id := strconv.FormatInt(v[0].ID, 10)

	editOwn := s.request(t, http.MethodGet, "/org/kyiv-live/venues/"+id+"/edit", alice)
	if editOwn.Code != http.StatusOK || !strings.Contains(editOwn.Body.String(), "Palats Sportu") {
		t.Errorf("owner GET edit = %d, want 200 with venue data", editOwn.Code)
	}

	editForeign := s.request(t, http.MethodGet, "/org/lviv-sound/venues/"+id+"/edit", bob)
	if editForeign.Code != http.StatusNotFound {
		t.Errorf("bob GET lviv-sound/venues/%s/edit = %d, want 404 (venue belongs to alice)", id, editForeign.Code)
	}
}

func mustOrgID(t *testing.T, s *authSite, slug string) int64 {
	t.Helper()
	org, err := dbgen.New(s.pool).GetOrganizerBySlug(context.Background(), slug)
	if err != nil {
		t.Fatal(err)
	}
	return org.ID
}

func TestVenueEditorCanManageVenues(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	bob := s.signInAs(t, "sub-bob", "bob@example.com", "Bob")
	s.createOrganizer(t, alice, "Kyiv Live", "kyiv-live")
	s.postForm(t, "/org/kyiv-live/members", url.Values{"email": {"bob@example.com"}}, alice)

	rec := s.postForm(t, "/org/kyiv-live/venues/new", venueForm("Palats Sportu", "Kyiv"), bob)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("editor create venue = %d\n%s", rec.Code, rec.Body.String())
	}
}

func TestVenueFormValidation(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	s.createOrganizer(t, alice, "Kyiv Live", "kyiv-live")

	cases := []struct {
		form     url.Values
		wantText string
	}{
		{venueForm("", "Kyiv"), "Вкажіть назву."},
		{func() url.Values { f := venueForm("Palats Sportu", "Kyiv"); f.Set("lat", "not-a-number"); return f }(), "Введіть число"},
		{func() url.Values { f := venueForm("Palats Sportu", "Kyiv"); f.Set("capacity", "0"); return f }(), "більшою за нуль"},
	}
	for _, tc := range cases {
		rec := s.postForm(t, "/org/kyiv-live/venues/new", tc.form, alice)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("form %+v: status %d, want 422", tc.form, rec.Code)
			continue
		}
		if !strings.Contains(rec.Body.String(), tc.wantText) {
			t.Errorf("form %+v: body does not contain %q", tc.form, tc.wantText)
		}
	}
}

func TestVenueFormatErrorDoesNotCreateVenueWithGarbageValues(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	s.createOrganizer(t, alice, "Kyiv Live", "kyiv-live")

	form := venueForm("", "Kyiv")
	form.Set("lat", "not-a-number")
	rec := s.postForm(t, "/org/kyiv-live/venues/new", form, alice)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Вкажіть назву.", "Введіть число"} {
		if !strings.Contains(body, want) {
			t.Errorf("422 body does not report %q alongside the other error", want)
		}
	}

	org := mustOrgID(t, s, "kyiv-live")
	vs, err := dbgen.New(s.pool).ListVenuesForOrganizer(context.Background(), org)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Errorf("a format error must not create a venue with defaulted values, got %+v", vs)
	}
}

func TestVenueDuplicateNameRejected(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	s.createOrganizer(t, alice, "Kyiv Live", "kyiv-live")
	s.postForm(t, "/org/kyiv-live/venues/new", venueForm("Palats Sportu", "Kyiv"), alice)

	rec := s.postForm(t, "/org/kyiv-live/venues/new", venueForm("Palats Sportu", "Kyiv"), alice)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "уже є майданчик з такою назвою") {
		t.Errorf("duplicate venue name = %d\n%s", rec.Code, rec.Body.String())
	}
}

func TestVenueDeleteBlockedWhenEventsExist(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	s.createOrganizer(t, alice, "Kyiv Live", "kyiv-live")
	s.postForm(t, "/org/kyiv-live/venues/new", venueForm("Palats Sportu", "Kyiv"), alice)

	org := mustOrgID(t, s, "kyiv-live")
	q := dbgen.New(s.pool)
	vs, err := q.ListVenuesForOrganizer(context.Background(), org)
	if err != nil || len(vs) != 1 {
		t.Fatalf("expected one venue, got %+v, %v", vs, err)
	}
	venueID := vs[0].ID
	start := time.Date(2030, 6, 1, 19, 0, 0, 0, time.UTC)
	if _, err := q.CreateEvent(context.Background(), dbgen.CreateEventParams{
		OrganizerID: org, VenueID: venueID, Slug: "show", Title: "Show", Genre: "rock", StartsAt: start, EndsAt: start.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	id := strconv.FormatInt(venueID, 10)
	rec := s.postForm(t, "/org/kyiv-live/venues/"+id+"/delete", nil, alice)
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "error=has_events") {
		t.Fatalf("delete blocked venue = %d → %q", rec.Code, rec.Header().Get("Location"))
	}

	list := s.request(t, http.MethodGet, rec.Header().Get("Location"), alice).Body.String()
	if !strings.Contains(list, "Palats Sportu") || !strings.Contains(list, "видаліть або перенесіть події") {
		t.Errorf("venue must survive a blocked delete and the list must explain why\n%s", list)
	}
}

func TestVenueDeleteRemovesUnusedVenue(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	s.createOrganizer(t, alice, "Kyiv Live", "kyiv-live")
	s.postForm(t, "/org/kyiv-live/venues/new", venueForm("Palats Sportu", "Kyiv"), alice)

	org := mustOrgID(t, s, "kyiv-live")
	q := dbgen.New(s.pool)
	vs, err := q.ListVenuesForOrganizer(context.Background(), org)
	if err != nil || len(vs) != 1 {
		t.Fatalf("expected one venue, got %+v, %v", vs, err)
	}
	id := strconv.FormatInt(vs[0].ID, 10)

	rec := s.postForm(t, "/org/kyiv-live/venues/"+id+"/delete", nil, alice)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("delete venue = %d\n%s", rec.Code, rec.Body.String())
	}
	list := s.request(t, http.MethodGet, "/org/kyiv-live/venues", alice).Body.String()
	if strings.Contains(list, "Palats Sportu") {
		t.Error("deleted venue must not appear in the list")
	}
}
