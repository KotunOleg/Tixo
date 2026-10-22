package controllers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
)

func (s *authSite) postForm(t *testing.T, target string, form url.Values, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.h.ServeHTTP(rec, req)
	return rec
}

func (s *authSite) createOrganizer(t *testing.T, cookie *http.Cookie, name, slug string) {
	t.Helper()
	rec := s.postForm(t, "/org/new", url.Values{"name": {name}, "slug": {slug}}, cookie)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/org/"+slug {
		t.Fatalf("create %s = %d → %q\n%s", slug, rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
}

func (s *authSite) addEvent(t *testing.T, slug, title string) {
	t.Helper()
	q := dbgen.New(s.pool)
	org, err := q.GetOrganizerBySlug(context.Background(), slug)
	if err != nil {
		t.Fatal(err)
	}
	v, err := q.CreateVenue(context.Background(), dbgen.CreateVenueParams{
		OrganizerID: org.ID, Name: title + " hall", City: "Kyiv", Lat: 50.45, Lng: 30.52, Capacity: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2030, 6, 1, 16, 0, 0, 0, time.UTC)
	if _, err := q.CreateEvent(context.Background(), dbgen.CreateEventParams{
		OrganizerID: org.ID, VenueID: v.ID, Slug: slug + "-show", Title: title,
		StartsAt: start, EndsAt: start.Add(time.Hour), Genre: "rock",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestOrganizerPagesRequireSignIn(t *testing.T) {
	s := newAuthSite(t)
	for _, p := range []string{"/org", "/org/new", "/org/kyiv-live"} {
		rec := s.request(t, http.MethodGet, p, nil)
		if rec.Code != http.StatusFound || !strings.HasPrefix(rec.Header().Get("Location"), "/auth/google/login?next=") {
			t.Errorf("anonymous GET %s = %d → %q, want 302 to sign-in", p, rec.Code, rec.Header().Get("Location"))
		}
	}
	rec := s.request(t, http.MethodPost, "/org/new", nil)
	if rec.Code != http.StatusFound {
		t.Errorf("anonymous POST /org/new = %d, want 302 to sign-in", rec.Code)
	}
}

func TestOrganizerWorkspaceIsIsolatedBetweenTenants(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	bob := s.signInAs(t, "sub-bob", "bob@example.com", "Bob")
	s.createOrganizer(t, alice, "Kyiv Live", "kyiv-live")
	s.createOrganizer(t, bob, "Lviv Sound", "lviv-sound")
	s.addEvent(t, "kyiv-live", "Alice Secret Show")
	s.addEvent(t, "lviv-sound", "Bob Secret Show")

	own := s.request(t, http.MethodGet, "/org/kyiv-live", alice)
	if own.Code != http.StatusOK {
		t.Fatalf("alice /org/kyiv-live = %d, want 200", own.Code)
	}
	body := own.Body.String()
	if !strings.Contains(body, "Kyiv Live") || !strings.Contains(body, "Alice Secret Show") || strings.Contains(body, "Bob Secret Show") {
		t.Error("alice's workspace must show her event and not bob's")
	}

	foreign := s.request(t, http.MethodGet, "/org/lviv-sound", alice)
	missing := s.request(t, http.MethodGet, "/org/no-such-org", alice)
	if foreign.Code != http.StatusNotFound || missing.Code != http.StatusNotFound {
		t.Errorf("alice → bob's workspace = %d, → missing = %d; want 404 for both", foreign.Code, missing.Code)
	}
	if strings.Contains(foreign.Body.String(), "Lviv Sound") || strings.Contains(foreign.Body.String(), "Bob Secret Show") {
		t.Error("404 page leaks bob's workspace data")
	}

	list := s.request(t, http.MethodGet, "/org", alice).Body.String()
	if !strings.Contains(list, "Kyiv Live") || strings.Contains(list, "Lviv Sound") {
		t.Error("alice's organizer list must contain only her organizers")
	}
}

func TestCreateOrganizerFormErrors(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	bob := s.signInAs(t, "sub-bob", "bob@example.com", "Bob")
	s.createOrganizer(t, alice, "Kyiv Live", "kyiv-live")

	cases := []struct {
		name, slug, wantText string
	}{
		{"Kyiv Live 2", "kyiv-live", "Ця адреса вже зайнята."},
		{"Kyiv Live 2", "Kyiv Live", "Лише латинські літери"},
		{"Kyiv Live 2", "new", "Ця адреса зарезервована"},
		{"", "kyiv-two", "Вкажіть назву."},
	}
	for _, tc := range cases {
		rec := s.postForm(t, "/org/new", url.Values{"name": {tc.name}, "slug": {tc.slug}}, bob)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("name=%q slug=%q: status %d, want 422", tc.name, tc.slug, rec.Code)
			continue
		}
		body := rec.Body.String()
		if !strings.Contains(body, tc.wantText) || !strings.Contains(body, `value="`+tc.slug+`"`) {
			t.Errorf("slug=%q: form must show %q and keep the typed values", tc.slug, tc.wantText)
		}
	}
	if rec := s.request(t, http.MethodGet, "/org/kyiv-live", bob); rec.Code != http.StatusNotFound {
		t.Errorf("taken slug gave bob access: %d", rec.Code)
	}
}

func TestEnglishWorkspaceFlowKeepsLanguage(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	rec := s.postForm(t, "/en/org/new", url.Values{"name": {"Kyiv Live"}, "slug": {"kyiv-live"}}, alice)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/en/org/kyiv-live" {
		t.Fatalf("english create = %d → %q, want 303 → /en/org/kyiv-live", rec.Code, rec.Header().Get("Location"))
	}
	body := s.request(t, http.MethodGet, "/en/org/kyiv-live", alice).Body.String()
	if !strings.Contains(body, "No events yet.") || !strings.Contains(body, "Owner") {
		t.Error("english workspace page is not translated")
	}
}

func TestOwnerBuildsAGroupAndOutsidersStayOut(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	bob := s.signInAs(t, "sub-bob", "bob@example.com", "Bob")
	carol := s.signInAs(t, "sub-carol", "carol@example.com", "Carol")
	s.createOrganizer(t, alice, "Kyiv Live", "kyiv-live")
	s.addEvent(t, "kyiv-live", "Alice Secret Show")

	if rec := s.request(t, http.MethodGet, "/org/kyiv-live", bob); rec.Code != http.StatusNotFound {
		t.Fatalf("bob before being added = %d, want 404", rec.Code)
	}
	rec := s.postForm(t, "/org/kyiv-live/members", url.Values{"email": {"Bob@Example.com"}}, alice)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/org/kyiv-live" {
		t.Fatalf("add bob = %d → %q", rec.Code, rec.Header().Get("Location"))
	}
	if body := s.request(t, http.MethodGet, "/org/kyiv-live", alice).Body.String(); !strings.Contains(body, "Учасника додано.") || !strings.Contains(body, "bob@example.com") {
		t.Error("owner page must confirm the addition and list bob")
	}

	asBob := s.request(t, http.MethodGet, "/org/kyiv-live", bob)
	if asBob.Code != http.StatusOK || !strings.Contains(asBob.Body.String(), "Alice Secret Show") {
		t.Errorf("bob as editor = %d, want 200 with the organizer's events", asBob.Code)
	}
	if strings.Contains(asBob.Body.String(), `name="email"`) {
		t.Error("editor sees the add-member form")
	}
	if rec := s.postForm(t, "/org/kyiv-live/members", url.Values{"email": {"carol@example.com"}}, bob); rec.Code != http.StatusForbidden {
		t.Errorf("editor adding a member = %d, want 403", rec.Code)
	}
	if rec := s.postForm(t, "/org/kyiv-live/members", url.Values{"email": {"carol@example.com"}}, carol); rec.Code != http.StatusNotFound {
		t.Errorf("outsider adding a member = %d, want 404", rec.Code)
	}
	if rec := s.request(t, http.MethodGet, "/org/kyiv-live", carol); rec.Code != http.StatusNotFound {
		t.Errorf("carol = %d, want 404: only alice and bob are in the group", rec.Code)
	}
}

func TestAddMemberFormErrors(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	s.createOrganizer(t, alice, "Kyiv Live", "kyiv-live")
	cases := map[string]string{
		"nobody@example.com": "Користувача з таким email ще немає",
		"alice@example.com":  "Ця людина вже є учасником.",
		"oops":               "Це не схоже на email.",
	}
	for email, want := range cases {
		rec := s.postForm(t, "/org/kyiv-live/members", url.Values{"email": {email}}, alice)
		body := rec.Body.String()
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(body, want) || !strings.Contains(body, `aria-invalid="true"`) {
			t.Errorf("email=%q: %d, want 422 with %q marked invalid", email, rec.Code, want)
		}
	}
}

func TestCreateOrganizerErrorsAreAnnouncedOnTheField(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	body := s.postForm(t, "/org/new", url.Values{"name": {""}, "slug": {"Kyiv Live"}}, alice).Body.String()
	for _, want := range []string{
		`id="org-name" name="name" value="" aria-invalid="true" aria-describedby="org-name-error"`,
		`id="org-name-error"`,
		`aria-invalid="true" aria-describedby="org-slug-help org-slug-error"`,
		`id="org-slug-error"`,
		`href="#org-name"`,
		`href="#org-slug"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("422 form lacks %s: screen readers do not hear which field is wrong", want)
		}
	}
	if clean := s.request(t, http.MethodGet, "/org/new", alice).Body.String(); strings.Contains(clean, `aria-invalid`) {
		t.Error("empty form already marks fields invalid")
	}
}

func TestEventsTableIsKeyboardScrollable(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	s.createOrganizer(t, alice, "Kyiv Live", "kyiv-live")
	s.addEvent(t, "kyiv-live", "Alice Show")
	body := s.request(t, http.MethodGet, "/org/kyiv-live", alice).Body.String()
	if !strings.Contains(body, `class="table-responsive" tabindex="0" role="region" aria-labelledby="events-title"`) {
		t.Error("events table scrolls sideways on phones but its container cannot be focused from the keyboard")
	}
}
