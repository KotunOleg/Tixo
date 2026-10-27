package controllers

import (
	"encoding/json"
	"encoding/xml"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/KotunOleg/Tixo/internal/events"
	"github.com/KotunOleg/Tixo/internal/tiers"
)

var ldScript = regexp.MustCompile(`(?s)<script type="application/ld\+json">(.*?)</script>`)

func eventJSONLD(t *testing.T, body string) map[string]any {
	t.Helper()
	m := ldScript.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no ld+json script on the page")
	}
	var ld map[string]any
	if err := json.Unmarshal([]byte(m[1]), &ld); err != nil {
		t.Fatalf("ld+json is not valid JSON: %v\n%s", err, m[1])
	}
	return ld
}

func TestLandingHasCanonicalOpenGraphAndEventData(t *testing.T) {
	s := newAuthSite(t)
	s.publicEvent(t, publicEvent{
		slug: "seo-show", status: events.StatusPublished, description: strings.Repeat("Довгий опис події з деталями програми. ", 10),
		tiers: []tiers.Input{{Name: "GA", Price: 35000, Quantity: 10}},
	})
	for path, want := range map[string][]string{
		"/events/seo-show": {
			`<link rel="canonical" href="https://tixo.example/events/seo-show">`,
			`<link rel="alternate" hreflang="en" href="https://tixo.example/en/events/seo-show">`,
			`<meta property="og:url" content="https://tixo.example/events/seo-show">`,
			`<meta property="og:locale" content="uk_UA">`,
			`<meta name="twitter:card" content="summary">`,
		},
		"/en/events/seo-show": {
			`<link rel="canonical" href="https://tixo.example/en/events/seo-show">`,
			`<meta property="og:url" content="https://tixo.example/en/events/seo-show">`,
			`<meta property="og:locale" content="en_US">`,
		},
	} {
		body := s.request(t, http.MethodGet, path, nil).Body.String()
		for _, w := range append(want, `<meta property="og:title" content="Show seo-show">`) {
			if !strings.Contains(body, w) {
				t.Errorf("%s: missing %s", path, w)
			}
		}
		desc := regexp.MustCompile(`<meta name="description" content="([^"]*)">`).FindStringSubmatch(body)
		if desc == nil || len([]rune(desc[1])) > 156 || !strings.HasSuffix(desc[1], "…") || !strings.HasPrefix(desc[1], "Довгий опис") {
			t.Errorf("%s: meta description = %v, want the event text cut to ~155 chars", path, desc)
		}

		ld := eventJSONLD(t, body)
		if ld["@type"] != "Event" || ld["name"] != "Show seo-show" || ld["eventStatus"] != "https://schema.org/EventScheduled" {
			t.Errorf("%s: ld = %v", path, ld)
		}
		offers, _ := ld["offers"].([]any)
		if len(offers) != 1 {
			t.Fatalf("%s: offers = %v", path, ld["offers"])
		}
		offer := offers[0].(map[string]any)
		if offer["price"] != "350.00" || offer["priceCurrency"] != "UAH" || offer["availability"] != "https://schema.org/InStock" {
			t.Errorf("%s: offer = %v", path, offer)
		}
		loc := ld["location"].(map[string]any)
		if loc["name"] != "Atlas" || loc["address"].(map[string]any)["addressLocality"] != "Kyiv" {
			t.Errorf("%s: location = %v", path, loc)
		}
	}
}

func TestCancelledEventDataSaysCancelled(t *testing.T) {
	s := newAuthSite(t)
	s.publicEvent(t, publicEvent{slug: "seo-cancelled", status: events.StatusCancelled, tiers: []tiers.Input{{Name: "GA", Price: 35000, Quantity: 10}}})
	ld := eventJSONLD(t, s.request(t, http.MethodGet, "/events/seo-cancelled", nil).Body.String())
	offer := ld["offers"].([]any)[0].(map[string]any)
	if ld["eventStatus"] != "https://schema.org/EventCancelled" || offer["availability"] != "https://schema.org/SoldOut" {
		t.Errorf("cancelled event ld: status %v, availability %v", ld["eventStatus"], offer["availability"])
	}
}

func TestEventDataCannotBreakOutOfScript(t *testing.T) {
	s := newAuthSite(t)
	s.publicEvent(t, publicEvent{slug: "seo-xss", status: events.StatusPublished, description: `</script><script>alert(1)</script> & "quotes"`})
	body := s.request(t, http.MethodGet, "/events/seo-xss", nil).Body.String()
	if strings.Contains(body, "<script>alert(1)") {
		t.Fatal("description escaped the ld+json script")
	}
	if ld := eventJSONLD(t, body); ld["description"] != `</script><script>alert(1)</script> & "quotes"` {
		t.Errorf("ld description = %q, want the original text after JSON decoding", ld["description"])
	}
}

func TestSitemapListsOnlyPublishedEventsInBothLanguages(t *testing.T) {
	s := newAuthSite(t)
	s.publicEvent(t, publicEvent{slug: "map-public", status: events.StatusPublished})
	s.publicEvent(t, publicEvent{slug: "map-draft", status: events.StatusDraft})
	s.publicEvent(t, publicEvent{slug: "map-cancelled", status: events.StatusCancelled})
	rec := s.request(t, http.MethodGet, "/sitemap.xml", nil)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/xml") {
		t.Fatalf("sitemap = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var set struct {
		URLs []struct {
			Loc string `xml:"loc"`
		} `xml:"url"`
	}
	if err := xml.Unmarshal(rec.Body.Bytes(), &set); err != nil {
		t.Fatalf("sitemap is not valid XML: %v", err)
	}
	locs := map[string]bool{}
	for _, u := range set.URLs {
		locs[u.Loc] = true
	}
	for _, want := range []string{"https://tixo.example/", "https://tixo.example/en/", "https://tixo.example/events/map-public", "https://tixo.example/en/events/map-public"} {
		if !locs[want] {
			t.Errorf("sitemap misses %s", want)
		}
	}
	for _, hidden := range []string{"map-draft", "map-cancelled"} {
		if strings.Contains(body, hidden) {
			t.Errorf("sitemap lists %s", hidden)
		}
	}
	if !strings.Contains(body, `<xhtml:link rel="alternate" hreflang="en" href="https://tixo.example/en/events/map-public">`) {
		t.Error("sitemap has no hreflang alternates")
	}
}

func TestRobotsPointsToSitemapAndHidesPrivatePages(t *testing.T) {
	body := do(t, newSite(t, nil), "/robots.txt").Body.String()
	for _, want := range []string{"User-agent: *", "Disallow: /org\n", "Disallow: /en/tickets\n", "Disallow: /events/*/buy\n", "Sitemap: https://tixo.example/sitemap.xml"} {
		if !strings.Contains(body, want) {
			t.Errorf("robots.txt misses %q:\n%s", want, body)
		}
	}
}
