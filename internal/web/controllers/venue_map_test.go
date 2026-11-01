package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/KotunOleg/Tixo/internal/events"
	"github.com/KotunOleg/Tixo/internal/platform/httpx"
	"github.com/KotunOleg/Tixo/internal/tiers"
)

func decodeMap(t *testing.T, body string) mapJSON {
	t.Helper()
	var got mapJSON
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return got
}

func TestVenueMapJSONListsPublishedEventsWithLocalizedLinks(t *testing.T) {
	s := newAuthSite(t)
	s.publicEvent(t, publicEvent{slug: "rock-show", status: events.StatusPublished, tiers: []tiers.Input{{Name: "Standard", Price: 50000, Quantity: 10}}})
	s.publicEvent(t, publicEvent{slug: "secret-draft", status: events.StatusDraft})

	for _, tc := range []struct{ path, wantURL string }{
		{"/map/venues.json", "/events/rock-show"},
		{"/en/map/venues.json", "/en/events/rock-show"},
	} {
		rec := s.request(t, http.MethodGet, tc.path, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d\n%s", tc.path, rec.Code, rec.Body)
		}
		if ct, cc := rec.Header().Get("Content-Type"), rec.Header().Get("Cache-Control"); ct != "application/json" || cc != "no-store" {
			t.Errorf("GET %s Content-Type = %q, Cache-Control = %q; want json that is never cached", tc.path, ct, cc)
		}
		got := decodeMap(t, rec.Body.String())
		if len(got.Venues) != 1 || len(got.Venues[0].Events) != 1 {
			t.Fatalf("GET %s venues = %+v, want one venue with the published event only", tc.path, got.Venues)
		}
		v, e := got.Venues[0], got.Venues[0].Events[0]
		if v.Name != "Atlas" || v.City != "Kyiv" || v.Lat != 50.4547 || e.URL != tc.wantURL || e.PriceFrom == nil || *e.PriceFrom != 50000 {
			t.Errorf("GET %s = venue %+v event %+v", tc.path, v, e)
		}
	}

	rec := s.request(t, http.MethodGet, "/map/venues.json?genre=jazz", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"venues":[]`) {
		t.Errorf("filter without matches = %d %s, want 200 with an empty venues array, not null", rec.Code, rec.Body)
	}
}

func TestVenueMapPriceMatchesTheLandingPage(t *testing.T) {
	s := newAuthSite(t)
	s.publicEvent(t, publicEvent{
		slug: "sold-out-show", status: events.StatusPublished, soldOut: true,
		tiers: []tiers.Input{{Name: "Fan zone", Price: 125000, Quantity: 3}, {Name: "Balcony", Price: 50000, Quantity: 1}},
	})
	pub, err := events.New(s.pool).PublicBySlug(context.Background(), "sold-out-show")
	if err != nil {
		t.Fatal(err)
	}
	_, landing := tiersFrom(pub.Tiers)

	got := decodeMap(t, s.request(t, http.MethodGet, "/map/venues.json", nil).Body.String())
	if len(got.Venues) != 1 || got.Venues[0].Events[0].PriceFrom == nil || *got.Venues[0].Events[0].PriceFrom != landing {
		t.Errorf("map price = %+v, landing price from = %d", got.Venues, landing)
	}
}

func TestVenueMapJSONRejectsInvalidFilters(t *testing.T) {
	s := newAuthSite(t)
	for _, tc := range []struct{ query, field string }{
		{"from=2030-13-01", events.FieldFrom},
		{"to=tomorrow", events.FieldTo},
		{"from=2030-05-10&to=2030-05-09", events.FieldTo},
		{"genre=disco", events.FieldGenre},
		{"max_price=-1", events.FieldMaxPrice},
		{"max_price=cheap", events.FieldMaxPrice},
	} {
		rec := s.request(t, http.MethodGet, "/map/venues.json?"+tc.query, nil)
		var body httpx.ErrorBody
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusBadRequest || body.Error != "invalid map filters" || body.Fields[tc.field] == "" {
			t.Errorf("?%s = %d %s, want 400 with an english message and %s in fields", tc.query, rec.Code, rec.Body, tc.field)
		}
	}
}

func TestVenueMapJSONKeepsMarkupInTitlesInert(t *testing.T) {
	s := newAuthSite(t)
	title := `<script>alert("x")</script><img src=x onerror=alert(1)>`
	s.publicEvent(t, publicEvent{slug: "xss-show", title: title, status: events.StatusPublished})

	rec := s.request(t, http.MethodGet, "/map/venues.json", nil)
	if body := rec.Body.String(); strings.Contains(body, "<script>") || strings.Contains(body, "<img") {
		t.Errorf("raw markup in json body: %s", body)
	}
	got := decodeMap(t, rec.Body.String())
	if len(got.Venues) != 1 || got.Venues[0].Events[0].Title != title {
		t.Errorf("title after decoding = %+v, want the original text back", got.Venues)
	}
}
