package controllers

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/KotunOleg/Tixo/internal/events"
)

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
			[]string{"tixo-error-summary", `href="#search-q"`}, []string{"tixo-empty", "jazz-night"}},
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
