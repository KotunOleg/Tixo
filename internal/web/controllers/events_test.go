package controllers

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
)

func (s *authSite) postMultipart(t *testing.T, target string, fields map[string]string, fileField, filename string, fileContent []byte, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if filename != "" {
		fw, err := w.CreateFormFile(fileField, filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(fileContent); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, target, &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.h.ServeHTTP(rec, req)
	return rec
}

func mustEventVenue(t *testing.T, s *authSite, slug string, cookie *http.Cookie) string {
	t.Helper()
	rec := s.postForm(t, "/org/"+slug+"/venues/new", venueForm("Palats Sportu", "Kyiv"), cookie)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("create venue = %d\n%s", rec.Code, rec.Body.String())
	}
	org := mustOrgID(t, s, slug)
	vs, err := dbgen.New(s.pool).ListVenuesForOrganizer(context.Background(), org)
	if err != nil || len(vs) == 0 {
		t.Fatalf("expected a venue, got %+v, %v", vs, err)
	}
	return strconv.FormatInt(vs[0].ID, 10)
}

func eventFields(venueID, slug string) map[string]string {
	return map[string]string{
		"title": "Big Show", "slug": slug, "venue_id": venueID,
		"starts_at": "2030-06-01T19:00", "ends_at": "2030-06-01T21:00", "genre": "rock",
	}
}

var pngBytes = []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0}

func TestEventCrudIsScopedToOrganizer(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	bob := s.signInAs(t, "sub-bob", "bob@example.com", "Bob")
	s.createOrganizer(t, alice, "Kyiv Live", "kyiv-live")
	s.createOrganizer(t, bob, "Lviv Sound", "lviv-sound")
	venueID := mustEventVenue(t, s, "kyiv-live", alice)

	rec := s.postMultipart(t, "/org/kyiv-live/events/new", eventFields(venueID, "big-show"), "", "", nil, alice)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/org/kyiv-live/events" {
		t.Fatalf("create event = %d → %q\n%s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}

	list := s.request(t, http.MethodGet, "/org/kyiv-live/events", alice).Body.String()
	if !strings.Contains(list, "Big Show") {
		t.Error("owner's event list must show the created event")
	}

	foreignList := s.request(t, http.MethodGet, "/org/kyiv-live/events", bob)
	if foreignList.Code != http.StatusNotFound {
		t.Errorf("bob (not a member) GET kyiv-live events = %d, want 404", foreignList.Code)
	}

	org := mustOrgID(t, s, "kyiv-live")
	evs, err := dbgen.New(s.pool).ListEventsForOrganizer(context.Background(), dbgen.ListEventsForOrganizerParams{OrganizerID: org, Lim: 10})
	if err != nil || len(evs) != 1 {
		t.Fatalf("expected one event, got %+v, %v", evs, err)
	}
	if evs[0].Status != "draft" {
		t.Errorf("new event status = %q, want draft", evs[0].Status)
	}

	id := strconv.FormatInt(evs[0].ID, 10)
	foreignEdit := s.request(t, http.MethodGet, "/org/lviv-sound/events/"+id+"/edit", bob)
	if foreignEdit.Code != http.StatusNotFound {
		t.Errorf("bob GET lviv-sound/events/%s/edit = %d, want 404 (event belongs to alice)", id, foreignEdit.Code)
	}
}

func TestEventRejectsVenueFromAnotherOrganizer(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	bob := s.signInAs(t, "sub-bob", "bob@example.com", "Bob")
	s.createOrganizer(t, alice, "Kyiv Live", "kyiv-live")
	s.createOrganizer(t, bob, "Lviv Sound", "lviv-sound")
	foreignVenue := mustEventVenue(t, s, "lviv-sound", bob)

	rec := s.postMultipart(t, "/org/kyiv-live/events/new", eventFields(foreignVenue, "big-show"), "", "", nil, alice)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "недоступний для вашої організації") {
		t.Errorf("create with foreign venue = %d\n%s", rec.Code, rec.Body.String())
	}
}

func TestEventCreateDoesNotOrphanPosterOnValidationFailure(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	bob := s.signInAs(t, "sub-bob", "bob@example.com", "Bob")
	s.createOrganizer(t, alice, "Kyiv Live", "kyiv-live")
	s.createOrganizer(t, bob, "Lviv Sound", "lviv-sound")
	foreignVenue := mustEventVenue(t, s, "lviv-sound", bob)

	rec := s.postMultipart(t, "/org/kyiv-live/events/new", eventFields(foreignVenue, "big-show"), "poster", "poster.png", pngBytes, alice)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("create with foreign venue and poster = %d\n%s", rec.Code, rec.Body.String())
	}
	entries, err := os.ReadDir(s.storageDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("a rejected create must not leave the uploaded poster on disk, found %v", entries)
	}
}

func TestEventFormValidation(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	s.createOrganizer(t, alice, "Kyiv Live", "kyiv-live")
	venueID := mustEventVenue(t, s, "kyiv-live", alice)

	cases := []struct {
		mutate   func(f map[string]string)
		wantText string
	}{
		{func(f map[string]string) { f["title"] = "" }, "Вкажіть назву."},
		{func(f map[string]string) { f["ends_at"] = f["starts_at"] }, "закінчуватися пізніше"},
		{func(f map[string]string) { f["genre"] = "polka" }, "Оберіть жанр"},
		{func(f map[string]string) { f["starts_at"] = "not-a-date" }, "коректні дату й час"},
	}
	for _, tc := range cases {
		f := eventFields(venueID, "big-show")
		tc.mutate(f)
		rec := s.postMultipart(t, "/org/kyiv-live/events/new", f, "", "", nil, alice)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("fields %+v: status %d, want 422", f, rec.Code)
			continue
		}
		if !strings.Contains(rec.Body.String(), tc.wantText) {
			t.Errorf("fields %+v: body does not contain %q", f, tc.wantText)
		}
	}
}

func TestEventPosterUploadAndReplace(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	s.createOrganizer(t, alice, "Kyiv Live", "kyiv-live")
	venueID := mustEventVenue(t, s, "kyiv-live", alice)

	rec := s.postMultipart(t, "/org/kyiv-live/events/new", eventFields(venueID, "big-show"), "poster", "poster.png", pngBytes, alice)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("create with poster = %d\n%s", rec.Code, rec.Body.String())
	}
	org := mustOrgID(t, s, "kyiv-live")
	evs, err := dbgen.New(s.pool).ListEventsForOrganizer(context.Background(), dbgen.ListEventsForOrganizerParams{OrganizerID: org, Lim: 10})
	if err != nil || len(evs) != 1 || !evs[0].PosterKey.Valid {
		t.Fatalf("expected event with a poster, got %+v, %v", evs, err)
	}
	firstKey := evs[0].PosterKey.String
	if _, err := s.deps.Storage.Open(firstKey); err != nil {
		t.Errorf("uploaded poster not found in storage: %v", err)
	}

	id := strconv.FormatInt(evs[0].ID, 10)
	f := eventFields(venueID, "big-show")
	rec = s.postMultipart(t, "/org/kyiv-live/events/"+id+"/edit", f, "poster", "poster2.png", pngBytes, alice)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("update with new poster = %d\n%s", rec.Code, rec.Body.String())
	}
	updated, err := dbgen.New(s.pool).GetEventForOrganizer(context.Background(), dbgen.GetEventForOrganizerParams{ID: evs[0].ID, OrganizerID: org})
	if err != nil || !updated.PosterKey.Valid || updated.PosterKey.String == firstKey {
		t.Fatalf("expected a new poster key, got %+v, %v", updated, err)
	}
	if _, err := s.deps.Storage.Open(firstKey); err == nil {
		t.Error("old poster must be deleted from storage after replacement")
	}
}

func TestEventUpdateDoesNotOrphanNewPosterOnValidationFailure(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	bob := s.signInAs(t, "sub-bob", "bob@example.com", "Bob")
	s.createOrganizer(t, alice, "Kyiv Live", "kyiv-live")
	s.createOrganizer(t, bob, "Lviv Sound", "lviv-sound")
	venueID := mustEventVenue(t, s, "kyiv-live", alice)
	foreignVenue := mustEventVenue(t, s, "lviv-sound", bob)

	s.postMultipart(t, "/org/kyiv-live/events/new", eventFields(venueID, "big-show"), "poster", "poster.png", pngBytes, alice)
	org := mustOrgID(t, s, "kyiv-live")
	evs, err := dbgen.New(s.pool).ListEventsForOrganizer(context.Background(), dbgen.ListEventsForOrganizerParams{OrganizerID: org, Lim: 10})
	if err != nil || len(evs) != 1 || !evs[0].PosterKey.Valid {
		t.Fatalf("expected event with a poster, got %+v, %v", evs, err)
	}
	firstKey := evs[0].PosterKey.String
	id := strconv.FormatInt(evs[0].ID, 10)

	f := eventFields(foreignVenue, "big-show")
	rec := s.postMultipart(t, "/org/kyiv-live/events/"+id+"/edit", f, "poster", "poster2.png", pngBytes, alice)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("update with foreign venue and new poster = %d\n%s", rec.Code, rec.Body.String())
	}
	if _, err := s.deps.Storage.Open(firstKey); err != nil {
		t.Errorf("original poster must survive a rejected update: %v", err)
	}
	entries, err := os.ReadDir(s.storageDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("a rejected update must not leave the newly uploaded poster on disk, found %v", entries)
	}
}

func TestEventPosterRejectsBadType(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	s.createOrganizer(t, alice, "Kyiv Live", "kyiv-live")
	venueID := mustEventVenue(t, s, "kyiv-live", alice)

	rec := s.postMultipart(t, "/org/kyiv-live/events/new", eventFields(venueID, "big-show"), "poster", "poster.txt", []byte("not an image"), alice)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "JPEG, PNG або WebP") {
		t.Errorf("bad poster type = %d\n%s", rec.Code, rec.Body.String())
	}
	org := mustOrgID(t, s, "kyiv-live")
	evs, err := dbgen.New(s.pool).ListEventsForOrganizer(context.Background(), dbgen.ListEventsForOrganizerParams{OrganizerID: org, Lim: 10})
	if err != nil || len(evs) != 0 {
		t.Errorf("a rejected poster must not create the event, got %+v, %v", evs, err)
	}
}

func TestEventStatusTransitions(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	s.createOrganizer(t, alice, "Kyiv Live", "kyiv-live")
	venueID := mustEventVenue(t, s, "kyiv-live", alice)
	s.postMultipart(t, "/org/kyiv-live/events/new", eventFields(venueID, "big-show"), "", "", nil, alice)
	org := mustOrgID(t, s, "kyiv-live")
	evs, _ := dbgen.New(s.pool).ListEventsForOrganizer(context.Background(), dbgen.ListEventsForOrganizerParams{OrganizerID: org, Lim: 10})
	id := strconv.FormatInt(evs[0].ID, 10)

	show := s.request(t, http.MethodGet, "/org/kyiv-live/events/"+id, alice).Body.String()
	if !strings.Contains(show, "Чернетка") {
		t.Error("new event must show as draft")
	}

	rec := s.postForm(t, "/org/kyiv-live/events/"+id+"/publish", nil, alice)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("publish = %d\n%s", rec.Code, rec.Body.String())
	}
	show = s.request(t, http.MethodGet, "/org/kyiv-live/events/"+id, alice).Body.String()
	if !strings.Contains(show, "Опублікована") {
		t.Error("event must show as published after publish")
	}
}

func TestEventDeleteRemovesEventAndPoster(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	s.createOrganizer(t, alice, "Kyiv Live", "kyiv-live")
	venueID := mustEventVenue(t, s, "kyiv-live", alice)
	s.postMultipart(t, "/org/kyiv-live/events/new", eventFields(venueID, "big-show"), "poster", "poster.png", pngBytes, alice)
	org := mustOrgID(t, s, "kyiv-live")
	evs, _ := dbgen.New(s.pool).ListEventsForOrganizer(context.Background(), dbgen.ListEventsForOrganizerParams{OrganizerID: org, Lim: 10})
	id := strconv.FormatInt(evs[0].ID, 10)
	posterKey := evs[0].PosterKey.String

	rec := s.postForm(t, "/org/kyiv-live/events/"+id+"/delete", nil, alice)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("delete = %d\n%s", rec.Code, rec.Body.String())
	}
	if _, err := dbgen.New(s.pool).GetEventForOrganizer(context.Background(), dbgen.GetEventForOrganizerParams{ID: evs[0].ID, OrganizerID: org}); err == nil {
		t.Error("event must be gone after delete")
	}
	if _, err := s.deps.Storage.Open(posterKey); err == nil {
		t.Error("poster must be removed from storage after the event is deleted")
	}
}
