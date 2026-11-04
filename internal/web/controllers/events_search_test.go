package controllers

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/KotunOleg/Tixo/internal/events"
)

func TestEventsSearchResultsShowThePosterFromTheSearchedEvent(t *testing.T) {
	s := newAuthSite(t)
	s.publicEvent(t, publicEvent{slug: "jazz-with-poster", title: "Jazz With Poster", status: events.StatusPublished, posterKey: "posters/jazz.jpg"})
	rec := s.request(t, http.MethodGet, "/events?q=Poster", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d\n%s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `src="/media/posters/jazz.jpg"`) {
		t.Errorf("GET /events?q=Poster: missing poster image, got:\n%s", rec.Body)
	}
}

func TestEventsSearchTruncatesAndSaysSo(t *testing.T) {
	s := newAuthSite(t)
	for i := range events.SearchMaxLimit + 2 {
		s.publicEvent(t, publicEvent{slug: "matching-" + strconv.Itoa(i), title: "Matching Show " + strconv.Itoa(i), status: events.StatusPublished})
	}
	rec := s.request(t, http.MethodGet, "/en/events?q=matching", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d\n%s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if got := strings.Count(body, `href="/en/events/matching-`); got != events.SearchMaxLimit {
		t.Errorf("results = %d, want capped at %d", got, events.SearchMaxLimit)
	}
	if !strings.Contains(body, "Showing the first matches") {
		t.Errorf("GET /en/events?q=matching: missing truncation notice, got:\n%s", body)
	}
}

func TestEventsSearchPage(t *testing.T) {
	s := newAuthSite(t)
	s.publicEvent(t, publicEvent{slug: "jazz-night", title: "Jazz Night", status: events.StatusPublished})
	s.publicEvent(t, publicEvent{slug: "hidden-jazz", title: "Hidden Jazz", status: events.StatusDraft})
	s.publicEvent(t, publicEvent{slug: "rock-show", title: "Rock Show", status: events.StatusPublished})

	for _, tc := range []struct {
		name, path string
		status     int
		want       []string
		notWant    []string
	}{
		{"matching query lists only the matching published event", "/events?q=jazz", http.StatusOK,
			[]string{`href="/events/jazz-night"`, "Jazz Night"}, []string{"rock-show", "hidden-jazz", "tixo-empty"}},
		{"english route keeps the language prefix in card links", "/en/events?q=jazz", http.StatusOK,
			[]string{`href="/en/events/jazz-night"`}, []string{"rock-show"}},
		{"empty query falls back to upcoming events like the home page", "/events", http.StatusOK,
			[]string{`href="/events/jazz-night"`, `href="/events/rock-show"`}, []string{"hidden-jazz", "tixo-error-summary"}},
		{"query without matches shows the empty state with map and assistant links", "/events?q=nonsense123", http.StatusOK,
			[]string{"tixo-empty", `href="/map"`, `href="/assistant"`}, []string{"jazz-night", "tixo-error-summary"}},
		{"query over 100 runes is rejected before searching", "/events?q=" + strings.Repeat("a", 101), http.StatusBadRequest,
			[]string{"tixo-error-summary", `href="#search-q"`, `id="search-q-error"`}, []string{"tixo-empty", "jazz-night"}},
		{"markup in the query is escaped in heading and field", "/en/events?q=" + url.QueryEscape("<script>alert(1)</script>"), http.StatusOK,
			[]string{"&lt;script&gt;alert(1)&lt;/script&gt;"}, []string{"<script>alert(1)"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := s.request(t, http.MethodGet, tc.path, nil)
			if rec.Code != tc.status {
				t.Fatalf("GET %s = %d, want %d\n%s", tc.path, rec.Code, tc.status, rec.Body)
			}
			body := rec.Body.String()
			for _, w := range tc.want {
				if !strings.Contains(body, w) {
					t.Errorf("GET %s: missing %q", tc.path, w)
				}
			}
			for _, w := range tc.notWant {
				if strings.Contains(body, w) {
					t.Errorf("GET %s: unexpected %q", tc.path, w)
				}
			}
		})
	}
}

func TestEventsSearchErrorMessageIDFallsBackForUnknownCodes(t *testing.T) {
	for _, tc := range []struct{ field, code, want string }{
		{events.FieldQuery, events.CodeLength, "events_search.error_length"},
		{events.FieldQuery, events.CodeInvalid, "events_search.error_generic"},
		{events.FieldCity, events.CodeLength, "events_search.error_generic"},
	} {
		if got := eventsSearchErrorMessageID(tc.field, tc.code); got != tc.want {
			t.Errorf("eventsSearchErrorMessageID(%q, %q) = %q, want %q", tc.field, tc.code, got, tc.want)
		}
	}
}
