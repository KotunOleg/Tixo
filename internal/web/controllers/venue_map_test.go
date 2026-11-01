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

func TestVenueMapPageListsVenuesWithoutJavaScript(t *testing.T) {
	s := newAuthSite(t)
	s.publicEvent(t, publicEvent{slug: "rock-show", status: events.StatusPublished, tiers: []tiers.Input{{Name: "Standard", Price: 50000, Quantity: 10}}})
	s.publicEvent(t, publicEvent{slug: "free-show", status: events.StatusPublished, tiers: []tiers.Input{{Name: "Entry", Price: 0, Quantity: 10}}})

	for _, tc := range []struct{ path, want, link, price, free string }{
		{"/map", "Знайдено 2 майданчики", `href="/events/rock-show"`, "від\u00a0500\u00a0₴", "Безкоштовно"},
		{"/en/map", "2 venues found", `href="/en/events/rock-show"`, "from\u00a0₴500", "Free"},
	} {
		rec := s.request(t, http.MethodGet, tc.path, nil)
		body := rec.Body.String()
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", tc.path, rec.Code)
		}
		for _, want := range []string{tc.want, tc.link, tc.price, tc.free, `<form class="tixo-map-filters" id="map-filters" method="get"`, `<a class="nav-link" aria-current="page" href="` + strings.TrimSuffix(tc.path, "/map") + `/map">`} {
			if !strings.Contains(body, want) {
				t.Errorf("GET %s: missing %q", tc.path, want)
			}
		}
		if strings.Contains(body, "від Безкоштовно") || strings.Contains(body, "from Free") {
			t.Errorf("GET %s: free event reads as a price from free", tc.path)
		}
	}
}

func TestVenueMapPageKeepsFiltersAndShowsEmptyState(t *testing.T) {
	s := newAuthSite(t)
	s.publicEvent(t, publicEvent{slug: "rock-show", status: events.StatusPublished})

	body := s.request(t, http.MethodGet, "/map?genre=jazz&max_price=300&from=2030-01-01", nil).Body.String()
	for _, want := range []string{`<option value="jazz" selected>`, `name="max_price" value="300"`, `name="from" value="2030-01-01"`, "Нічого не знайдено", `href="/map">Скинути фільтри`} {
		if !strings.Contains(body, want) {
			t.Errorf("filtered page: missing %q", want)
		}
	}
	if strings.Contains(body, "rock-show") {
		t.Error("filtered page lists an event that does not match the genre")
	}
	if unfiltered := s.request(t, http.MethodGet, "/map", nil).Body.String(); strings.Contains(unfiltered, "Скинути фільтри") {
		t.Error("reset link shown without any filter")
	}
}

func TestVenueMapPageShowsFieldErrors(t *testing.T) {
	s := newAuthSite(t)
	s.publicEvent(t, publicEvent{slug: "rock-show", status: events.StatusPublished})

	rec := s.request(t, http.MethodGet, "/en/map?from=2030-05-10&to=2030-05-09&max_price=-5", nil)
	body := rec.Body.String()
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	for _, want := range []string{
		`role="alert"`, `<a href="#map-to">The to date can&#39;t be earlier than the from date.</a>`, `<a href="#map-max-price">`,
		`id="map-to" name="to" value="2030-05-09" aria-invalid="true" aria-describedby="map-to-error"`,
		`name="max_price" value="-5" aria-describedby="map-max-price-help map-max-price-error" aria-invalid="true"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, "rock-show") || strings.Contains(body, "Nothing found") {
		t.Error("invalid filters must show the errors only, not results or the empty state")
	}
}

func TestVenueMapEmptyStateHasASingleResetLink(t *testing.T) {
	s := newAuthSite(t)
	body := s.request(t, http.MethodGet, "/map?genre=jazz", nil).Body.String()
	if n := strings.Count(body, "Скинути фільтри"); n != 1 {
		t.Errorf("reset links on an empty filtered result = %d, want 1 in the empty state", n)
	}
}
