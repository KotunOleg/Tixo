//go:build ui

package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
)

type uiServer struct {
	Base       string            `json:"base"`
	NoAuth     string            `json:"no_auth"`
	CookieName string            `json:"cookie_name"`
	Cookies    map[string]string `json:"cookies"`
}

var uiAuthErrors = []string{msgAuthState, msgAuthProvider, msgAuthUnverified, msgAuthTaken}

func TestUISnapshotServer(t *testing.T) {
	out := os.Getenv("UI_OUT")
	if out == "" {
		t.Skip("UI_OUT not set: this test only serves demo pages for the screenshot harness")
	}
	var site *authSite
	site = newAuthSiteWith(t, nil, func(r chi.Router) {
		r.Get("/ui/auth-error/{kind}", func(w http.ResponseWriter, r *http.Request) {
			id := "auth.error." + chi.URLParam(r, "kind")
			if !slices.Contains(uiAuthErrors, id) {
				site.deps.View.NotFound(w, r)
				return
			}
			site.deps.View.Render(w, r, http.StatusBadRequest, "pages/auth_error", authErrorData{MessageID: id})
		})
		r.Get("/ui/boom", func(http.ResponseWriter, *http.Request) { panic("ui snapshot: server error page") })
	})

	owner := site.signInAs(t, "sub-owner", "oleksandra.kostiantynopolska-shevchenko@example.com", "Олександра-Вікторія Костянтинопольська-Шевченко")
	member := site.signInAs(t, "sub-member", "bob@example.com", "Bob")
	newcomer := site.signInAs(t, "sub-newcomer", "carol@example.com", "Carol")

	site.createOrganizer(t, owner, "Київська міська філармонія імені Миколи Лисенка та асоціація камерної музики", "kyiv-philharmonic")
	site.createOrganizer(t, owner, "Lviv Sound", "lviv-sound")
	site.createOrganizer(t, owner, "Odesa Jazz Fest", "odesa-jazz")
	site.seedEvents(t, "kyiv-philharmonic", []uiEvent{
		{"Симфонічний вечір: Лятошинський, Сильвестров і прем'єра твору молодих композиторів", "published", 3},
		{"Камерний концерт", "draft", 10},
		{"Органна ніч", "cancelled", 20},
	})
	if rec := site.postForm(t, "/org/kyiv-philharmonic/members", url.Values{"email": {"bob@example.com"}}, owner); rec.Code != http.StatusSeeOther {
		t.Fatalf("add member = %d", rec.Code)
	}

	srv := httptest.NewServer(site.h)
	t.Cleanup(srv.Close)
	noAuth := httptest.NewServer(newSite(t, nil))
	t.Cleanup(noAuth.Close)

	info := uiServer{
		Base:       srv.URL,
		NoAuth:     noAuth.URL,
		CookieName: owner.Name,
		Cookies: map[string]string{
			"owner":    owner.Value,
			"member":   member.Value,
			"newcomer": newcomer.Value,
		},
	}
	raw, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("ui snapshot server at %s (sign-in disabled: %s)", srv.URL, noAuth.URL)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case <-ctx.Done():
	case <-time.After(30 * time.Minute):
	}
}

type uiEvent struct {
	title  string
	status string
	inDays int
}

func (s *authSite) seedEvents(t *testing.T, slug string, events []uiEvent) {
	t.Helper()
	ctx := context.Background()
	q := dbgen.New(s.pool)
	org, err := q.GetOrganizerBySlug(ctx, slug)
	if err != nil {
		t.Fatal(err)
	}
	v, err := q.CreateVenue(ctx, dbgen.CreateVenueParams{
		OrganizerID: org.ID, Name: "Національна філармонія", City: "Kyiv", Lat: 50.45, Lng: 30.52, Capacity: 900,
	})
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range events {
		start := time.Date(2030, 3, 1, 19, 0, 0, 0, time.UTC).AddDate(0, 0, e.inDays)
		ev, err := q.CreateEvent(ctx, dbgen.CreateEventParams{
			OrganizerID: org.ID, VenueID: v.ID, Slug: slug + "-event-" + string(rune('a'+i)), Title: e.title,
			StartsAt: start, EndsAt: start.Add(2 * time.Hour), Genre: "classical",
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := q.SetEventStatus(ctx, dbgen.SetEventStatusParams{Status: e.status, ID: ev.ID, OrganizerID: org.ID}); err != nil {
			t.Fatal(err)
		}
	}
}
