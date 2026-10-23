package controllers

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/events"
	"github.com/KotunOleg/Tixo/internal/organizers"
	"github.com/KotunOleg/Tixo/internal/tiers"
	"github.com/KotunOleg/Tixo/internal/venues"
)

type publicEvent struct {
	slug        string
	status      string
	description string
	startsIn    time.Duration
	tiers       []tiers.Input
	soldOut     bool
}

func (s *authSite) publicEvent(t *testing.T, e publicEvent) int64 {
	t.Helper()
	ctx := context.Background()
	u, err := dbgen.New(s.pool).CreateUser(ctx, dbgen.CreateUserParams{Email: e.slug + "@example.com", Name: e.slug})
	if err != nil {
		t.Fatal(err)
	}
	orgs := organizers.New(s.pool)
	org, err := orgs.Create(ctx, u.ID, organizers.CreateInput{Name: "Kyiv Live " + e.slug, Slug: "org-" + e.slug})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := orgs.Update(ctx, org, organizers.UpdateInput{Name: org.Name, ContactEmail: "hello@kyiv.live", ContactTelegram: "@kyivlive"}); err != nil {
		t.Fatal(err)
	}
	v, err := venues.New(s.pool).Create(ctx, org.ID, venues.Input{Name: "Atlas", Address: "Sichovykh Striltsiv 37-41", City: "Kyiv", Lat: 50.4547, Lng: 30.4962, Capacity: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if e.startsIn == 0 {
		e.startsIn = 30 * 24 * time.Hour
	}
	start := time.Now().Add(e.startsIn).Truncate(time.Minute)
	evs := events.New(s.pool)
	ev, err := evs.Create(ctx, org.ID, events.Input{
		VenueID: v.ID, Slug: e.slug, Title: "Show " + e.slug, Description: e.description, Genre: events.GenreRock,
		StartsAt: start, EndsAt: start.Add(3 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range e.tiers {
		tier, err := tiers.New(s.pool).Create(ctx, org.ID, ev.ID, in)
		if err != nil {
			t.Fatal(err)
		}
		if e.soldOut {
			if _, err := s.pool.Exec(ctx, "UPDATE ticket_tiers SET available = 0 WHERE id = $1", tier.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if e.status != events.StatusDraft {
		if _, err := evs.SetStatus(ctx, org.ID, ev.ID, e.status); err != nil {
			t.Fatal(err)
		}
	}
	return ev.ID
}

func TestLandingIsNotFoundForDraftsAndUnknownSlugs(t *testing.T) {
	s := newAuthSite(t)
	s.publicEvent(t, publicEvent{slug: "secret-draft", status: events.StatusDraft})
	for _, path := range []string{"/events/secret-draft", "/en/events/secret-draft", "/events/no-such-event"} {
		if rec := s.request(t, http.MethodGet, path, nil); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, rec.Code)
		}
	}
}

func TestLandingShowsEventInBothLanguages(t *testing.T) {
	s := newAuthSite(t)
	id := s.publicEvent(t, publicEvent{
		slug: "atlas-night", status: events.StatusPublished, description: "First line\nSecond line",
		tiers: []tiers.Input{{Name: "Fan zone", Price: 125000, Quantity: 3}, {Name: "Balcony", Price: 50000, Quantity: 1}},
	})
	q := dbgen.New(s.pool)
	headliner, err := q.CreateArtist(context.Background(), dbgen.CreateArtistParams{Name: "Okean Elzy", SortName: "Okean Elzy"})
	if err != nil {
		t.Fatal(err)
	}
	opener, err := q.CreateArtist(context.Background(), dbgen.CreateArtistParams{Name: "Opening Act", SortName: "Opening Act"})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []struct {
		id       int64
		headline bool
	}{{opener.ID, false}, {headliner.ID, true}} {
		if _, err := s.pool.Exec(context.Background(), "INSERT INTO event_artists (event_id, artist_id, is_headliner) VALUES ($1, $2, $3)", id, a.id, a.headline); err != nil {
			t.Fatal(err)
		}
	}

	for path, want := range map[string][]string{
		"/events/atlas-night":    {`<html lang="uk"`, "Show atlas-night", "Квитки від", "500 ₴", "1 250 ₴", "Лишилося 3 квитки", "Лишився 1 квиток", "Купити квиток", "хедлайнер"},
		"/en/events/atlas-night": {`<html lang="en"`, "Show atlas-night", "Tickets from", "₴500", "₴1,250", "3 tickets left", "1 ticket left", "Buy a ticket", "headliner"},
	} {
		rec := s.request(t, http.MethodGet, path, nil)
		body := rec.Body.String()
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", path, rec.Code)
		}
		for _, w := range append(want, "First line\nSecond line", `href="mailto:hello@kyiv.live"`, `href="https://t.me/kyivlive"`, "Atlas", "openstreetmap.org/?mlat=50.454700") {
			if !strings.Contains(body, w) {
				t.Errorf("%s: missing %q", path, w)
			}
		}
		if strings.Index(body, "Okean Elzy") > strings.Index(body, "Opening Act") {
			t.Errorf("%s: headliner is not listed first", path)
		}
	}
}

func TestLandingStatesWithoutPurchase(t *testing.T) {
	s := newAuthSite(t)
	standard := []tiers.Input{{Name: "Standard", Price: 50000, Quantity: 10}}
	s.publicEvent(t, publicEvent{slug: "called-off", status: events.StatusCancelled, tiers: standard})
	s.publicEvent(t, publicEvent{slug: "already-on", status: events.StatusPublished, tiers: standard, startsIn: -time.Hour})
	s.publicEvent(t, publicEvent{slug: "no-tiers-yet", status: events.StatusPublished})
	s.publicEvent(t, publicEvent{slug: "all-gone", status: events.StatusPublished, tiers: standard, soldOut: true})

	for path, want := range map[string]string{
		"/events/called-off":   "Подію скасовано.",
		"/events/already-on":   "Продаж завершено",
		"/events/no-tiers-yet": "Квитки ще не в продажу",
		"/events/all-gone":     "Усе розпродано",
	} {
		rec := s.request(t, http.MethodGet, path, nil)
		body := rec.Body.String()
		if rec.Code != http.StatusOK || !strings.Contains(body, want) {
			t.Errorf("GET %s = %d, want 200 with %q", path, rec.Code, want)
		}
		if strings.Contains(body, "Купити квиток") {
			t.Errorf("GET %s offers to buy", path)
		}
	}
}

func TestLandingEscapesUserText(t *testing.T) {
	s := newAuthSite(t)
	s.publicEvent(t, publicEvent{slug: "xss-show", status: events.StatusPublished, description: `</div><script>alert("x")</script>`})
	body := s.request(t, http.MethodGet, "/events/xss-show", nil).Body.String()
	if strings.Contains(body, `<script>alert`) {
		t.Error("event description is rendered as HTML")
	}
	if !strings.Contains(body, "&lt;script&gt;alert") {
		t.Error("escaped description is missing")
	}
}

func TestHomeLinksUpcomingEventsToLanding(t *testing.T) {
	s := newAuthSite(t)
	s.publicEvent(t, publicEvent{slug: "home-show", status: events.StatusPublished})
	s.publicEvent(t, publicEvent{slug: "hidden-draft", status: events.StatusDraft})
	for path, href := range map[string]string{"/": `href="/events/home-show"`, "/en/": `href="/en/events/home-show"`} {
		body := s.request(t, http.MethodGet, path, nil).Body.String()
		if !strings.Contains(body, href) {
			t.Errorf("%s: missing %s", path, href)
		}
		if strings.Contains(body, "hidden-draft") {
			t.Errorf("%s: lists a draft event", path)
		}
	}
}
